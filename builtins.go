package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"gosh/expander"
)

// builtinFunc is the signature for all builtin commands.
// stdin and stdout are the files for I/O (may be redirected).
type builtinFunc func(state *shellState, args []string, stdin, stdout, stderr *os.File) int

// builtins maps command names to their builtin implementations.
// These run in the shell process (not forked), which is required
// for cd (changes shell's cwd), exit (stops the REPL), export
// (modifies shell variables), and unset. echo/pwd/true/false are
// builtins for convenience and performance.
var builtins = map[string]builtinFunc{
	"cd":              builtinCd,
	"pwd":             builtinPwd,
	"echo":            builtinEcho,
	"exit":            builtinExit,
	"export":          builtinExport,
	"unset":           builtinUnset,
	"true":            builtinTrue,
	"false":           builtinFalse,
	"version":         builtinVersion,
	"jobs":            builtinJobs,
	"fg":              builtinFg,
	"bg":              builtinBg,
	"break":           builtinBreak,
	"continue":        builtinContinue,
	"return":          builtinReturn,
	"shift":           builtinShift,
	"test":            builtinTest,
	"[":               builtinBracket,
	"read":            builtinRead,
	"local":           builtinLocal,
	"alias":           builtinAlias,
	"unalias":         builtinUnalias,
	"printf":          builtinPrintf,
	"debug-tokens":    builtinDebugTokens,
	"debug-ast":       builtinDebugAST,
	"debug-expanded":  builtinDebugExpanded,
	"history":         builtinHistory,
	"set":             builtinSet,
	"wait":            builtinWait,
	"exec":            builtinExecStub,
	"let":             builtinLet,
	"getopts":         builtinGetopts,
	"declare":         builtinDeclare,
	"typeset":         builtinDeclare,
	"readonly":        builtinReadonly,
}

// builtinCd changes the shell's working directory.
// With no arguments, changes to $HOME.
func builtinCd(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	var dir string
	switch len(args) {
	case 0:
		dir = state.vars["HOME"]
		if dir == "" {
			fmt.Fprintln(stderr, "gosh: cd: HOME not set")
			return 1
		}
	case 1:
		if args[0] == "-" {
			dir = state.vars["OLDPWD"]
			if dir == "" {
				fmt.Fprintln(stderr, "gosh: cd: OLDPWD not set")
				return 1
			}
		} else {
			dir = args[0]
		}
	default:
		fmt.Fprintln(stderr, "gosh: cd: too many arguments")
		return 1
	}

	// Save current directory as OLDPWD before changing.
	oldwd, _ := os.Getwd()

	if err := os.Chdir(dir); err != nil {
		fmt.Fprintf(stderr, "gosh: cd: %s: %v\n", dir, err)
		return 1
	}

	state.setVar("OLDPWD", oldwd)

	// Update PWD and print new directory if cd - was used.
	if wd, err := os.Getwd(); err == nil {
		state.setVar("PWD", wd)
		if len(args) > 0 && args[0] == "-" {
			fmt.Fprintln(stdout, wd)
		}
	}
	return 0
}

// builtinPwd prints the current working directory.
func builtinPwd(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "gosh: pwd: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, wd)
	return 0
}

// builtinEcho prints its arguments separated by spaces.
// Supports -n to suppress the trailing newline.
func builtinEcho(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	suppressNewline := false
	start := 0
	if len(args) > 0 && args[0] == "-n" {
		suppressNewline = true
		start = 1
	}
	fmt.Fprint(stdout, strings.Join(args[start:], " "))
	if !suppressNewline {
		fmt.Fprintln(stdout)
	}
	return 0
}

// builtinExit sets the exit flag to stop the REPL.
// Optional argument is the exit status (default 0).
func builtinExit(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	status := 0
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(stderr, "gosh: exit: %s: numeric argument required\n", args[0])
			status = 2
		} else {
			status = n
		}
	}
	state.exitFlag = true
	state.lastStatus = status
	return status
}

// builtinBreak exits the innermost for/while loop.
func builtinBreak(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if state.loopDepth == 0 {
		fmt.Fprintln(stderr, "gosh: break: only meaningful in a loop")
		return 1
	}
	state.breakFlag = true
	return 0
}

// builtinContinue skips to the next iteration of the innermost for/while loop.
func builtinContinue(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if state.loopDepth == 0 {
		fmt.Fprintln(stderr, "gosh: continue: only meaningful in a loop")
		return 1
	}
	state.continueFlag = true
	return 0
}

// builtinReturn exits the current function with an optional status.
func builtinReturn(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	status := state.lastStatus
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(stderr, "gosh: return: %s: numeric argument required\n", args[0])
			return 2
		}
		status = n
	}
	state.returnFlag = true
	state.lastStatus = status
	return status
}

// builtinShift shifts positional parameters to the left by N (default 1).
// $1 is removed, $2 becomes $1, etc.
func builtinShift(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	n := 1
	if len(args) > 0 {
		var err error
		n, err = strconv.Atoi(args[0])
		if err != nil || n < 0 {
			fmt.Fprintf(stderr, "gosh: shift: %s: numeric argument required\n", args[0])
			return 1
		}
	}
	if n > len(state.positionalParams) {
		fmt.Fprintf(stderr, "gosh: shift: shift count out of range\n")
		return 1
	}
	state.positionalParams = state.positionalParams[n:]
	return 0
}

