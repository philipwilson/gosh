package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gosh/expander"
	"gosh/lexer"
	"gosh/parser"
)

// execSubshell runs commands in an isolated variable scope.
// Variable changes inside the subshell do not affect the parent.
func execSubshell(state *shellState, cmd *parser.SubshellCmd, stdin, stdout, stderr *os.File) int {
	// Save shell state.
	savedVars := make(map[string]string, len(state.vars))
	for k, v := range state.vars {
		savedVars[k] = v
	}
	savedArrays := make(map[string][]string, len(state.arrays))
	for k, v := range state.arrays {
		cp := make([]string, len(v))
		copy(cp, v)
		savedArrays[k] = cp
	}
	savedAttrs := make(map[string]uint8, len(state.attrs))
	for k, v := range state.attrs {
		savedAttrs[k] = v
	}
	savedFuncs := make(map[string]*parser.List, len(state.funcs))
	for k, v := range state.funcs {
		savedFuncs[k] = v
	}
	savedAliases := make(map[string]string, len(state.aliases))
	for k, v := range state.aliases {
		savedAliases[k] = v
	}
	savedParams := state.positionalParams
	savedExitFlag := state.exitFlag
	savedOptErrexit := state.optErrexit
	savedOptNounset := state.optNounset
	savedOptXtrace := state.optXtrace
	savedOptPipefail := state.optPipefail
	state.trapsMu.RLock()
	savedTraps := make(map[string]string, len(state.traps))
	for k, v := range state.traps {
		savedTraps[k] = v
	}
	state.trapsMu.RUnlock()

	// Run the body.
	body := parser.CloneList(cmd.Body)
	execList(state, body, stdin, stdout, stderr)
	status := state.lastStatus

	// Restore shell state.
	state.vars = savedVars
	state.arrays = savedArrays
	state.attrs = savedAttrs
	state.funcs = savedFuncs
	state.aliases = savedAliases
	state.positionalParams = savedParams
	state.exitFlag = savedExitFlag
	state.optErrexit = savedOptErrexit
	state.optNounset = savedOptNounset
	state.optXtrace = savedOptXtrace
	state.optPipefail = savedOptPipefail
	state.trapsMu.Lock()
	state.traps = savedTraps
	state.trapsMu.Unlock()
	state.lastStatus = status

	return status
}

// execArithCmd evaluates a (( expr )) arithmetic command.
// Returns 0 if the expression result is non-zero, 1 if zero (bash semantics).
func execArithCmd(state *shellState, cmd *parser.ArithCmd, stderr *os.File) int {
	lookup := func(name string) string { return state.lookup(name) }
	setVar := func(name, value string) { state.setVar(name, value) }

	// Expand variables in the expression before evaluation.
	expr := expander.ExpandDollar(cmd.Expr, lookup)

	val, err := expander.EvalArith(expr, lookup, setVar)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: ((%s)): %s\n", cmd.Expr, err)
		return 1
	}
	if val != 0 {
		return 0
	}
	return 1
}

// execIf evaluates an if/elif/else/fi command. Each condition and
// body is expanded lazily — only the taken branch is expanded.
func execIf(state *shellState, cmd *parser.IfCmd, stdin, stdout, stderr *os.File) int {
	for _, clause := range cmd.Clauses {
		cond := parser.CloneList(clause.Condition)
		state.noErrexit++
		execList(state, cond, stdin, stdout, stderr)
		state.noErrexit--

		if state.lastStatus == 0 {
			body := parser.CloneList(clause.Body)
			execList(state, body, stdin, stdout, stderr)
			return state.lastStatus
		}
	}

	if cmd.ElseBody != nil {
		body := parser.CloneList(cmd.ElseBody)
		execList(state, body, stdin, stdout, stderr)
		return state.lastStatus
	}

	// No branch taken — exit status is 0 (bash behavior).
	state.lastStatus = 0
	return 0
}

// execWhile evaluates a while/do/done loop. The condition and body
// are cloned before each iteration so that $VAR references in the
// original AST are preserved for re-expansion.
func execWhile(state *shellState, cmd *parser.WhileCmd, stdin, stdout, stderr *os.File) int {
	state.loopDepth++
	defer func() { state.loopDepth-- }()

	for {
		cond := parser.CloneList(cmd.Condition)
		state.noErrexit++
		execList(state, cond, stdin, stdout, stderr)
		state.noErrexit--

		if state.lastStatus != 0 || state.breakFlag {
			state.breakFlag = false
			// A while loop that exits because its condition is false
			// has exit status 0 (bash behavior).
			state.lastStatus = 0
			break
		}

		body := parser.CloneList(cmd.Body)
		execList(state, body, stdin, stdout, stderr)

		if state.exitFlag || state.returnFlag || state.breakFlag {
			if state.breakFlag {
				state.breakFlag = false
			}
			return state.lastStatus
		}
		if state.continueFlag {
			state.continueFlag = false
		}
	}

	return state.lastStatus
}

