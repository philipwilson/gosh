package main

import (
	"bufio"
	"fmt"
	"os"
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
	"debug-tokens":    builtinDebugTokens,
	"debug-ast":       builtinDebugAST,
	"debug-expanded":  builtinDebugExpanded,
	"history":         builtinHistory,
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

	for _, arg := range args {
		name, value, hasValue := strings.Cut(arg, "=")
		// Only save the first time a variable is declared local in
		// this scope — subsequent "local x=newval" should not
		// overwrite the saved original.
		if _, already := scope[name]; !already {
			old, exists := state.vars[name]
			scope[name] = savedVar{value: old, exists: exists}
		}
		if hasValue {
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
func builtinUnset(state *shellState, args []string, stdin, stdout *os.File) int {
	for _, name := range args {
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
	varNames := args

	// Parse -r flag.
	if len(varNames) > 0 && varNames[0] == "-r" {
		raw = true
		varNames = varNames[1:]
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

func init() {
	// Registered in init to break the initialization cycle:
	// builtins → builtinSource → runScript → runLine → ... → builtins.
	builtins["source"] = builtinSource
	builtins["."] = builtinSource
	builtins["eval"] = builtinEval
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