// builtinLocal declares function-scoped variables.
//
//	local var1 [var2=value ...]
//
// Each variable is saved in the current function's local scope and
// will be restored when the function returns. Can only be used
// inside a function. Supports "local var" (sets to empty) and
// "local var=value" (sets to value).
func builtinLocal(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(state.localScopes) == 0 {
		fmt.Fprintln(stderr, "gosh: local: can only be used in a function")
		return 1
	}

	// Check for -a / -A flag.
	declareArray := false
	declareAssoc := false
	startIdx := 0
	if len(args) > 0 && args[0] == "-a" {
		declareArray = true
		startIdx = 1
	} else if len(args) > 0 && args[0] == "-A" {
		declareAssoc = true
		startIdx = 1
	}

	for _, arg := range args[startIdx:] {
		name, value, hasValue := strings.Cut(arg, "=")
		saveLocalVar(state, name, declareArray || declareAssoc)
		if declareAssoc {
			state.attrs[name] |= attrAssoc
			if !hasValue {
				state.setAssocArray(name, make(map[string]string))
			}
		} else if declareArray {
			if !hasValue {
				state.setArray(name, nil)
			}
		} else if hasValue {
			state.setVar(name, value)
		} else {
			state.setVar(name, "")
		}
	}

	return 0
}

// saveLocalVar saves the current state of a variable in the current local
// scope. Only saves the first time a variable is declared in a scope.
func saveLocalVar(state *shellState, name string, isArray bool) {
	scope := state.localScopes[len(state.localScopes)-1]
	if _, already := scope[name]; already {
		return
	}
	if m, ok := state.assocArrays[name]; ok {
		cp := make(map[string]string, len(m))
		for k, v := range m {
			cp[k] = v
		}
		scope[name] = savedVar{exists: true, isAssoc: true, assocVal: cp, attrs: state.attrs[name]}
	} else if arr, isArr := state.arrays[name]; isArr {
		cp := make([]string, len(arr))
		copy(cp, arr)
		scope[name] = savedVar{exists: true, isArray: true, arrayVal: cp, attrs: state.attrs[name]}
	} else if isArray {
		scope[name] = savedVar{exists: false, isArray: true, attrs: state.attrs[name]}
	} else {
		old, exists := state.vars[name]
		scope[name] = savedVar{value: old, exists: exists, attrs: state.attrs[name]}
	}
}

// builtinAlias defines or lists aliases.
//
//	alias              — list all aliases
//	alias name=value   — define an alias
//	alias name         — print the alias definition
func builtinAlias(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		// List all aliases, sorted.
		names := make([]string, 0, len(state.aliases))
		for name := range state.aliases {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(stdout, "alias %s=%q\n", name, state.aliases[name])
		}
		return 0
	}

	status := 0
	for _, arg := range args {
		if name, value, ok := strings.Cut(arg, "="); ok {
			state.aliases[name] = value
		} else {
			// Print a single alias.
			if val, ok := state.aliases[arg]; ok {
				fmt.Fprintf(stdout, "alias %s=%q\n", arg, val)
			} else {
				fmt.Fprintf(stderr, "gosh: alias: %s: not found\n", arg)
				status = 1
			}
		}
	}
	return status
}

// builtinUnalias removes aliases.
//
//	unalias name ...
//	unalias -a          — remove all aliases
func builtinUnalias(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "gosh: unalias: usage: unalias [-a] name ...")
		return 1
	}

	if args[0] == "-a" {
		state.aliases = make(map[string]string)
		return 0
	}

	status := 0
	for _, name := range args {
		if _, ok := state.aliases[name]; ok {
			delete(state.aliases, name)
		} else {
			fmt.Fprintf(stderr, "gosh: unalias: %s: not found\n", name)
			status = 1
		}
	}
	return status
}

// builtinExport marks variables for export to child processes.
// Supports "export VAR" and "export VAR=VALUE".
func builtinExport(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		names := make([]string, 0, len(state.attrs))
		for k, a := range state.attrs {
			if a&attrExport != 0 {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for _, k := range names {
			fmt.Fprintf(stdout, "export %s=%q\n", k, state.vars[k])
		}
		return 0
	}
	for _, arg := range args {
		if name, value, ok := strings.Cut(arg, "="); ok {
			state.setVar(name, value)
			state.exportVar(name)
		} else {
			state.exportVar(arg)
		}
	}
	return 0
}

// builtinUnset removes variables from the shell.
// Supports unsetting array elements: unset 'arr[N]'
func builtinUnset(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	status := 0
	for _, name := range args {
		// Strip quotes that the user might have used to protect brackets.
		name = strings.Trim(name, "'\"")
		if !state.unsetVar(name) {
			status = 1
		}
	}
	return status
}

// builtinRead reads a line from stdin and splits it into variables.
//
//	read [-r] [var1 var2 ...]
//
// Reads one line from stdin. The line is split by IFS into fields
// which are assigned to the named variables. The last variable gets
// the remaining unsplit text. With no variables, the line is stored
// in REPLY. Returns 1 on EOF, 0 otherwise.
//
// -r: raw mode — backslash does not act as an escape character.
func builtinRead(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	raw := false
	readArray := false
	var prompt string
	varNames := args

	// Parse flags.
	done := false
	for len(varNames) > 0 && strings.HasPrefix(varNames[0], "-") && !done {
		switch varNames[0] {
		case "-r":
			raw = true
			varNames = varNames[1:]
		case "-a":
			readArray = true
			varNames = varNames[1:]
		case "-p":
			varNames = varNames[1:]
			if len(varNames) == 0 {
				fmt.Fprintln(stderr, "gosh: read: -p: option requires an argument")
				return 1
			}
			prompt = varNames[0]
			varNames = varNames[1:]
		default:
			done = true
		}
	}

	if prompt != "" {
		fmt.Fprint(stderr, prompt)
	}

	// Read one line from stdin, one byte at a time to avoid buffering.
	// Using bufio.NewReader would read ahead into a 4096-byte buffer,
	// consuming data that subsequent read calls in a loop would never see.
	var line string
	buf := make([]byte, 1)
	for {
		n, err := stdin.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				if !raw && strings.HasSuffix(line, "\\") {
					// Line continuation: strip trailing backslash.
					line = line[:len(line)-1]
					continue
				}
				break
			}
			line += string(buf[0])
		}
		if err != nil {
			// EOF — process whatever we got.
			if line == "" {
				return 1
			}
			break
		}
	}

	// Strip trailing newline.
	line = strings.TrimRight(line, "\n")

	// read -a: split into array.
	if readArray {
		arrayName := "REPLY"
		if len(varNames) > 0 {
			arrayName = varNames[0]
		}
		ifs, ifsSet := state.vars["IFS"]
		if !ifsSet {
			ifs = " \t\n"
		}
		// Split with no field limit.
		fields := splitByIFS(line, ifs, 1<<30)
		state.setArray(arrayName, fields)
		return 0
	}

	// No variable names: store in REPLY.
	if len(varNames) == 0 {
		state.setVar("REPLY", line)
		return 0
	}

	// Split by IFS. IFS="" means no splitting; IFS unset means default.
	ifs, ifsSet := state.vars["IFS"]
	if !ifsSet {
		ifs = " \t\n"
	}

	fields := splitByIFS(line, ifs, len(varNames))

	// Assign fields to variables.
	for i, name := range varNames {
		if i < len(fields) {
			state.setVar(name, fields[i])
		} else {
			state.setVar(name, "")
		}
	}

	return 0
}

