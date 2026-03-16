package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"gosh/expander"
	"gosh/lexer"
	"gosh/parser"
)

// execList runs pipelines connected by ;, &&, and ||.
// Each entry is expanded just before execution (lazy expansion) so
// that assignments in earlier entries are visible to later ones.
//
//	;  — always run the next pipeline
//	&& — run the next pipeline only if the previous succeeded (status 0)
//	|| — run the next pipeline only if the previous failed (status != 0)
func execList(state *shellState, list *parser.List, stdin, stdout *os.File) {
	for i := range list.Entries {
		if i > 0 {
			prevOp := list.Entries[i-1].Op
			switch prevOp {
			case "&&":
				if state.lastStatus != 0 {
					continue
				}
			case "||":
				if state.lastStatus == 0 {
					continue
				}
			}
		}

		// Suppress errexit for LHS of && and ||.
		suppressErrexit := list.Entries[i].Op == "&&" || list.Entries[i].Op == "||"
		if suppressErrexit {
			state.noErrexit++
		}

		// Expand this entry just before execution.
		singleList := &parser.List{Entries: []parser.ListEntry{list.Entries[i]}}
		expander.Expand(singleList, state.lookup, state.cmdSubst, state.setVar, state.lookupArray)
		list.Entries[i] = singleList.Entries[0]

		// Check for nounset error during expansion.
		if state.nounsetError {
			state.nounsetError = false
			state.lastStatus = 1
			state.runTrapWithIO("ERR", stdin, stdout)
			if suppressErrexit {
				state.noErrexit--
			}
			if state.optErrexit && state.noErrexit == 0 {
				state.exitFlag = true
				return
			}
			continue
		}

		if state.debugExpanded {
			fmt.Fprintf(os.Stderr, "  %s\n", list.Entries[i].Pipeline)
		}

		if list.Entries[i].Op == "&" {
			execBackground(state, list.Entries[i].Pipeline)
		} else {
			execPipeline(state, list.Entries[i].Pipeline, stdin, stdout)
		}

		// Run pending signal traps and ERR trap.
		state.runPendingTrapsWithIO(stdin, stdout)
		if state.lastStatus != 0 {
			state.runTrapWithIO("ERR", stdin, stdout)
		}

		if suppressErrexit {
			state.noErrexit--
		}

		// Errexit: exit if command failed and not suppressed.
		// Don't fire for the LHS of && or || (suppressErrexit was true).
		if !suppressErrexit && state.optErrexit && state.noErrexit == 0 && state.lastStatus != 0 {
			state.exitFlag = true
			return
		}

		if state.exitFlag || state.breakFlag || state.continueFlag || state.returnFlag {
			return
		}
	}
}

// execBackground starts a pipeline in the background without waiting.
func execBackground(state *shellState, pipe *parser.Pipeline) {
	n := len(pipe.Cmds)

	type pipePair struct{ r, w *os.File }
	var pipes []pipePair
	if n > 1 {
		pipes = make([]pipePair, n-1)
		for i := range pipes {
			r, w, err := os.Pipe()
			if err != nil {
				fmt.Fprintf(os.Stderr, "gosh: pipe: %v\n", err)
				return
			}
			pipes[i] = pipePair{r, w}
		}
	}

	pgid := 0
	var pids []int

	for i, cmd := range pipe.Cmds {
		simple, ok := cmd.(*parser.SimpleCmd)
		if !ok {
			fmt.Fprintf(os.Stderr, "gosh: compound commands not supported in background pipelines\n")
			continue
		}

		var stdin *os.File
		if i == 0 {
			stdin = os.Stdin
		} else {
			stdin = pipes[i-1].r
		}

		var stdout *os.File
		if i == n-1 {
			stdout = os.Stdout
		} else {
			stdout = pipes[i].w
		}

		fds := [3]*os.File{stdin, stdout, os.Stderr}
		proc, opened := startProcess(state, simple, fds, pgid)
		// Close files opened by redirects immediately — the child has
		// inherited them.
		for _, f := range opened {
			f.Close()
		}
		if proc == nil {
			continue
		}
		pids = append(pids, proc.Pid)

		if i == 0 {
			pgid = proc.Pid
			syscall.Setpgid(proc.Pid, proc.Pid)
		}
	}

	for _, p := range pipes {
		p.r.Close()
		p.w.Close()
	}

	if len(pids) == 0 {
		return
	}

	cmdParts := make([]string, n)
	for i, cmd := range pipe.Cmds {
		if simple, ok := cmd.(*parser.SimpleCmd); ok {
			cmdParts[i] = strings.Join(simple.ArgStrings(), " ")
		}
	}
	cmdText := strings.Join(cmdParts, " | ")

	j := state.addJob(pgid, pids, cmdText, jobRunning)
	state.lastBgPid = pgid
	fmt.Fprintf(os.Stderr, "[%d] %d\n", j.id, pgid)
	state.lastStatus = 0
}

