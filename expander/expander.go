// Package expander performs variable, command substitution, and glob
// expansion on the AST.
//
// Expansion phases (in order):
//
//  0. Brace expansion: {a,b,c}, {1..5}, {a..e} on args only (brace.go)
//  1. Tilde expansion: ~ → $HOME, ~user → user's home dir
//  2a. Arithmetic expansion: $((expr)) (arith.go)
//  2b. Command substitution: $(cmd) and `cmd` → output of cmd
//  3. Variable expansion: $VAR, ${VAR}, $?, $$ (vars.go)
//  3.5. Word splitting: split unquoted expansion results on IFS (split.go)
//  4. Glob expansion: *, ?, [...] on unquoted args
//
// Pattern matching helpers are in pattern.go.
package expander

import (
	"gosh/lexer"
	"gosh/parser"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// LookupFunc returns the value for a variable name. It should
// return "" for undefined variables (matching bash default behavior).
type LookupFunc func(name string) string

// SubstFunc executes a command string and returns its stdout output.
// It should strip trailing newlines from the result. May be nil if
// command substitution is not needed (e.g., in tests).
type SubstFunc func(cmd string) (string, error)

// LookupArrayFunc returns the elements of an array variable for "@"
// subscripts. Returns (elements, true) when the variable is an array
// with "@" subscript (each element becomes a separate word in double
// quotes). Returns (nil, false) for "*" subscripts and non-array vars.
type LookupArrayFunc func(name string) ([]string, bool)

// IsSetFunc returns true if the named variable is set (exists) in the
// shell state. Used to avoid triggering nounset errors when parameter
// expansion operators like ${var:-default} provide fallback values.
type IsSetFunc func(name string) bool

// IsAssocFunc returns true if the named variable is an associative array.
// Used to skip arithmetic evaluation for associative array subscripts.
type IsAssocFunc func(name string) bool

// Expand walks the AST and performs all expansion phases.
// It modifies the AST in place. lookupArray, isSet, and isAssoc may be nil.
func Expand(list *parser.List, lookup LookupFunc, subst SubstFunc, setVar SetFunc, lookupArray LookupArrayFunc, isSet IsSetFunc, isAssoc ...IsAssocFunc) {
	var assocFn IsAssocFunc
	if len(isAssoc) > 0 {
		assocFn = isAssoc[0]
	}
	for i := range list.Entries {
		expandPipeline(list.Entries[i].Pipeline, lookup, subst, setVar, lookupArray, isSet, assocFn)
	}
}

// expandRedirects performs tilde, arithmetic, command substitution, and
// variable expansion on redirect filenames. No word splitting or globbing.
func expandRedirects(redirs []parser.Redirect, lookup LookupFunc, subst SubstFunc, setVar SetFunc, isSet IsSetFunc) {
	for i := range redirs {
		redirs[i].File = expandTilde(redirs[i].File, lookup)
		redirs[i].File = expandArithInWord(redirs[i].File, lookup, setVar)
		if subst != nil {
			redirs[i].File = expandCmdSubstInWord(redirs[i].File, subst)
		}
		redirs[i].File = expandVarsInWord(redirs[i].File, lookup, isSet)
	}
}

func expandPipeline(pipe *parser.Pipeline, lookup LookupFunc, subst SubstFunc, setVar SetFunc, lookupArray LookupArrayFunc, isSet IsSetFunc, isAssoc IsAssocFunc) {
	for _, cmd := range pipe.Cmds {
		switch c := cmd.(type) {
		case *parser.SimpleCmd:
			expandCommand(c, lookup, subst, setVar, lookupArray, isSet, isAssoc)
		case *parser.IfCmd:
			// IfCmd branches are expanded lazily by the executor.
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.WhileCmd:
			// WhileCmd condition and body are expanded lazily.
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.UntilCmd:
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.ForCmd:
			// ForCmd words and body are expanded lazily.
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.ArithForCmd:
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.CaseCmd:
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.FuncDef:
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.SelectCmd:
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.DblBracketCmd:
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.SubshellCmd:
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		case *parser.ArithCmd:
			expandRedirects(c.Redirects, lookup, subst, setVar, isSet)
		default:
			_ = c
		}
	}
}

func expandCommand(cmd *parser.SimpleCmd, lookup LookupFunc, subst SubstFunc, setVar SetFunc, lookupArray LookupArrayFunc, isSet IsSetFunc, isAssoc IsAssocFunc) {
	// Phase 0: brace expansion on args only.
	cmd.Args = expandBracesInArgs(cmd.Args)

	// Phase 1: tilde expansion on all words.
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandTilde(cmd.Assigns[i].Value, lookup)
		for j := range cmd.Assigns[i].Array {
			cmd.Assigns[i].Array[j] = expandTilde(cmd.Assigns[i].Array[j], lookup)
		}
	}
	for i := range cmd.Args {
		cmd.Args[i] = expandTilde(cmd.Args[i], lookup)
	}
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandTilde(cmd.Redirects[i].File, lookup)
	}

	// Phase 2a: arithmetic substitution on all words.
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandArithInWord(cmd.Assigns[i].Value, lookup, setVar)
		for j := range cmd.Assigns[i].Array {
			cmd.Assigns[i].Array[j] = expandArithInWord(cmd.Assigns[i].Array[j], lookup, setVar)
		}
	}
	for i := range cmd.Args {
		cmd.Args[i] = expandArithInWord(cmd.Args[i], lookup, setVar)
	}
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandArithInWord(cmd.Redirects[i].File, lookup, setVar)
	}

	// Phase 2b: command substitution on all words.
	if subst != nil {
		for i := range cmd.Assigns {
			cmd.Assigns[i].Value = expandCmdSubstInWord(cmd.Assigns[i].Value, subst)
			for j := range cmd.Assigns[i].Array {
				cmd.Assigns[i].Array[j] = expandCmdSubstInWord(cmd.Assigns[i].Array[j], subst)
			}
		}
		for i := range cmd.Args {
			cmd.Args[i] = expandCmdSubstInWord(cmd.Args[i], subst)
		}
		for i := range cmd.Redirects {
			cmd.Redirects[i].File = expandCmdSubstInWord(cmd.Redirects[i].File, subst)
		}
	}

	// Phase 3: variable expansion on all words.
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandVarsInWord(cmd.Assigns[i].Value, lookup, isSet, isAssoc)
		for j := range cmd.Assigns[i].Array {
			cmd.Assigns[i].Array[j] = expandVarsInWord(cmd.Assigns[i].Array[j], lookup, isSet, isAssoc)
		}
	}
	// For args, "${arr[@]}" and "$@" in double quotes may produce multiple words.
	var newArgs []lexer.Word
	for _, arg := range cmd.Args {
		newArgs = append(newArgs, expandVarsInWordMulti(arg, lookup, lookupArray, isSet, isAssoc)...)
	}
	cmd.Args = newArgs
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandVarsInWord(cmd.Redirects[i].File, lookup, isSet, isAssoc)
	}

	// Phase 3.5: word splitting on args only (not assignments or redirects).
	// Split unquoted expansion results on IFS characters.
	// Look up IFS without triggering nounset — it's an internal shell lookup.
	// IFS="" (empty) means no splitting; IFS unset means default " \t\n".
	var ifs string
	if isSet != nil && !isSet("IFS") {
		ifs = " \t\n"
	} else if isSet != nil {
		ifs = lookup("IFS") // may be "" — that's intentional (no splitting)
	} else {
		// No isSet callback (tests) — fall back to lookup with default.
		ifs = lookup("IFS")
		if ifs == "" {
			ifs = " \t\n"
		}
	}
	cmd.Args = splitFieldsInArgs(cmd.Args, ifs)

	// Phase 4: glob expansion on args only.
	cmd.Args = expandGlobsInArgs(cmd.Args)
}