// splitByIFS splits a line into at most maxFields fields using IFS.
// The last field gets the remainder of the line (unsplit).
// IFS whitespace characters are trimmed and collapsed; non-whitespace
// IFS characters each act as individual delimiters.
func splitByIFS(line, ifs string, maxFields int) []string {
	if maxFields <= 0 {
		return nil
	}

	isIFSWhitespace := func(r rune) bool {
		return (r == ' ' || r == '\t' || r == '\n') && strings.ContainsRune(ifs, r)
	}
	isIFS := func(r rune) bool {
		return strings.ContainsRune(ifs, r)
	}

	var fields []string
	runes := []rune(line)
	i := 0

	// Skip leading IFS whitespace.
	for i < len(runes) && isIFSWhitespace(runes[i]) {
		i++
	}

	for i < len(runes) {
		if len(fields) == maxFields-1 {
			// Last field: take the rest (but trim trailing IFS whitespace).
			rest := string(runes[i:])
			rest = strings.TrimRightFunc(rest, func(r rune) bool {
				return isIFSWhitespace(r)
			})
			fields = append(fields, rest)
			return fields
		}

		// Collect characters until next IFS delimiter.
		start := i
		for i < len(runes) && !isIFS(runes[i]) {
			i++
		}
		fields = append(fields, string(runes[start:i]))

		if i >= len(runes) {
			break
		}

		// Skip IFS delimiter(s).
		if isIFSWhitespace(runes[i]) {
			for i < len(runes) && isIFSWhitespace(runes[i]) {
				i++
			}
			// If a non-whitespace IFS char follows, skip it too.
			if i < len(runes) && isIFS(runes[i]) && !isIFSWhitespace(runes[i]) {
				i++
				for i < len(runes) && isIFSWhitespace(runes[i]) {
					i++
				}
			}
		} else {
			// Non-whitespace IFS char: skip it and any surrounding IFS whitespace.
			i++
			for i < len(runes) && isIFSWhitespace(runes[i]) {
				i++
			}
		}
	}

	return fields
}

func builtinTrue(state *shellState, args []string, stdin, stdout, stderr *os.File) int  { return 0 }
func builtinFalse(state *shellState, args []string, stdin, stdout, stderr *os.File) int { return 1 }

// builtinPrintf implements the printf builtin.
// printf FORMAT [ARGUMENTS...]
// Supports: %s (string), %d (decimal), %x (hex), %o (octal), %c (char), %% (literal %)
// Format escape sequences: \n, \t, \\, \", \', \a, \b, \f, \r, \v, \0NNN (octal), \xHH (hex)
// If more arguments than format specifiers, format is reused.
func builtinPrintf(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "gosh: printf: usage: printf format [arguments]")
		return 1
	}
	format := args[0]
	fmtArgs := args[1:]

	argIdx := 0
	getArg := func() string {
		if argIdx < len(fmtArgs) {
			s := fmtArgs[argIdx]
			argIdx++
			return s
		}
		argIdx++
		return ""
	}

	// Process format string, reusing it if there are remaining arguments.
	for {
		startArgIdx := argIdx
		s := printfExpand(format, getArg)
		fmt.Fprint(stdout, s)

		// If no arguments were consumed or all arguments are used up, stop.
		if argIdx == startArgIdx || argIdx >= len(fmtArgs) {
			break
		}
	}
	return 0
}

// printfExpand processes a printf format string, calling getArg for each
// format specifier to get the next argument.
func printfExpand(format string, getArg func() string) string {
	runes := []rune(format)
	var out strings.Builder
	i := 0

	for i < len(runes) {
		ch := runes[i]

		if ch == '\\' {
			i++
			if i >= len(runes) {
				out.WriteRune('\\')
				break
			}
			esc := runes[i]
			i++
			switch esc {
			case 'n':
				out.WriteRune('\n')
			case 't':
				out.WriteRune('\t')
			case '\\':
				out.WriteRune('\\')
			case '"':
				out.WriteRune('"')
			case '\'':
				out.WriteRune('\'')
			case 'a':
				out.WriteRune('\a')
			case 'b':
				out.WriteRune('\b')
			case 'f':
				out.WriteRune('\f')
			case 'r':
				out.WriteRune('\r')
			case 'v':
				out.WriteRune('\v')
			case '0':
				// Octal escape \0NNN (up to 3 octal digits).
				val := 0
				for j := 0; j < 3 && i < len(runes) && runes[i] >= '0' && runes[i] <= '7'; j++ {
					val = val*8 + int(runes[i]-'0')
					i++
				}
				out.WriteRune(rune(val))
			case 'x':
				// Hex escape \xHH (up to 2 hex digits).
				val := 0
				for j := 0; j < 2 && i < len(runes); j++ {
					d := hexDigit(runes[i])
					if d < 0 {
						break
					}
					val = val*16 + d
					i++
				}
				out.WriteRune(rune(val))
			default:
				out.WriteRune('\\')
				out.WriteRune(esc)
			}
			continue
		}

		if ch == '%' {
			i++
			if i >= len(runes) {
				out.WriteRune('%')
				break
			}
			spec := runes[i]
			i++

			// Collect optional flags and width between % and the specifier.
			// For simplicity, we handle basic flags: -, 0, and width digits.
			flagStart := i - 1
			for spec == '-' || spec == '0' || (spec >= '1' && spec <= '9') {
				if i >= len(runes) {
					out.WriteString(string(runes[flagStart-1:]))
					return out.String()
				}
				spec = runes[i]
				i++
			}

			switch spec {
			case '%':
				out.WriteRune('%')
			case 's':
				arg := getArg()
				out.WriteString(arg)
			case 'd':
				arg := getArg()
				n, _ := strconv.Atoi(arg)
				out.WriteString(strconv.Itoa(n))
			case 'x':
				arg := getArg()
				n, _ := strconv.Atoi(arg)
				out.WriteString(fmt.Sprintf("%x", n))
			case 'X':
				arg := getArg()
				n, _ := strconv.Atoi(arg)
				out.WriteString(fmt.Sprintf("%X", n))
			case 'o':
				arg := getArg()
				n, _ := strconv.Atoi(arg)
				out.WriteString(fmt.Sprintf("%o", n))
			case 'c':
				arg := getArg()
				if len(arg) > 0 {
					out.WriteByte(arg[0])
				}
			default:
				out.WriteRune('%')
				out.WriteRune(spec)
			}
			continue
		}

		out.WriteRune(ch)
		i++
	}

	return out.String()
}