// execPipeline runs a pipeline of one or more commands.
// Terminal foreground control is only applied when not inside a
// command substitution (state.substDepth == 0).
func execPipeline(state *shellState, pipe *parser.Pipeline, stdin, stdout *os.File) {
	n := len(pipe.Cmds)

	if n == 1 {
		state.lastStatus = execCommand(state, pipe.Cmds[0], stdin, stdout)
		return
	}

	// In a pipeline, builtins fall through to external command
	// lookup. Running a builtin in a pipeline would require forking
	// (to wire its output to a pipe), which defeats the purpose.
	// External /bin/echo, /bin/pwd etc. handle this case.

	foreground := state.interactive && state.substDepth == 0

	type pipePair struct{ r, w *os.File }
	pipes := make([]pipePair, n-1)
	for i := range pipes {
		r, w, err := os.Pipe()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: pipe: %v\n", err)
			return
		}
		pipes[i] = pipePair{r, w}
	}

	type procInfo struct {
		proc  *os.Process
		files []*os.File
	}
	infos := make([]procInfo, n)
	pgid := 0

	for i, cmd := range pipe.Cmds {
		simple, ok := cmd.(*parser.SimpleCmd)
		if !ok {
			fmt.Fprintf(os.Stderr, "gosh: compound commands not supported in pipelines\n")
			continue
		}

		var sin *os.File
		if i == 0 {
			sin = stdin
		} else {
			sin = pipes[i-1].r
		}

		var sout *os.File
		if i == n-1 {
			sout = stdout
		} else {
			sout = pipes[i].w
		}

		fds := [3]*os.File{sin, sout, os.Stderr}
		proc, opened := startProcess(state, simple, fds, pgid)
		infos[i] = procInfo{proc: proc, files: opened}

		if i == 0 && proc != nil {
			pgid = proc.Pid
			syscall.Setpgid(proc.Pid, proc.Pid)
			if foreground {
				tcsetpgrp(state.termFd, pgid)
			}
		}
	}

	for _, p := range pipes {
		p.r.Close()
		p.w.Close()
	}

	var pids []int
	for _, info := range infos {
		if info.proc != nil {
			pids = append(pids, info.proc.Pid)
		}
	}

	anyStopped := false
	var lastNonZero int
	for i, info := range infos {
		if info.proc == nil {
			continue
		}
		res := waitProc(info.proc)
		for _, f := range info.files {
			f.Close()
		}
		if res.stopped {
			anyStopped = true
		}
		if res.status != 0 {
			lastNonZero = res.status
		}
		if i == n-1 {
			if state.optPipefail && lastNonZero != 0 {
				state.lastStatus = lastNonZero
			} else {
				state.lastStatus = res.status
			}
		}
	}

	if foreground {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	if anyStopped {
		cmdParts := make([]string, n)
		for i, cmd := range pipe.Cmds {
			if simple, ok := cmd.(*parser.SimpleCmd); ok {
				cmdParts[i] = strings.Join(simple.ArgStrings(), " ")
			}
		}
		cmdText := strings.Join(cmdParts, " | ")
		j := state.addJob(pgid, pids, cmdText, jobStopped)
		fmt.Fprintf(os.Stderr, "[%d]+  Stopped                 %s\n", j.id, j.cmd)
	}
}