// expandTilde performs tilde expansion on a word. Only an unquoted ~
// at the very start of the word is expanded:
//
//	~        → $HOME
//	~/path   → $HOME/path
//	~user    → user's home directory
//	"~"      → literal ~ (quoted, no expansion)
func expandTilde(w lexer.Word, lookup LookupFunc) lexer.Word {
	if len(w) == 0 {
		return w
	}

	first := w[0]
	if first.Quote != lexer.Unquoted || !strings.HasPrefix(first.Text, "~") {
		return w
	}

	// Extract the tilde prefix: everything up to the first / (or end).
	text := first.Text[1:] // skip the ~
	var prefix, rest string
	if idx := strings.IndexByte(text, '/'); idx >= 0 {
		prefix = text[:idx]
		rest = text[idx:] // includes the /
	} else {
		prefix = text
		rest = ""
	}

	// Resolve the home directory.
	var home string
	if prefix == "" {
		// ~ or ~/path → use $HOME
		home = lookup("HOME")
	} else {
		// ~user → look up that user's home directory
		if u, err := user.Lookup(prefix); err == nil {
			home = u.HomeDir
		} else {
			return w // unknown user, keep as-is
		}
	}

	if home == "" {
		return w
	}

	// Replace the first part with the expanded path.
	expanded := home + rest
	result := make(lexer.Word, 0, len(w))
	result = append(result, lexer.WordPart{Text: expanded, Quote: lexer.Unquoted})
	result = append(result, w[1:]...)
	return result
}