// hexDigit returns the value of a hex digit, or -1 if not a hex digit.
func hexDigit(ch rune) int {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0')
	case ch >= 'a' && ch <= 'f':
		return int(ch-'a') + 10
	case ch >= 'A' && ch <= 'F':
		return int(ch-'A') + 10
	default:
		return -1
	}
}

// builtinDebugTokens toggles printing of the token stream before parsing.
func builtinDebugTokens(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	state.debugTokens = !state.debugTokens
	if state.debugTokens {
		fmt.Fprintln(stdout, "token debugging on")
	} else {
		fmt.Fprintln(stdout, "token debugging off")
	}
	return 0
}

// builtinDebugAST toggles printing of the AST before expansion.
func builtinDebugAST(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	state.debugAST = !state.debugAST
	if state.debugAST {
		fmt.Fprintln(stdout, "AST debugging on")
	} else {
		fmt.Fprintln(stdout, "AST debugging off")
	}
	return 0
}

// builtinDebugExpanded toggles printing of the AST after expansion.
func builtinDebugExpanded(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	state.debugExpanded = !state.debugExpanded
	if state.debugExpanded {
		fmt.Fprintln(stdout, "expanded AST debugging on")
	} else {
		fmt.Fprintln(stdout, "expanded AST debugging off")
	}
	return 0
}

// builtinHistory prints the command history.
func builtinHistory(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if state.ed == nil {
		fmt.Fprintln(stderr, "gosh: history: not available in non-interactive mode")
		return 1
	}
	entries := state.ed.History.Entries()
	for i, entry := range entries {
		fmt.Fprintf(stdout, "%5d  %s\n", i+1, entry)
	}
	return 0
}

// builtinVersion prints the shell version.
func builtinVersion(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	fmt.Fprintf(stdout, "gosh %s\n", version)
	return 0
}

// builtinExecStub exists for tab completion and type/command -v.
// The actual exec logic is in execExec, called from execSimple.
func builtinExecStub(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	return 0
}

// builtinLet evaluates arithmetic expressions.
// Returns 0 if the last expression is non-zero, 1 if zero (bash semantics).
func builtinLet(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "gosh: let: expression expected\n")
		return 1
	}
	lookup := func(name string) string { return state.lookup(name) }
	setVar := func(name, value string) { state.setVar(name, value) }
	var last int64
	for _, arg := range args {
		expr := expander.ExpandDollar(arg, lookup)
		val, err := expander.EvalArith(expr, lookup, setVar)
		if err != nil {
			fmt.Fprintf(stderr, "gosh: let: %s\n", err)
			return 1
		}
		last = val
	}
	if last != 0 {
		return 0
	}
	return 1
}