// execCommand dispatches a Command (simple or compound) for execution.
func execCommand(state *shellState, cmd parser.Command, stdin, stdout *os.File) int {
	switch c := cmd.(type) {
	case *parser.SimpleCmd:
		return execSimple(state, c, stdin, stdout)
	case *parser.IfCmd:
		return execIf(state, c, stdin, stdout)
	case *parser.WhileCmd:
		return execWhile(state, c, stdin, stdout)
	case *parser.ForCmd:
		return execFor(state, c, stdin, stdout)
	case *parser.ArithForCmd:
		return execArithFor(state, c, stdin, stdout)
	case *parser.CaseCmd:
		return execCase(state, c, stdin, stdout)
	case *parser.DblBracketCmd:
		return execDblBracket(state, c)
	case *parser.SubshellCmd:
		return execSubshell(state, c, stdin, stdout)
	case *parser.ArithCmd:
		return execArithCmd(state, c)
	case *parser.FuncDef:
		state.funcs[c.Name] = c.Body
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gosh: unknown command type\n")
		return 1
	}
}

// execSubshell runs commands in an isolated variable scope.
// Variable changes inside the subshell do not affect the parent.
func execSubshell(state *shellState, cmd *parser.SubshellCmd, stdin, stdout *os.File) int {
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
	savedExported := make(map[string]bool, len(state.exported))
	for k, v := range state.exported {
		savedExported[k] = v
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
	savedTraps := make(map[string]string, len(state.traps))
	for k, v := range state.traps {
		savedTraps[k] = v
	}

	// Run the body.
	body := parser.CloneList(cmd.Body)
	execList(state, body, stdin, stdout)
	status := state.lastStatus

	// Restore shell state.
	state.vars = savedVars
	state.arrays = savedArrays
	state.exported = savedExported
	state.funcs = savedFuncs
	state.aliases = savedAliases
	state.positionalParams = savedParams
	state.exitFlag = savedExitFlag
	state.optErrexit = savedOptErrexit
	state.optNounset = savedOptNounset
	state.optXtrace = savedOptXtrace
	state.optPipefail = savedOptPipefail
	state.traps = savedTraps
	state.lastStatus = status

	return status
}

// execArithCmd evaluates a (( expr )) arithmetic command.
// Returns 0 if the expression result is non-zero, 1 if zero (bash semantics).
func execArithCmd(state *shellState, cmd *parser.ArithCmd) int {
	lookup := func(name string) string { return state.lookup(name) }
	setVar := func(name, value string) { state.setVar(name, value) }

	// Expand variables in the expression before evaluation.
	expr := expander.ExpandDollar(cmd.Expr, lookup)

	val, err := expander.EvalArith(expr, lookup, setVar)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: ((%s)): %s\n", cmd.Expr, err)
		return 1
	}
	if val != 0 {
		return 0
	}
	return 1
}

// execIf evaluates an if/elif/else/fi command. Each condition and
// body is expanded lazily — only the taken branch is expanded.
func execIf(state *shellState, cmd *parser.IfCmd, stdin, stdout *os.File) int {
	for _, clause := range cmd.Clauses {
		cond := parser.CloneList(clause.Condition)
		state.noErrexit++
		execList(state, cond, stdin, stdout)
		state.noErrexit--

		if state.lastStatus == 0 {
			body := parser.CloneList(clause.Body)
			execList(state, body, stdin, stdout)
			return state.lastStatus
		}
	}

	if cmd.ElseBody != nil {
		body := parser.CloneList(cmd.ElseBody)
		execList(state, body, stdin, stdout)
		return state.lastStatus
	}

	// No branch taken — exit status is 0 (bash behavior).
	state.lastStatus = 0
	return 0
}

