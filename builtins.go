package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// builtinFunc is the signature for all builtin commands.
// stdin and stdout are the files for I/O (may be redirected).
type builtinFunc func(state *shellState, args []string, stdin, stdout *os.File) int

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
}

// builtinCd changes the shell's working directory.
// With no arguments, changes to $HOME.
func builtinCd(state *shellState, args []string, stdin, stdout *os.File) int {
	var dir string
	switch len(args) {
	case 0:
		dir = state.vars["HOME"]
		if dir == "" {
			fmt.Fprintln(os.Stderr, "gosh: cd: HOME not set")
			return 1
		}
	case 1:
		if args[0] == "-" {
			dir = state.vars["OLDPWD"]
			if dir == "" {
				fmt.Fprintln(os.Stderr, "gosh: cd: OLDPWD not set")
				return 1
			}
		} else {
			dir = args[0]
		}
	default:
		fmt.Fprintln(os.Stderr, "gosh: cd: too many arguments")
		return 1
	}

	// Save current directory as OLDPWD before changing.
	oldwd, _ := os.Getwd()

	if err := os.Chdir(dir); err != nil {
		fmt.Fprintf(os.Stderr, "gosh: cd: %s: %v\n", dir, err)
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
func builtinPwd(state *shellState, args []string, stdin, stdout *os.File) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: pwd: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, wd)
	return 0
}