// builtinGetopts parses options from positional parameters or a custom arg list.
//
//	getopts optstring name [args...]
//
// Processes one option per invocation. OPTIND tracks position across calls.
// Leading ':' in optstring enables silent error reporting.
func builtinGetopts(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "gosh: getopts: usage: getopts optstring name [arg ...]")
		return 2
	}

	optstring := args[0]
	name := args[1]

	// Determine arg list: custom args or positional params.
	var argList []string
	if len(args) > 2 {
		argList = args[2:]
	} else {
		argList = state.positionalParams
	}

	// Detect silent mode.
	silent := len(optstring) > 0 && optstring[0] == ':'
	lookupStr := optstring
	if silent {
		lookupStr = optstring[1:]
	}

	// Read OPTIND (1-based).
	optind := 1
	if v, ok := state.vars["OPTIND"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			optind = n
		}
	}

	// Read internal char position tracker.
	optpos := 0
	if v, ok := state.vars["_OPTPOS"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			optpos = n
		}
	}

	// Detect if OPTIND was externally changed (e.g., user set OPTIND=1 to reset).
	// Compare against the last value getopts wrote.
	if lastInd, ok := state.vars["_OPTIND_LAST"]; ok {
		if strconv.Itoa(optind) != lastInd {
			optpos = 0
		}
	} else if optind == 1 {
		// First call ever: ensure optpos starts at 0.
		optpos = 0
	}

	// Helper to update OPTIND and sync the tracking variable.
	setOptind := func(n int) {
		s := strconv.Itoa(n)
		state.setVar("OPTIND", s)
		state.setVar("_OPTIND_LAST", s)
	}

	argIdx := optind - 1
	if argIdx >= len(argList) {
		state.setVar(name, "?")
		return 1
	}

	current := argList[argIdx]

	// Check if current arg is an option.
	if current == "--" {
		setOptind(optind + 1)
		state.setVar("_OPTPOS", "0")
		state.setVar(name, "?")
		return 1
	}
	if len(current) < 2 || current[0] != '-' {
		state.setVar(name, "?")
		return 1
	}

	// Determine char position within the current arg.
	charPos := optpos
	if charPos == 0 {
		charPos = 1 // skip the leading '-'
	}

	if charPos >= len(current) {
		state.setVar(name, "?")
		return 1
	}

	optChar := current[charPos]

	// advanceChar moves past the current option character, advancing
	// OPTIND when the bundled arg is fully consumed.
	advanceChar := func() {
		charPos++
		if charPos >= len(current) {
			setOptind(optind + 1)
			state.setVar("_OPTPOS", "0")
		} else {
			state.setVar("_OPTPOS", strconv.Itoa(charPos))
			// Keep _OPTIND_LAST in sync even when OPTIND doesn't change.
			state.setVar("_OPTIND_LAST", strconv.Itoa(optind))
		}
	}

	// Look up optChar in optstring.
	idx := strings.IndexByte(lookupStr, optChar)
	if idx < 0 {
		// Unknown option.
		if !silent {
			fmt.Fprintf(stderr, "gosh: getopts: illegal option -- %c\n", optChar)
		}
		state.setVar(name, "?")
		if silent {
			state.setVar("OPTARG", string(optChar))
		} else {
			state.unsetVar("OPTARG")
		}
		advanceChar()
		return 0
	}

	// Check if this option takes an argument.
	takesArg := idx+1 < len(lookupStr) && lookupStr[idx+1] == ':'

	if !takesArg {
		state.setVar(name, string(optChar))
		state.unsetVar("OPTARG")
		advanceChar()
		return 0
	}

	// Option takes an argument.
	state.setVar(name, string(optChar))
	if charPos+1 < len(current) {
		// Rest of current arg is the option argument.
		state.setVar("OPTARG", current[charPos+1:])
		setOptind(optind + 1)
		state.setVar("_OPTPOS", "0")
	} else if argIdx+1 < len(argList) {
		// Next arg is the option argument.
		state.setVar("OPTARG", argList[argIdx+1])
		setOptind(optind + 2)
		state.setVar("_OPTPOS", "0")
	} else {
		// Missing argument.
		if silent {
			state.setVar(name, ":")
			state.setVar("OPTARG", string(optChar))
		} else {
			fmt.Fprintf(stderr, "gosh: getopts: option requires an argument -- %c\n", optChar)
			state.setVar(name, "?")
			state.unsetVar("OPTARG")
		}
		setOptind(optind + 1)
		state.setVar("_OPTPOS", "0")
	}
	return 0
}

// builtinDeclare implements the declare/typeset builtin.
//
//	declare [-airx] [name[=value] ...]
//	declare [-p] [name ...]
//	declare [+ix] [name ...]
func builtinDeclare(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	// Parse flags.
	var setFlags, clearFlags uint8
	printMode := false
	declareArray := false
	declareAssoc := false
	declareGlobal := false
	i := 0
	for i < len(args) {
		arg := args[i]
		if len(arg) < 2 || (arg[0] != '-' && arg[0] != '+') {
			break
		}
		prefix := arg[0]
		for _, ch := range arg[1:] {
			switch ch {
			case 'a':
				if prefix == '+' {
					// +a not meaningful, ignore
				} else {
					declareArray = true
				}
			case 'A':
				if prefix == '+' {
					fmt.Fprintln(stderr, "gosh: declare: cannot remove associative array attribute")
					return 1
				}
				declareAssoc = true
			case 'g':
				declareGlobal = true
			case 'i':
				if prefix == '-' {
					setFlags |= attrInteger
				} else {
					clearFlags |= attrInteger
				}
			case 'r':
				if prefix == '+' {
					fmt.Fprintln(stderr, "gosh: declare: cannot remove readonly attribute")
					return 1
				}
				setFlags |= attrReadonly
			case 'x':
				if prefix == '-' {
					setFlags |= attrExport
				} else {
					clearFlags |= attrExport
				}
			case 'p':
				printMode = true
			default:
				fmt.Fprintf(stderr, "gosh: declare: -%c: invalid option\n", ch)
				return 1
			}
		}
		i++
	}
	remaining := args[i:]

	// Validate conflicting flags.
	if declareArray && declareAssoc {
		fmt.Fprintln(stderr, "gosh: declare: cannot use -a and -A simultaneously")
		return 1
	}

	// Print mode or no args: list variables.
	if printMode || (len(remaining) == 0 && setFlags == 0 && clearFlags == 0 && !declareArray && !declareAssoc) {
		return declarePrint(state, remaining, stdout)
	}

	inFunction := len(state.localScopes) > 0

	for _, arg := range remaining {
		name, value, hasValue := strings.Cut(arg, "=")

		if inFunction && !declareGlobal {
			saveLocalVar(state, name, declareArray || declareAssoc)
		}

		// Set non-readonly attributes first (so integer evaluation kicks in).
		// Readonly is deferred until after value is set.
		state.attrs[name] = (state.attrs[name] | (setFlags &^ attrReadonly)) &^ clearFlags

		if declareAssoc {
			state.attrs[name] |= attrAssoc
			if !hasValue {
				if _, exists := state.assocArrays[name]; !exists {
					state.setAssocArray(name, make(map[string]string))
				}
			}
		} else if declareArray {
			if !hasValue {
				if _, exists := state.arrays[name]; !exists {
					state.setArray(name, nil)
				}
			}
		} else if hasValue {
			state.setVar(name, value)
		} else if setFlags != 0 || clearFlags != 0 {
			// Just setting/clearing attributes on existing var.
			// If the variable doesn't exist, create it as empty.
			if _, ok := state.vars[name]; !ok {
				if _, ok := state.arrays[name]; !ok {
					if _, ok := state.assocArrays[name]; !ok {
						state.vars[name] = ""
					}
				}
			}
		}

		// Now apply readonly if requested (after value is set).
		if setFlags&attrReadonly != 0 {
			state.attrs[name] |= attrReadonly
		}
	}

	return 0
}