// execWhile evaluates a while/do/done loop. The condition and body
// are cloned before each iteration so that $VAR references in the
// original AST are preserved for re-expansion.
func execWhile(state *shellState, cmd *parser.WhileCmd, stdin, stdout *os.File) int {
	state.loopDepth++
	defer func() { state.loopDepth-- }()

	for {
		cond := parser.CloneList(cmd.Condition)
		state.noErrexit++
		execList(state, cond, stdin, stdout)
		state.noErrexit--

		if state.lastStatus != 0 || state.breakFlag {
			state.breakFlag = false
			// A while loop that exits because its condition is false
			// has exit status 0 (bash behavior).
			state.lastStatus = 0
			break
		}

		body := parser.CloneList(cmd.Body)
		execList(state, body, stdin, stdout)

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
func execFor(state *shellState, cmd *parser.ForCmd, stdin, stdout *os.File) int {
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
	}, state.lookup, state.cmdSubst, state.setVar, state.lookupArray)

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
		execList(state, body, stdin, stdout)

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
func execArithFor(state *shellState, cmd *parser.ArithForCmd, stdin, stdout *os.File) int {
	lookup := func(name string) string { return state.lookup(name) }
	setVar := func(name, value string) { state.setVar(name, value) }

	// Evaluate init expression.
	if cmd.Init != "" {
		initExpr := expander.ExpandDollar(cmd.Init, lookup)
		if _, err := expander.EvalArith(initExpr, lookup, setVar); err != nil {
			fmt.Fprintf(os.Stderr, "gosh: for((%s)): %s\n", cmd.Init, err)
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
				fmt.Fprintf(os.Stderr, "gosh: for((%s)): %s\n", cmd.Cond, err)
				return 1
			}
			if val == 0 {
				break
			}
		}

		// Execute body.
		body := parser.CloneList(cmd.Body)
		execList(state, body, stdin, stdout)

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
				fmt.Fprintf(os.Stderr, "gosh: for((%s)): %s\n", cmd.Step, err)
				return 1
			}
		}
	}

	return state.lastStatus
}

// execCase evaluates a case/in/esac command. The word is expanded once,
// then each clause's patterns are expanded and matched using filepath.Match.
// The body of the first matching clause is executed.
func execCase(state *shellState, cmd *parser.CaseCmd, stdin, stdout *os.File) int {
	// Expand the subject word.
	word := parser.CloneWord(cmd.Word)
	tmpCmd := &parser.SimpleCmd{Args: []lexer.Word{word}}
	expander.Expand(&parser.List{
		Entries: []parser.ListEntry{{
			Pipeline: &parser.Pipeline{Cmds: []parser.Command{tmpCmd}},
		}},
	}, state.lookup, state.cmdSubst, state.setVar, state.lookupArray)
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
				execList(state, body, stdin, stdout)
				return state.lastStatus
			}
		}
	}

	// No match — exit status 0.
	state.lastStatus = 0
	return 0
}