// execUntil evaluates an until/do/done loop. Like while but with
// an inverted condition: runs body while condition is non-zero.
func execUntil(state *shellState, cmd *parser.UntilCmd, stdin, stdout, stderr *os.File) int {
	state.loopDepth++
	defer func() { state.loopDepth-- }()

	for {
		cond := parser.CloneList(cmd.Condition)
		state.noErrexit++
		execList(state, cond, stdin, stdout, stderr)
		state.noErrexit--

		if state.lastStatus == 0 || state.breakFlag {
			state.breakFlag = false
			state.lastStatus = 0
			break
		}

		body := parser.CloneList(cmd.Body)
		execList(state, body, stdin, stdout, stderr)

		if state.exitFlag || state.returnFlag || state.breakFlag {
			if state.breakFlag {
				state.breakFlag = false
			}
			return state.lastStatus
		}
		if state.continueFlag {
			state.continueFlag = false
		}
	}

	return state.lastStatus
}

// execFor evaluates a for/in/do/done loop. The word list is expanded
// once (variables + globs), then the body runs for each resulting word
// with the loop variable set.
func execFor(state *shellState, cmd *parser.ForCmd, stdin, stdout, stderr *os.File) int {
	// Expand the word list: variable expansion + glob expansion.
	// Build a temporary SimpleCmd to reuse the expander's word logic.
	expandedWords := make([]lexer.Word, len(cmd.Words))
	for i, w := range cmd.Words {
		expandedWords[i] = parser.CloneWord(w)
	}
	tmpCmd := &parser.SimpleCmd{Args: expandedWords}
	expander.Expand(&parser.List{
		Entries: []parser.ListEntry{{
			Pipeline: &parser.Pipeline{Cmds: []parser.Command{tmpCmd}},
		}},
	}, state.lookupNounset, state.cmdSubst, state.setVar, state.lookupArray, state.isVarSet)

	// Collect the expanded arg strings.
	values := tmpCmd.ArgStrings()

	if len(values) == 0 {
		state.lastStatus = 0
		return 0
	}

	state.loopDepth++
	defer func() { state.loopDepth-- }()

	for _, val := range values {
		state.setVar(cmd.VarName, val)

		body := parser.CloneList(cmd.Body)
		execList(state, body, stdin, stdout, stderr)

		if state.exitFlag || state.returnFlag || state.breakFlag {
			if state.breakFlag {
				state.breakFlag = false
			}
			return state.lastStatus
		}
		if state.continueFlag {
			state.continueFlag = false
		}
	}

	return state.lastStatus
}

// execArithFor evaluates a for (( init; cond; step )) do body done loop.
func execArithFor(state *shellState, cmd *parser.ArithForCmd, stdin, stdout, stderr *os.File) int {
	lookup := func(name string) string { return state.lookup(name) }
	setVar := func(name, value string) { state.setVar(name, value) }

	// Evaluate init expression.
	if cmd.Init != "" {
		initExpr := expander.ExpandDollar(cmd.Init, lookup)
		if _, err := expander.EvalArith(initExpr, lookup, setVar); err != nil {
			fmt.Fprintf(stderr, "gosh: for((%s)): %s\n", cmd.Init, err)
			return 1
		}
	}

	state.loopDepth++
	defer func() { state.loopDepth-- }()

	for {
		// Evaluate condition (empty condition = infinite loop).
		if cmd.Cond != "" {
			condExpr := expander.ExpandDollar(cmd.Cond, lookup)
			val, err := expander.EvalArith(condExpr, lookup, setVar)
			if err != nil {
				fmt.Fprintf(stderr, "gosh: for((%s)): %s\n", cmd.Cond, err)
				return 1
			}
			if val == 0 {
				break
			}
		}

		// Execute body.
		body := parser.CloneList(cmd.Body)
		execList(state, body, stdin, stdout, stderr)

		if state.exitFlag || state.returnFlag || state.breakFlag {
			if state.breakFlag {
				state.breakFlag = false
			}
			return state.lastStatus
		}
		if state.continueFlag {
			state.continueFlag = false
		}

		// Evaluate step expression.
		if cmd.Step != "" {
			stepExpr := expander.ExpandDollar(cmd.Step, lookup)
			if _, err := expander.EvalArith(stepExpr, lookup, setVar); err != nil {
				fmt.Fprintf(stderr, "gosh: for((%s)): %s\n", cmd.Step, err)
				return 1
			}
		}
	}

	return state.lastStatus
}