// declarePrint prints variable declarations.
func declarePrint(state *shellState, names []string, stdout *os.File) int {
	if len(names) == 0 {
		// Print all variables with attributes.
		allNames := make([]string, 0, len(state.vars)+len(state.arrays)+len(state.assocArrays))
		seen := make(map[string]bool)
		for k := range state.vars {
			allNames = append(allNames, k)
			seen[k] = true
		}
		for k := range state.arrays {
			if !seen[k] {
				allNames = append(allNames, k)
				seen[k] = true
			}
		}
		for k := range state.assocArrays {
			if !seen[k] {
				allNames = append(allNames, k)
			}
		}
		sort.Strings(allNames)
		for _, name := range allNames {
			printDeclare(state, name, stdout)
		}
		return 0
	}

	status := 0
	for _, name := range names {
		if _, ok := state.vars[name]; ok {
			printDeclare(state, name, stdout)
		} else if _, ok := state.arrays[name]; ok {
			printDeclare(state, name, stdout)
		} else if _, ok := state.assocArrays[name]; ok {
			printDeclare(state, name, stdout)
		} else {
			fmt.Fprintf(os.Stderr, "gosh: declare: %s: not found\n", name)
			status = 1
		}
	}
	return status
}

// printDeclare prints a single variable in declare format.
func printDeclare(state *shellState, name string, stdout *os.File) {
	a := state.attrs[name]
	var flags strings.Builder
	flags.WriteByte('-')
	hasFlags := false
	if a&attrExport != 0 {
		flags.WriteByte('x')
		hasFlags = true
	}
	if a&attrInteger != 0 {
		flags.WriteByte('i')
		hasFlags = true
	}
	if a&attrReadonly != 0 {
		flags.WriteByte('r')
		hasFlags = true
	}
	if m, ok := state.assocArrays[name]; ok {
		if hasFlags {
			flags.WriteByte('A')
		} else {
			flags.Reset()
			flags.WriteString("-A")
		}
		fmt.Fprintf(stdout, "declare %s %s=%s\n", flags.String(), name, formatAssocArrayValue(m))
		return
	}
	if arr, ok := state.arrays[name]; ok {
		if hasFlags {
			flags.WriteByte('a')
		} else {
			flags.Reset()
			flags.WriteString("-a")
		}
		fmt.Fprintf(stdout, "declare %s %s=%s\n", flags.String(), name, formatArrayValue(arr))
		return
	}
	prefix := "declare --"
	if hasFlags {
		prefix = "declare " + flags.String()
	}
	fmt.Fprintf(stdout, "%s %s=%q\n", prefix, name, state.vars[name])
}

// formatArrayValue formats an array as (elem0 elem1 ...) with quoting.
func formatArrayValue(arr []string) string {
	var sb strings.Builder
	sb.WriteByte('(')
	for i, v := range arr {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(strconv.Quote(v))
	}
	sb.WriteByte(')')
	return sb.String()
}

// formatAssocArrayValue formats an associative array as ([k1]="v1" [k2]="v2")
// with keys sorted alphabetically.
func formatAssocArrayValue(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('(')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteByte('[')
		sb.WriteString(k)
		sb.WriteString("]=")
		sb.WriteString(strconv.Quote(m[k]))
	}
	sb.WriteByte(')')
	return sb.String()
}

// builtinReadonly implements the readonly builtin.
//
//	readonly              — list all readonly variables
//	readonly name[=value] — mark variable as readonly
func builtinReadonly(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		// List all readonly variables.
		names := make([]string, 0)
		for k, a := range state.attrs {
			if a&attrReadonly != 0 {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			printDeclare(state, name, stdout)
		}
		return 0
	}

	for _, arg := range args {
		name, value, hasValue := strings.Cut(arg, "=")
		if hasValue {
			// Temporarily ensure not readonly so we can set the value.
			oldAttrs := state.attrs[name]
			state.attrs[name] &^= attrReadonly
			state.setVar(name, value)
			state.attrs[name] = oldAttrs
		}
		state.attrs[name] |= attrReadonly
	}
	return 0
}

func init() {
	// Registered in init to break the initialization cycle:
	// builtins → builtinSource → runScript → runLine → ... → builtins.
	builtins["source"] = builtinSource
	builtins["."] = builtinSource
	builtins["eval"] = builtinEval
	builtins["trap"] = builtinTrap
	builtins["command"] = builtinCommand
	builtins["type"] = builtinType
}

// builtinSource reads and executes a file in the current shell.
func builtinSource(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "gosh: source: filename argument required")
		return 1
	}
	return runScriptWithIO(state, args[0], stdin, stdout, stderr)
}

// builtinEval joins its arguments with spaces and executes the result
// as a shell command in the current shell context.
func builtinEval(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		return 0
	}
	line := strings.Join(args, " ")
	runLineWithIO(state, line, stdin, stdout, stderr)
	return state.lastStatus
}