// execSimple runs a single command (not in a pipeline).
// Builtins are handled here; in pipelines they fall through
// to external commands.
func execSimple(state *shellState, cmd *parser.SimpleCmd, stdin, stdout *os.File) int {
	// Handle assignment-only commands: just set variables.
	if len(cmd.Args) == 0 {
		for _, a := range cmd.Assigns {
			execAssignment(state, a)
		}
		return 0
	}

	// Process substitution: replace <(cmd) / >(cmd) args with /dev/fd/N.
	procCleanup := processProcSubsts(state, cmd, stdin, stdout)
	defer procCleanup()

	args := cmd.ArgStrings()

	// Special-case exec: needs access to the SimpleCmd redirects.
	if len(args) > 0 && args[0] == "exec" {
		return execExec(state, cmd, args[1:], stdin, stdout)
	}

	// Xtrace: print commands before execution.
	if state.optXtrace && len(args) > 0 {
		ps4 := state.vars["PS4"]
		if ps4 == "" {
			ps4 = "+ "
		}
		fmt.Fprintf(os.Stderr, "%s%s\n", ps4, strings.Join(args, " "))
	}

	// Check for user-defined functions.
	if body, ok := state.funcs[args[0]]; ok {
		saved := make(map[string]savedVar, len(cmd.Assigns))
		for _, a := range cmd.Assigns {
			saveVarState(state, saved, a.Name)
			execAssignment(state, a)
		}

		fds := [3]*os.File{stdin, stdout, os.Stderr}
		fds, opened, err := applyRedirects(cmd, fds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			restoreVars(state, saved)
			return 1
		}

		status := execFunction(state, body, args[1:], fds[0], fds[1])
		for _, f := range opened {
			f.Close()
		}

		restoreVars(state, saved)
		return status
	}

	// Check for builtins.
	if fn, ok := builtins[args[0]]; ok {
		// Per-command assignments are temporary for builtins:
		// save old values, set new ones, run, then restore.
		saved := make(map[string]savedVar, len(cmd.Assigns))
		for _, a := range cmd.Assigns {
			saveVarState(state, saved, a.Name)
			execAssignment(state, a)
		}

		fds := [3]*os.File{stdin, stdout, os.Stderr}
		fds, opened, err := applyRedirects(cmd, fds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			restoreVars(state, saved)
			return 1
		}
		status := fn(state, args[1:], fds[0], fds[1])
		for _, f := range opened {
			f.Close()
		}

		restoreVars(state, saved)
		return status
	}

	// External command.
	fds := [3]*os.File{stdin, stdout, os.Stderr}
	proc, opened := startProcess(state, cmd, fds, 0)
	if proc == nil {
		return 127
	}

	pgid := proc.Pid
	syscall.Setpgid(pgid, pgid)

	if state.interactive && state.substDepth == 0 {
		tcsetpgrp(state.termFd, pgid)
	}

	res := waitProc(proc)
	for _, f := range opened {
		f.Close()
	}

	if state.interactive && state.substDepth == 0 {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	if res.stopped {
		cmdText := cmd.ArgStrings()
		j := state.addJob(pgid, []int{pgid}, strings.Join(cmdText, " "), jobStopped)
		fmt.Fprintf(os.Stderr, "[%d]+  Stopped                 %s\n", j.id, j.cmd)
		return res.status
	}

	return res.status
}

// execExec implements the exec builtin.
// With args: replaces the shell process with the given command.
// Without args but with redirects: permanently redirects shell fds.
// Without args or redirects: no-op.
func execExec(state *shellState, cmd *parser.SimpleCmd, args []string, stdin, stdout *os.File) int {
	if len(args) == 0 {
		// exec with redirects only (or no-op).
		if len(cmd.Redirects) == 0 {
			return 0
		}
		fds := [3]*os.File{os.Stdin, os.Stdout, os.Stderr}
		fds, _, err := applyRedirects(cmd, fds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: exec: %v\n", err)
			return 1
		}
		// Permanently apply redirects via dup2.
		for i, f := range fds {
			if f.Fd() != uintptr(i) {
				if err := syscall.Dup2(int(f.Fd()), i); err != nil {
					fmt.Fprintf(os.Stderr, "gosh: exec: dup2: %v\n", err)
					return 1
				}
				switch i {
				case 0:
					os.Stdin = os.NewFile(0, f.Name())
				case 1:
					os.Stdout = os.NewFile(1, f.Name())
				case 2:
					os.Stderr = os.NewFile(2, f.Name())
				}
			}
		}
		return 0
	}

	// exec cmd args... — replace process.
	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: exec: %s: not found\n", args[0])
		return 127
	}

	// Apply redirects.
	fds := [3]*os.File{os.Stdin, os.Stdout, os.Stderr}
	fds, _, redirErr := applyRedirects(cmd, fds)
	if redirErr != nil {
		fmt.Fprintf(os.Stderr, "gosh: exec: %v\n", redirErr)
		return 1
	}
	for i, f := range fds {
		if f.Fd() != uintptr(i) {
			syscall.Dup2(int(f.Fd()), i)
		}
	}

	// Build environment.
	env := state.environ()
	for _, a := range cmd.Assigns {
		env = append(env, a.Name+"="+a.Value.String())
	}

	// Save history and fire EXIT trap.
	if state.ed != nil {
		state.ed.Close()
	}
	state.runTrap("EXIT")

	// Replace the process.
	execErr := syscall.Exec(path, args, env)
	// If we get here, exec failed.
	fmt.Fprintf(os.Stderr, "gosh: exec: %s: %v\n", args[0], execErr)
	return 126
}