// execCase evaluates a case/in/esac command. The word is expanded once,
// then each clause's patterns are expanded and matched using filepath.Match.
// The body of the first matching clause is executed.
func execCase(state *shellState, cmd *parser.CaseCmd, stdin, stdout, stderr *os.File) int {
	// Expand the subject word.
	word := parser.CloneWord(cmd.Word)
	tmpCmd := &parser.SimpleCmd{Args: []lexer.Word{word}}
	expander.Expand(&parser.List{
		Entries: []parser.ListEntry{{
			Pipeline: &parser.Pipeline{Cmds: []parser.Command{tmpCmd}},
		}},
	}, state.lookupNounset, state.cmdSubst, state.setVar, state.lookupArray, state.isVarSet)
	subject := tmpCmd.ArgStrings()[0]

	for _, clause := range cmd.Clauses {
		for _, pat := range clause.Patterns {
			// Expand variables in the pattern but NOT globs —
			// glob metacharacters are used for matching, not file expansion.
			pattern := expander.ExpandWord(parser.CloneWord(pat), state.lookup)

			matched, err := filepath.Match(pattern, subject)
			if err != nil {
				continue
			}
			if matched {
				body := parser.CloneList(clause.Body)
				execList(state, body, stdin, stdout, stderr)
				return state.lastStatus
			}
		}
	}

	// No match — exit status 0.
	state.lastStatus = 0
	return 0
}

// execSelect runs a select/in/do/done interactive menu loop.
// Displays a numbered menu, reads user input from stdin, sets the
// loop variable to the selected item (or empty for invalid), and
// sets REPLY to the raw input.
func execSelect(state *shellState, cmd *parser.SelectCmd, stdin, stdout, stderr *os.File) int {
	// Expand the word list (same pattern as execFor).
	expandedWords := make([]lexer.Word, len(cmd.Words))
	for i, w := range cmd.Words {
		expandedWords[i] = parser.CloneWord(w)
	}
	tmpCmd := &parser.SimpleCmd{Args: expandedWords}
	expander.Expand(&parser.List{
		Entries: []parser.ListEntry{{
			Pipeline: &parser.Pipeline{Cmds: []parser.Command{tmpCmd}},
		}},
	}, state.lookupNounset, state.cmdSubst, state.setVar, state.lookupArray, state.isVarSet)
	values := tmpCmd.ArgStrings()

	if len(values) == 0 {
		state.lastStatus = 0
		return 0
	}

	state.loopDepth++
	defer func() { state.loopDepth-- }()

	reader := bufio.NewReader(stdin)
	displayMenu := true

	for {
		if displayMenu {
			for i, v := range values {
				fmt.Fprintf(stderr, "%d) %s\n", i+1, v)
			}
			displayMenu = false
		}

		// Display PS3 prompt.
		ps3 := state.vars["PS3"]
		if ps3 == "" {
			ps3 = "#? "
		}
		fmt.Fprint(stderr, ps3)

		line, err := reader.ReadString('\n')
		if err != nil {
			// EOF — exit loop.
			break
		}
		line = strings.TrimRight(line, "\n\r")

		if line == "" {
			// Empty input — redisplay menu.
			displayMenu = true
			continue
		}

		// Try to parse as number.
		n, parseErr := strconv.Atoi(line)
		if parseErr != nil || n < 1 || n > len(values) {
			state.setVar(cmd.VarName, "")
		} else {
			state.setVar(cmd.VarName, values[n-1])
		}
		state.setVar("REPLY", line)

		body := parser.CloneList(cmd.Body)
		execList(state, body, stdin, stdout, stderr)

		if state.exitFlag || state.returnFlag || state.breakFlag {
			if state.breakFlag {
				state.breakFlag = false
			}
			return state.lastStatus
		}
		if state.continueFlag {
			state.continueFlag = false
			displayMenu = true
		}
	}

	state.lastStatus = 0
	return 0
}