// builtinEcho prints its arguments separated by spaces.
// Supports -n to suppress the trailing newline.
func builtinEcho(state *shellState, args []string, stdin, stdout *os.File) int {
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
func builtinExit(state *shellState, args []string, stdin, stdout *os.File) int {
	status := 0
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: exit: %s: numeric argument required\n", args[0])
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
func builtinBreak(state *shellState, args []string, stdin, stdout *os.File) int {
	if state.loopDepth == 0 {
		fmt.Fprintln(os.Stderr, "gosh: break: only meaningful in a loop")
		return 1
	}
	state.breakFlag = true
	return 0
}

// builtinContinue skips to the next iteration of the innermost for/while loop.
func builtinContinue(state *shellState, args []string, stdin, stdout *os.File) int {
	if state.loopDepth == 0 {
		fmt.Fprintln(os.Stderr, "gosh: continue: only meaningful in a loop")
		return 1
	}
	state.continueFlag = true
	return 0
}

// builtinReturn exits the current function with an optional status.
func builtinReturn(state *shellState, args []string, stdin, stdout *os.File) int {
	status := state.lastStatus
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: return: %s: numeric argument required\n", args[0])
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
func builtinShift(state *shellState, args []string, stdin, stdout *os.File) int {
	n := 1
	if len(args) > 0 {
		var err error
		n, err = strconv.Atoi(args[0])
		if err != nil || n < 0 {
			fmt.Fprintf(os.Stderr, "gosh: shift: %s: numeric argument required\n", args[0])
			return 1
		}
	}
	if n > len(state.positionalParams) {
		fmt.Fprintf(os.Stderr, "gosh: shift: shift count out of range\n")
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
func builtinLocal(state *shellState, args []string, stdin, stdout *os.File) int {
	if len(state.localScopes) == 0 {
		fmt.Fprintln(os.Stderr, "gosh: local: can only be used in a function")
		return 1
	}

	scope := state.localScopes[len(state.localScopes)-1]

	// Check for -a flag (declare local array).
	declareArray := false
	startIdx := 0
	if len(args) > 0 && args[0] == "-a" {
		declareArray = true
		startIdx = 1
	}

	for _, arg := range args[startIdx:] {
		name, value, hasValue := strings.Cut(arg, "=")
		// Only save the first time a variable is declared local in
		// this scope — subsequent "local x=newval" should not
		// overwrite the saved original.
		if _, already := scope[name]; !already {
			if arr, isArr := state.arrays[name]; isArr {
				cp := make([]string, len(arr))
				copy(cp, arr)
				scope[name] = savedVar{exists: true, isArray: true, arrayVal: cp}
			} else if declareArray {
				// Declaring a new local array — save that it didn't exist.
				scope[name] = savedVar{exists: false, isArray: true}
			} else {
				old, exists := state.vars[name]
				scope[name] = savedVar{value: old, exists: exists}
			}
		}
		if declareArray {
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

// builtinAlias defines or lists aliases.
//
//	alias              — list all aliases
//	alias name=value   — define an alias
//	alias name         — print the alias definition
func builtinAlias(state *shellState, args []string, stdin, stdout *os.File) int {
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
				fmt.Fprintf(os.Stderr, "gosh: alias: %s: not found\n", arg)
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
func builtinUnalias(state *shellState, args []string, stdin, stdout *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gosh: unalias: usage: unalias [-a] name ...")
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
			fmt.Fprintf(os.Stderr, "gosh: unalias: %s: not found\n", name)
			status = 1
		}
	}
	return status
}

// builtinExport marks variables for export to child processes.
// Supports "export VAR" and "export VAR=VALUE".
func builtinExport(state *shellState, args []string, stdin, stdout *os.File) int {
	if len(args) == 0 {
		for k := range state.exported {
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
func builtinUnset(state *shellState, args []string, stdin, stdout *os.File) int {
	for _, name := range args {
		// Strip quotes that the user might have used to protect brackets.
		name = strings.Trim(name, "'\"")
		state.unsetVar(name)
	}
	return 0
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
func builtinRead(state *shellState, args []string, stdin, stdout *os.File) int {
	raw := false
	readArray := false
	varNames := args

	// Parse flags.
	for len(varNames) > 0 && strings.HasPrefix(varNames[0], "-") {
		switch varNames[0] {
		case "-r":
			raw = true
			varNames = varNames[1:]
		case "-a":
			readArray = true
			varNames = varNames[1:]
		default:
			break
		}
	}

	// Read one line from stdin.
	reader := bufio.NewReader(stdin)
	var line string
	for {
		segment, err := reader.ReadString('\n')
		if !raw && strings.HasSuffix(strings.TrimRight(segment, "\n"), "\\") {
			// Line continuation: strip trailing backslash-newline.
			segment = strings.TrimRight(segment, "\n")
			segment = segment[:len(segment)-1]
			line += segment
			continue
		}
		line += segment
		if err != nil {
			// EOF — process whatever we got.
			if line == "" {
				return 1
			}
			break
		}
		break
	}

	// Strip trailing newline.
	line = strings.TrimRight(line, "\n")

	// read -a: split into array.
	if readArray {
		arrayName := "REPLY"
		if len(varNames) > 0 {
			arrayName = varNames[0]
		}
		ifs := state.vars["IFS"]
		if ifs == "" {
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

	// Split by IFS.
	ifs := state.vars["IFS"]
	if ifs == "" {
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

func builtinTrue(state *shellState, args []string, stdin, stdout *os.File) int  { return 0 }
func builtinFalse(state *shellState, args []string, stdin, stdout *os.File) int { return 1 }

// builtinPrintf implements the printf builtin.
// printf FORMAT [ARGUMENTS...]
// Supports: %s (string), %d (decimal), %x (hex), %o (octal), %c (char), %% (literal %)
// Format escape sequences: \n, \t, \\, \", \', \a, \b, \f, \r, \v, \0NNN (octal), \xHH (hex)
// If more arguments than format specifiers, format is reused.
func builtinPrintf(state *shellState, args []string, stdin, stdout *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gosh: printf: usage: printf format [arguments]")
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
func builtinDebugTokens(state *shellState, args []string, stdin, stdout *os.File) int {
	state.debugTokens = !state.debugTokens
	if state.debugTokens {
		fmt.Fprintln(stdout, "token debugging on")
	} else {
		fmt.Fprintln(stdout, "token debugging off")
	}
	return 0
}

// builtinDebugAST toggles printing of the AST before expansion.
func builtinDebugAST(state *shellState, args []string, stdin, stdout *os.File) int {
	state.debugAST = !state.debugAST
	if state.debugAST {
		fmt.Fprintln(stdout, "AST debugging on")
	} else {
		fmt.Fprintln(stdout, "AST debugging off")
	}
	return 0
}

// builtinDebugExpanded toggles printing of the AST after expansion.
func builtinDebugExpanded(state *shellState, args []string, stdin, stdout *os.File) int {
	state.debugExpanded = !state.debugExpanded
	if state.debugExpanded {
		fmt.Fprintln(stdout, "expanded AST debugging on")
	} else {
		fmt.Fprintln(stdout, "expanded AST debugging off")
	}
	return 0
}

// builtinHistory prints the command history.
func builtinHistory(state *shellState, args []string, stdin, stdout *os.File) int {
	if state.ed == nil {
		fmt.Fprintln(os.Stderr, "gosh: history: not available in non-interactive mode")
		return 1
	}
	entries := state.ed.History.Entries()
	for i, entry := range entries {
		fmt.Fprintf(stdout, "%5d  %s\n", i+1, entry)
	}
	return 0
}

// builtinVersion prints the shell version.
func builtinVersion(state *shellState, args []string, stdin, stdout *os.File) int {
	fmt.Fprintf(stdout, "gosh %s\n", version)
	return 0
}

// builtinExecStub exists for tab completion and type/command -v.
// The actual exec logic is in execExec, called from execSimple.
func builtinExecStub(state *shellState, args []string, stdin, stdout *os.File) int {
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
func builtinSource(state *shellState, args []string, stdin, stdout *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gosh: source: filename argument required")
		return 1
	}
	return runScript(state, args[0])
}

// builtinEval joins its arguments with spaces and executes the result
// as a shell command in the current shell context.
func builtinEval(state *shellState, args []string, stdin, stdout *os.File) int {
	if len(args) == 0 {
		return 0
	}
	line := strings.Join(args, " ")
	runLine(state, line)
	return state.lastStatus
}

// builtinTrap manages signal trap handlers.
//
//	trap                    — list all traps
//	trap command signal...  — set trap handler
//	trap - signal...        — remove trap handler
//	trap '' signal...       — ignore signal
func builtinTrap(state *shellState, args []string, stdin, stdout *os.File) int {
	if len(args) == 0 {
		// List all traps.
		names := make([]string, 0, len(state.traps))
		for name := range state.traps {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(stdout, "trap -- %s %s\n", strconv.Quote(state.traps[name]), name)
		}
		return 0
	}

	if len(args) == 1 {
		fmt.Fprintln(os.Stderr, "gosh: trap: usage: trap [-] command signal...")
		return 1
	}

	command := args[0]
	signals := args[1:]

	for _, spec := range signals {
		name, sig, err := parseSignalSpec(spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: trap: %v\n", err)
			return 1
		}

		if command == "-" {
			// Remove trap.
			delete(state.traps, name)
			// For real signals, stop notifying (reset to default).
			if sig != 0 {
				signal.Reset(sig)
				// Re-notify for signals the shell needs (INT, TSTP).
				if sig == syscall.SIGINT || sig == syscall.SIGTSTP {
					signal.Notify(state.sigCh, sig)
				}
			}
		} else {
			state.traps[name] = command
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
func builtinCommand(state *shellState, args []string, stdin, stdout *os.File) int {
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
				fmt.Fprintf(os.Stderr, "gosh: command: %s: not found\n", name)
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
		return fn(state, cmdArgs, stdin, stdout)
	}

	// External command lookup.
	path, err := exec.LookPath(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: command: %s: not found\n", name)
		return 127
	}

	env := state.environ()
	proc, err := os.StartProcess(path, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{stdin, stdout, os.Stderr},
		Sys: &syscall.SysProcAttr{
			Setpgid: true,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %s: %v\n", name, err)
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
func builtinType(state *shellState, args []string, stdin, stdout *os.File) int {
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
			fmt.Fprintf(os.Stderr, "gosh: type: %s: not found\n", name)
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
func builtinSet(state *shellState, args []string, stdin, stdout *os.File) int {
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
				fmt.Fprintf(os.Stderr, "gosh: set: %s: invalid option name\n", optName)
				return 1
			}
			continue
		}

		if len(arg) >= 2 && (arg[0] == '-' || arg[0] == '+') {
			enable := arg[0] == '-'
			for _, ch := range arg[1:] {
				name := shortToOption(ch)
				if name == "" {
					fmt.Fprintf(os.Stderr, "gosh: set: -%c: invalid option\n", ch)
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
func builtinJobs(state *shellState, args []string, stdin, stdout *os.File) int {
	state.reapJobs()
	for _, j := range state.jobs {
		fmt.Fprintf(stdout, "[%d]+  %-24s%s\n", j.id, j.state, j.cmd)
	}
	return 0
}

// builtinFg brings a job to the foreground.
func builtinFg(state *shellState, args []string, stdin, stdout *os.File) int {
	id, err := parseJobSpec(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: fg: %v\n", err)
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
			fmt.Fprintln(os.Stderr, "gosh: fg: no current job")
		} else {
			fmt.Fprintf(os.Stderr, "gosh: fg: %%%d: no such job\n", id)
		}
		return 1
	}

	fmt.Fprintf(os.Stderr, "%s\n", j.cmd)

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
		fmt.Fprintf(os.Stderr, "[%d]+  Stopped                 %s\n", j.id, j.cmd)
	} else {
		state.removeJob(j.id)
	}

	state.lastStatus = lastResult.status
	return lastResult.status
}

// builtinBg resumes a stopped job in the background.
func builtinBg(state *shellState, args []string, stdin, stdout *os.File) int {
	id, err := parseJobSpec(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: bg: %v\n", err)
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
			fmt.Fprintln(os.Stderr, "gosh: bg: no current job")
		} else {
			fmt.Fprintf(os.Stderr, "gosh: bg: %%%d: no such job\n", id)
		}
		return 1
	}

	syscall.Kill(-j.pgid, syscall.SIGCONT)
	j.state = jobRunning
	fmt.Fprintf(os.Stderr, "[%d]+ %s &\n", j.id, j.cmd)
	return 0
}

// builtinWait waits for background jobs or specific PIDs to complete.
//
//	wait         — wait for all background jobs
//	wait %N      — wait for job N
//	wait PID ... — wait for specific PIDs
func builtinWait(state *shellState, args []string, stdin, stdout *os.File) int {
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
				fmt.Fprintf(os.Stderr, "gosh: wait: %v\n", err)
				return 127
			}
			j := state.findJob(id)
			if j == nil {
				fmt.Fprintf(os.Stderr, "gosh: wait: %%%d: no such job\n", id)
				return 127
			}
			lastStatus = waitJob(state, j)
		} else {
			// PID.
			pid, err := strconv.Atoi(arg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "gosh: wait: %s: not a pid or valid job spec\n", arg)
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
					fmt.Fprintf(os.Stderr, "gosh: wait: pid %d is not a child of this shell\n", pid)
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