// execFunction runs a user-defined function body with positional params.
func execFunction(state *shellState, body *parser.List, args []string, stdin, stdout *os.File) int {
	// Save and set positional parameters.
	savedParams := state.positionalParams
	state.positionalParams = args

	// Push a new local scope for this function call.
	state.localScopes = append(state.localScopes, make(map[string]savedVar))

	cloned := parser.CloneList(body)
	execList(state, cloned, stdin, stdout)

	// Fire RETURN trap before restoring state.
	state.runTrapWithIO("RETURN", stdin, stdout)

	// Pop and restore local variables.
	scope := state.localScopes[len(state.localScopes)-1]
	state.localScopes = state.localScopes[:len(state.localScopes)-1]
	restoreVars(state, scope)

	status := state.lastStatus
	state.returnFlag = false
	state.positionalParams = savedParams

	return status
}

// execAssignment processes a single assignment, handling scalar, array,
// and indexed assignments.
func execAssignment(state *shellState, a parser.Assignment) {
	if a.Array != nil {
		// Array assignment: arr=(a b c) or arr+=(x y)
		var vals []string
		for _, w := range a.Array {
			vals = append(vals, w.String())
		}
		if a.Append {
			state.appendArray(a.Name, vals)
		} else {
			state.setArray(a.Name, vals)
		}
		return
	}
	if a.Index != "" {
		// Indexed assignment: arr[expr]=val
		// Evaluate subscript as arithmetic.
		subscript := expander.ExpandDollar(a.Index, state.lookup)
		idx, err := expander.EvalArith(subscript, state.lookup, state.setVar)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %s: bad array subscript\n", a.Index)
			return
		}
		key := a.Name + "[" + strconv.FormatInt(idx, 10) + "]"
		state.setVar(key, a.Value.String())
		return
	}
	if a.Append {
		// String append: var+=value
		state.setVar(a.Name, state.vars[a.Name]+a.Value.String())
		return
	}
	state.setVar(a.Name, a.Value.String())
}

// savedVar records a variable's previous state for restoration.
type savedVar struct {
	value    string
	exists   bool
	isArray  bool
	arrayVal []string
}

// restoreVars undoes temporary per-command variable assignments.
func restoreVars(state *shellState, saved map[string]savedVar) {
	for name, sv := range saved {
		if sv.isArray {
			if sv.exists {
				state.arrays[name] = sv.arrayVal
			} else {
				delete(state.arrays, name)
			}
		} else {
			if sv.exists {
				state.setVar(name, sv.value)
			} else {
				delete(state.vars, name)
			}
		}
	}
}

// saveVarState saves the current state of a variable (scalar or array)
// for later restoration.
func saveVarState(state *shellState, saved map[string]savedVar, name string) {
	if _, already := saved[name]; already {
		return
	}
	if arr, isArr := state.arrays[name]; isArr {
		cp := make([]string, len(arr))
		copy(cp, arr)
		saved[name] = savedVar{exists: true, isArray: true, arrayVal: cp}
	} else {
		old, exists := state.vars[name]
		saved[name] = savedVar{value: old, exists: exists}
	}
}

// --- Process substitution ---