// expandArithInWord replaces ArithSubst and ArithSubstDQ parts with
// the result of evaluating the arithmetic expression.
func expandArithInWord(w lexer.Word, lookup LookupFunc, setVar SetFunc) lexer.Word {
	var result lexer.Word

	for _, part := range w {
		if part.Quote != lexer.ArithSubst && part.Quote != lexer.ArithSubstDQ {
			result = append(result, part)
			continue
		}

		val, err := EvalArith(part.Text, lookup, setVar)
		text := "0"
		if err == nil {
			text = strconv.FormatInt(val, 10)
		}

		// ArithSubst (unquoted) → result is Expanded (subject to splitting/globs).
		// ArithSubstDQ (double-quoted) → result is DoubleQuoted (no globs).
		quote := lexer.Expanded
		if part.Quote == lexer.ArithSubstDQ {
			quote = lexer.DoubleQuoted
		}
		result = append(result, lexer.WordPart{Text: text, Quote: quote})
	}

	return result
}

// expandCmdSubstInWord replaces CmdSubst and CmdSubstDQ parts with
// the output of running the command. CmdSubst results are Unquoted
// (subject to glob expansion), CmdSubstDQ results are DoubleQuoted
// (not subject to glob expansion).
func expandCmdSubstInWord(w lexer.Word, subst SubstFunc) lexer.Word {
	var result lexer.Word

	for _, part := range w {
		if part.Quote != lexer.CmdSubst && part.Quote != lexer.CmdSubstDQ {
			result = append(result, part)
			continue
		}

		output, err := subst(part.Text)
		if err != nil {
			output = ""
		}

		// CmdSubst (unquoted) → result is Expanded (subject to splitting/globs).
		// CmdSubstDQ (double-quoted) → result is DoubleQuoted (no globs).
		quote := lexer.Expanded
		if part.Quote == lexer.CmdSubstDQ {
			quote = lexer.DoubleQuoted
		}
		result = append(result, lexer.WordPart{Text: output, Quote: quote})
	}

	return result
}

// expandGlobsInArgs processes a list of arg words, expanding any
// that contain unquoted glob metacharacters into multiple words
// (one per matching file). Words without globs, or globs that
// match nothing, are kept as-is.
func expandGlobsInArgs(args []lexer.Word) []lexer.Word {
	var result []lexer.Word

	for _, w := range args {
		if !hasUnquotedGlob(w) {
			result = append(result, w)
			continue
		}

		pattern := buildGlobPattern(w)
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			// No matches or bad pattern: keep original word.
			result = append(result, w)
			continue
		}

		sort.Strings(matches)
		for _, m := range matches {
			result = append(result, lexer.Word{
				{Text: m, Quote: lexer.Unquoted},
			})
		}
	}

	return result
}

// hasUnquotedGlob returns true if the word contains any unquoted
// glob metacharacters (*, ?, [).
func hasUnquotedGlob(w lexer.Word) bool {
	for _, part := range w {
		if part.Quote != lexer.Unquoted {
			continue
		}
		if strings.ContainsAny(part.Text, "*?[") {
			return true
		}
	}
	return false
}

// buildGlobPattern constructs a filepath.Glob pattern from a word.
// Unquoted parts are used as-is (their metacharacters are live).
// Quoted parts have their metacharacters escaped with \ so they
// match literally.
func buildGlobPattern(w lexer.Word) string {
	var sb strings.Builder

	for _, part := range w {
		if part.Quote == lexer.Unquoted {
			sb.WriteString(part.Text)
		} else {
			// Escape glob metacharacters in quoted text.
			for _, ch := range part.Text {
				if ch == '*' || ch == '?' || ch == '[' || ch == ']' || ch == '\\' {
					sb.WriteRune('\\')
				}
				sb.WriteRune(ch)
			}
		}
	}

	return sb.String()
}

// ExpandWord is the exported version for use by the executor
// (e.g., to expand a single word for redirect filenames).
func ExpandWord(w lexer.Word, lookup LookupFunc) string {
	return expandVarsInWord(w, lookup, nil).String()
}

// ExpandDollar is the exported version of expandDollar for use by the
// executor (e.g., to expand $VAR references in arithmetic command expressions).
func ExpandDollar(text string, lookup LookupFunc) string {
	return expandDollar(text, lookup, nil)
}

// isNameStart returns true if ch can start a shell variable name.
func isNameStart(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

// isNameCont returns true if ch can continue a shell variable name.
func isNameCont(ch rune) bool {
	return isNameStart(ch) || (ch >= '0' && ch <= '9')
}