// builtinTrap manages signal trap handlers.
//
//	trap                    — list all traps
//	trap command signal...  — set trap handler
//	trap - signal...        — remove trap handler
//	trap '' signal...       — ignore signal
func builtinTrap(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		// List all traps.
		state.trapsMu.RLock()
		names := make([]string, 0, len(state.traps))
		for name := range state.traps {
			names = append(names, name)
		}
		trapsCopy := make(map[string]string, len(state.traps))
		for k, v := range state.traps {
			trapsCopy[k] = v
		}
		state.trapsMu.RUnlock()
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(stdout, "trap -- %s %s\n", strconv.Quote(trapsCopy[name]), name)
		}
		return 0
	}

	if len(args) == 1 {
		fmt.Fprintln(stderr, "gosh: trap: usage: trap [-] command signal...")
		return 1
	}

	command := args[0]
	signals := args[1:]

	for _, spec := range signals {
		name, sig, err := parseSignalSpec(spec)
		if err != nil {
			fmt.Fprintf(stderr, "gosh: trap: %v\n", err)
			return 1
		}

		if command == "-" {
			// Remove trap.
			state.trapsMu.Lock()
			delete(state.traps, name)
			state.trapsMu.Unlock()
			// For real signals, stop notifying (reset to default).
			if sig != 0 {
				signal.Reset(sig)
				// Re-notify for signals the shell needs (INT, TSTP).
				if sig == syscall.SIGINT || sig == syscall.SIGTSTP {
					signal.Notify(state.sigCh, sig)
				}
			}
		} else {
			state.trapsMu.Lock()
			state.traps[name] = command
			state.trapsMu.Unlock()
			// For real signals, ensure we're notified.
			if sig != 0 {
				signal.Notify(state.sigCh, sig)
			}
		}
	}

	return 0
}

// builtinCommand implements the command builtin.
//
//	command name args...    — run name, skipping function lookup
//	command -v name         — print how name would be resolved
//	command -V name         — verbose: describe how name would be resolved
func builtinCommand(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		return 0
	}

	// Parse flags.
	switch args[0] {
	case "-v":
		status := 0
		for _, name := range args[1:] {
			if _, ok := builtins[name]; ok {
				fmt.Fprintln(stdout, name)
			} else if path, err := exec.LookPath(name); err == nil {
				fmt.Fprintln(stdout, path)
			} else {
				status = 1
			}
		}
		return status

	case "-V":
		status := 0
		for _, name := range args[1:] {
			if _, ok := builtins[name]; ok {
				fmt.Fprintf(stdout, "%s is a shell builtin\n", name)
			} else if path, err := exec.LookPath(name); err == nil {
				fmt.Fprintf(stdout, "%s is %s\n", name, path)
			} else {
				fmt.Fprintf(stderr, "gosh: command: %s: not found\n", name)
				status = 1
			}
		}
		return status
	}

	// command name args... — run name, skipping function lookup.
	name := args[0]
	cmdArgs := args[1:]

	// Check builtins first.
	if fn, ok := builtins[name]; ok {
		return fn(state, cmdArgs, stdin, stdout, stderr)
	}

	// External command lookup.
	path, err := exec.LookPath(name)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: command: %s: not found\n", name)
		return 127
	}

	env := state.environ()
	proc, err := os.StartProcess(path, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{stdin, stdout, stderr},
		Sys: &syscall.SysProcAttr{
			Setpgid: true,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "gosh: %s: %v\n", name, err)
		return 1
	}

	pgid := proc.Pid
	syscall.Setpgid(pgid, pgid)

	if state.interactive && state.substDepth == 0 {
		tcsetpgrp(state.termFd, pgid)
	}

	res := waitProc(proc)

	if state.interactive && state.substDepth == 0 {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	return res.status
}

// builtinType reports how each name would be interpreted as a command.
func builtinType(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		return 0
	}

	status := 0
	for _, name := range args {
		if _, ok := state.funcs[name]; ok {
			fmt.Fprintf(stdout, "%s is a function\n", name)
		} else if val, ok := state.aliases[name]; ok {
			fmt.Fprintf(stdout, "%s is aliased to '%s'\n", name, val)
		} else if _, ok := builtins[name]; ok {
			fmt.Fprintf(stdout, "%s is a shell builtin\n", name)
		} else if path, err := exec.LookPath(name); err == nil {
			fmt.Fprintf(stdout, "%s is %s\n", name, path)
		} else {
			fmt.Fprintf(stderr, "gosh: type: %s: not found\n", name)
			status = 1
		}
	}
	return status
}

// builtinSet implements the set builtin.
//
//	set -e/-o errexit       enable errexit
//	set -u/-o nounset       enable nounset
//	set -x/-o xtrace        enable xtrace
//	set -o pipefail         enable pipefail
//	set +e/+o errexit       disable errexit (etc.)
//	set -eu                 combined short flags
//	set -- arg1 arg2        set positional parameters
//	set -o                  list all options
func builtinSet(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		return 0
	}

	// set -- args... sets positional parameters.
	for i, arg := range args {
		if arg == "--" {
			state.positionalParams = args[i+1:]
			return 0
		}
	}

	i := 0
	for i < len(args) {
		arg := args[i]
		i++

		if arg == "-o" || arg == "+o" {
			enable := arg[0] == '-'
			if i >= len(args) {
				// set -o with no argument: list all options.
				printSetOptions(state, stdout)
				return 0
			}
			optName := args[i]
			i++
			if !setOption(state, optName, enable) {
				fmt.Fprintf(stderr, "gosh: set: %s: invalid option name\n", optName)
				return 1
			}
			continue
		}

		if len(arg) >= 2 && (arg[0] == '-' || arg[0] == '+') {
			enable := arg[0] == '-'
			for _, ch := range arg[1:] {
				name := shortToOption(ch)
				if name == "" {
					fmt.Fprintf(stderr, "gosh: set: -%c: invalid option\n", ch)
					return 1
				}
				setOption(state, name, enable)
			}
			continue
		}
	}

	return 0
}

func shortToOption(ch rune) string {
	switch ch {
	case 'e':
		return "errexit"
	case 'u':
		return "nounset"
	case 'x':
		return "xtrace"
	default:
		return ""
	}
}

func setOption(state *shellState, name string, enable bool) bool {
	switch name {
	case "errexit":
		state.optErrexit = enable
	case "nounset":
		state.optNounset = enable
	case "xtrace":
		state.optXtrace = enable
	case "pipefail":
		state.optPipefail = enable
	default:
		return false
	}
	return true
}