// processProcSubsts scans a command's args for process substitution parts
// (ProcSubstIn / ProcSubstOut). For each one it creates a FIFO (named pipe),
// spawns a goroutine to run the inner command, and replaces the arg with
// the FIFO path. Returns a cleanup function that waits for goroutines and
// removes the FIFOs.
func processProcSubsts(state *shellState, cmd *parser.SimpleCmd, stdin, stdout *os.File) func() {
	var wg sync.WaitGroup
	var fifoDir string

	for i := range cmd.Args {
		if len(cmd.Args[i]) != 1 {
			continue
		}
		part := cmd.Args[i][0]
		if part.Quote != lexer.ProcSubstIn && part.Quote != lexer.ProcSubstOut {
			continue
		}

		// Create a temp directory for FIFOs on first use.
		if fifoDir == "" {
			var err error
			fifoDir, err = os.MkdirTemp("", "gosh-procsub-*")
			if err != nil {
				fmt.Fprintf(os.Stderr, "gosh: process substitution: %v\n", err)
				continue
			}
		}

		fifoPath := filepath.Join(fifoDir, fmt.Sprintf("fifo%d", i))
		if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "gosh: process substitution: mkfifo: %v\n", err)
			continue
		}

		innerCmd := part.Text
		isInput := part.Quote == lexer.ProcSubstIn

		cmd.Args[i] = lexer.Word{lexer.WordPart{Text: fifoPath, Quote: lexer.SingleQuoted}}

		wg.Add(1)
		if isInput {
			// <(cmd): goroutine runs cmd, writes stdout to FIFO
			go func(cmdText, path string) {
				defer wg.Done()
				f, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					return
				}
				defer f.Close()
				cloned := cloneShellState(state)
				cloned.substDepth++
				tokens, err := lexer.Lex(cmdText)
				if err != nil {
					return
				}
				list, err := parser.Parse(tokens)
				if err != nil {
					return
				}
				execList(cloned, list, os.Stdin, f)
			}(innerCmd, fifoPath)
		} else {
			// >(cmd): goroutine runs cmd, reads stdin from FIFO
			go func(cmdText, path string) {
				defer wg.Done()
				f, err := os.Open(path)
				if err != nil {
					return
				}
				defer f.Close()
				cloned := cloneShellState(state)
				cloned.substDepth++
				tokens, err := lexer.Lex(cmdText)
				if err != nil {
					return
				}
				list, err := parser.Parse(tokens)
				if err != nil {
					return
				}
				execList(cloned, list, f, os.Stdout)
			}(innerCmd, fifoPath)
		}
	}

	return func() {
		wg.Wait()
		if fifoDir != "" {
			os.RemoveAll(fifoDir)
		}
	}
}

// cloneShellState creates a deep copy of shell state for use in process
// substitution goroutines. The clone is isolated: no editor, no jobs,
// non-interactive.
func cloneShellState(state *shellState) *shellState {
	s := &shellState{
		vars:             make(map[string]string, len(state.vars)),
		arrays:           make(map[string][]string, len(state.arrays)),
		exported:         make(map[string]bool, len(state.exported)),
		aliases:          make(map[string]string, len(state.aliases)),
		funcs:            make(map[string]*parser.List, len(state.funcs)),
		traps:            make(map[string]string, len(state.traps)),
		pendingSignals:   make(map[string]bool),
		sigCh:            make(chan os.Signal, 8),
		positionalParams: state.positionalParams,
		lastStatus:       state.lastStatus,
		substDepth:       state.substDepth,
		optErrexit:       state.optErrexit,
		optNounset:       state.optNounset,
		optXtrace:        state.optXtrace,
		optPipefail:      state.optPipefail,
		termFd:           state.termFd,
	}
	for k, v := range state.vars {
		s.vars[k] = v
	}
	for k, v := range state.arrays {
		cp := make([]string, len(v))
		copy(cp, v)
		s.arrays[k] = cp
	}
	for k, v := range state.exported {
		s.exported[k] = v
	}
	for k, v := range state.aliases {
		s.aliases[k] = v
	}
	for k, v := range state.funcs {
		s.funcs[k] = v
	}
	for k, v := range state.traps {
		s.traps[k] = v
	}
	return s
}

// --- Process management ---