func printSetOptions(state *shellState, stdout *os.File) {
	type opt struct {
		name string
		val  bool
	}
	opts := []opt{
		{"errexit", state.optErrexit},
		{"nounset", state.optNounset},
		{"pipefail", state.optPipefail},
		{"xtrace", state.optXtrace},
	}
	for _, o := range opts {
		onOff := "off"
		if o.val {
			onOff = "on"
		}
		fmt.Fprintf(stdout, "%-15s %s\n", o.name, onOff)
	}
}

// parseJobSpec parses a job specifier like "%1" and returns the job ID.
// If args is empty, returns -1 to indicate "current job".
func parseJobSpec(args []string) (int, error) {
	if len(args) == 0 {
		return -1, nil
	}
	spec := args[0]
	if len(spec) > 0 && spec[0] == '%' {
		spec = spec[1:]
	}
	id, err := strconv.Atoi(spec)
	if err != nil {
		return 0, fmt.Errorf("invalid job spec: %s", args[0])
	}
	return id, nil
}

// builtinJobs lists all active jobs.
func builtinJobs(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	state.reapJobs()
	for _, j := range state.jobs {
		fmt.Fprintf(stdout, "[%d]+  %-24s%s\n", j.id, j.state, j.cmd)
	}
	return 0
}

// builtinFg brings a job to the foreground.
func builtinFg(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	id, err := parseJobSpec(args)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: fg: %v\n", err)
		return 1
	}

	var j *job
	if id < 0 {
		j = state.currentJob()
	} else {
		j = state.findJob(id)
	}
	if j == nil {
		if id < 0 {
			fmt.Fprintln(stderr, "gosh: fg: no current job")
		} else {
			fmt.Fprintf(stderr, "gosh: fg: %%%d: no such job\n", id)
		}
		return 1
	}

	fmt.Fprintf(stderr, "%s\n", j.cmd)

	// Give the job the terminal and send SIGCONT.
	if state.interactive {
		tcsetpgrp(state.termFd, j.pgid)
	}
	syscall.Kill(-j.pgid, syscall.SIGCONT)
	j.state = jobRunning

	// Wait for the job (may stop again).
	var lastResult waitResult
	for _, pid := range j.pids {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil)
		if err != nil {
			continue
		}
		if ws.Stopped() {
			lastResult = waitResult{status: 128 + int(ws.StopSignal()), stopped: true}
		} else if ws.Signaled() {
			lastResult = waitResult{status: 128 + int(ws.Signal())}
		} else {
			lastResult = waitResult{status: ws.ExitStatus()}
		}
	}

	if state.interactive {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	if lastResult.stopped {
		j.state = jobStopped
		fmt.Fprintf(stderr, "[%d]+  Stopped                 %s\n", j.id, j.cmd)
	} else {
		state.removeJob(j.id)
	}

	state.lastStatus = lastResult.status
	return lastResult.status
}

// builtinBg resumes a stopped job in the background.
func builtinBg(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	id, err := parseJobSpec(args)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: bg: %v\n", err)
		return 1
	}

	var j *job
	if id < 0 {
		j = state.currentJob()
	} else {
		j = state.findJob(id)
	}
	if j == nil {
		if id < 0 {
			fmt.Fprintln(stderr, "gosh: bg: no current job")
		} else {
			fmt.Fprintf(stderr, "gosh: bg: %%%d: no such job\n", id)
		}
		return 1
	}

	syscall.Kill(-j.pgid, syscall.SIGCONT)
	j.state = jobRunning
	fmt.Fprintf(stderr, "[%d]+ %s &\n", j.id, j.cmd)
	return 0
}

// builtinWait waits for background jobs or specific PIDs to complete.
//
//	wait         — wait for all background jobs
//	wait %N      — wait for job N
//	wait PID ... — wait for specific PIDs
func builtinWait(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		// Wait for all background jobs.
		lastStatus := 0
		for len(state.jobs) > 0 {
			j := state.jobs[0]
			if j.state == jobDone {
				state.removeJob(j.id)
				continue
			}
			status := waitJob(state, j)
			lastStatus = status
		}
		return lastStatus
	}

	lastStatus := 0
	for _, arg := range args {
		if len(arg) > 0 && arg[0] == '%' {
			// Job spec.
			id, err := parseJobSpec([]string{arg})
			if err != nil {
				fmt.Fprintf(stderr, "gosh: wait: %v\n", err)
				return 127
			}
			j := state.findJob(id)
			if j == nil {
				fmt.Fprintf(stderr, "gosh: wait: %%%d: no such job\n", id)
				return 127
			}
			lastStatus = waitJob(state, j)
		} else {
			// PID.
			pid, err := strconv.Atoi(arg)
			if err != nil {
				fmt.Fprintf(stderr, "gosh: wait: %s: not a pid or valid job spec\n", arg)
				return 127
			}
			// Check if this PID belongs to a known job.
			found := false
			for _, j := range state.jobs {
				for _, p := range j.pids {
					if p == pid {
						lastStatus = waitJob(state, j)
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				// Try to wait on the PID directly.
				var ws syscall.WaitStatus
				_, err := syscall.Wait4(pid, &ws, 0, nil)
				if err != nil {
					fmt.Fprintf(stderr, "gosh: wait: pid %d is not a child of this shell\n", pid)
					return 127
				}
				if ws.Signaled() {
					lastStatus = 128 + int(ws.Signal())
				} else {
					lastStatus = ws.ExitStatus()
				}
			}
		}
	}
	return lastStatus
}

// waitJob waits for all processes in a job to finish and removes the job.
// Returns the exit status of the last process.
func waitJob(state *shellState, j *job) int {
	lastStatus := 0
	if j.state != jobDone {
		for _, pid := range j.pids {
			var ws syscall.WaitStatus
			_, err := syscall.Wait4(pid, &ws, 0, nil)
			if err != nil {
				continue
			}
			if ws.Signaled() {
				lastStatus = 128 + int(ws.Signal())
			} else {
				lastStatus = ws.ExitStatus()
			}
		}
	}
	state.removeJob(j.id)
	return lastStatus
}