// applyRedirects processes a command's redirections, updating the
// fd table (stdin=0, stdout=1, stderr=2). Returns the updated fds
// and a list of opened files that must be closed after use.
func applyRedirects(cmd *parser.SimpleCmd, fds [3]*os.File) ([3]*os.File, []*os.File, error) {
	var opened []*os.File

	fail := func(err error) ([3]*os.File, []*os.File, error) {
		for _, o := range opened {
			o.Close()
		}
		return fds, nil, err
	}

	for _, r := range cmd.Redirects {
		// Resolve the source fd: use explicit fd, or default
		// based on redirect type.
		fd := r.Fd
		if fd < 0 {
			switch r.Type {
			case parser.REDIR_IN:
				fd = 0
			default:
				fd = 1
			}
		}

		if fd > 2 {
			return fail(fmt.Errorf("fd %d: only 0-2 supported", fd))
		}

		switch r.Type {
		case parser.REDIR_IN:
			filename := r.File.String()
			f, err := os.Open(filename)
			if err != nil {
				return fail(fmt.Errorf("%s: %v", filename, err))
			}
			opened = append(opened, f)
			fds[fd] = f

		case parser.REDIR_OUT:
			filename := r.File.String()
			f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return fail(fmt.Errorf("%s: %v", filename, err))
			}
			opened = append(opened, f)
			fds[fd] = f

		case parser.REDIR_APPEND:
			filename := r.File.String()
			f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
			if err != nil {
				return fail(fmt.Errorf("%s: %v", filename, err))
			}
			opened = append(opened, f)
			fds[fd] = f

		case parser.REDIR_HEREDOC:
			body := r.File.String()
			pr, pw, err := os.Pipe()
			if err != nil {
				return fail(fmt.Errorf("heredoc pipe: %v", err))
			}
			go func() {
				pw.WriteString(body)
				pw.Close()
			}()
			opened = append(opened, pr)
			fds[fd] = pr

		case parser.REDIR_HERESTRING:
			body := r.File.String() + "\n"
			pr, pw, err := os.Pipe()
			if err != nil {
				return fail(fmt.Errorf("herestring pipe: %v", err))
			}
			go func() {
				pw.WriteString(body)
				pw.Close()
			}()
			opened = append(opened, pr)
			fds[fd] = pr

		case parser.REDIR_DUP:
			target, err := strconv.Atoi(r.File.String())
			if err != nil || target < 0 || target > 2 {
				return fail(fmt.Errorf("bad fd: %s", r.File.String()))
			}
			fds[fd] = fds[target]
		}
	}

	return fds, opened, nil
}

func startProcess(state *shellState, cmd *parser.SimpleCmd, fds [3]*os.File, pgid int) (*os.Process, []*os.File) {
	args := cmd.ArgStrings()
	if len(args) == 0 {
		return nil, nil
	}

	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %s: command not found\n", args[0])
		return nil, nil
	}

	fds, opened, err := applyRedirects(cmd, fds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
		return nil, nil
	}

	env := state.environ()
	for _, a := range cmd.Assigns {
		env = append(env, a.Name+"="+a.Value.String())
	}

	proc, err := os.StartProcess(path, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{fds[0], fds[1], fds[2]},
		Sys: &syscall.SysProcAttr{
			Setpgid: true,
			Pgid:    pgid,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %s: %v\n", args[0], err)
		for _, f := range opened {
			f.Close()
		}
		return nil, nil
	}
	return proc, opened
}

// waitResult holds the outcome of waiting on a process.
type waitResult struct {
	status  int  // exit status (or 128+signal)
	stopped bool // true if the process was stopped (e.g., SIGTSTP)
}

func waitProc(proc *os.Process) waitResult {
	var ws syscall.WaitStatus
	_, err := syscall.Wait4(proc.Pid, &ws, syscall.WUNTRACED, nil)
	// Release Go's internal process entry since we bypassed proc.Wait().
	proc.Release()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: wait: %v\n", err)
		return waitResult{status: 1}
	}
	if ws.Stopped() {
		return waitResult{status: 128 + int(ws.StopSignal()), stopped: true}
	}
	if ws.Signaled() {
		return waitResult{status: 128 + int(ws.Signal())}
	}
	return waitResult{status: ws.ExitStatus()}
}
