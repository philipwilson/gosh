package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// builtinFunc is the signature for all builtin commands.
// stdout is the file to write output to (may be redirected).
type builtinFunc func(state *shellState, args []string, stdout *os.File) int

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
	"debug-tokens":    builtinDebugTokens,
	"debug-ast":       builtinDebugAST,
	"debug-expanded":  builtinDebugExpanded,
	"history":         builtinHistory,
}

// builtinCd changes the shell's working directory.
// With no arguments, changes to $HOME.
func builtinCd(state *shellState, args []string, stdout *os.File) int {
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
func builtinPwd(state *shellState, args []string, stdout *os.File) int {
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
func builtinEcho(state *shellState, args []string, stdout *os.File) int {
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
func builtinExit(state *shellState, args []string, stdout *os.File) int {
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
func builtinBreak(state *shellState, args []string, stdout *os.File) int {
	if state.loopDepth == 0 {
		fmt.Fprintln(os.Stderr, "gosh: break: only meaningful in a loop")
		return 1
	}
	state.breakFlag = true
	return 0
}

// builtinContinue skips to the next iteration of the innermost for/while loop.
func builtinContinue(state *shellState, args []string, stdout *os.File) int {
	if state.loopDepth == 0 {
		fmt.Fprintln(os.Stderr, "gosh: continue: only meaningful in a loop")
		return 1
	}
	state.continueFlag = true
	return 0
}

// builtinExport marks variables for export to child processes.
// Supports "export VAR" and "export VAR=VALUE".
func builtinExport(state *shellState, args []string, stdout *os.File) int {
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
func builtinUnset(state *shellState, args []string, stdout *os.File) int {
	for _, name := range args {
		state.unsetVar(name)
	}
	return 0
}

func builtinTrue(state *shellState, args []string, stdout *os.File) int  { return 0 }
func builtinFalse(state *shellState, args []string, stdout *os.File) int { return 1 }

// builtinDebugTokens toggles printing of the token stream before parsing.
func builtinDebugTokens(state *shellState, args []string, stdout *os.File) int {
	state.debugTokens = !state.debugTokens
	if state.debugTokens {
		fmt.Fprintln(stdout, "token debugging on")
	} else {
		fmt.Fprintln(stdout, "token debugging off")
	}
	return 0
}

// builtinDebugAST toggles printing of the AST before expansion.
func builtinDebugAST(state *shellState, args []string, stdout *os.File) int {
	state.debugAST = !state.debugAST
	if state.debugAST {
		fmt.Fprintln(stdout, "AST debugging on")
	} else {
		fmt.Fprintln(stdout, "AST debugging off")
	}
	return 0
}

// builtinDebugExpanded toggles printing of the AST after expansion.
func builtinDebugExpanded(state *shellState, args []string, stdout *os.File) int {
	state.debugExpanded = !state.debugExpanded
	if state.debugExpanded {
		fmt.Fprintln(stdout, "expanded AST debugging on")
	} else {
		fmt.Fprintln(stdout, "expanded AST debugging off")
	}
	return 0
}

// builtinHistory prints the command history.
func builtinHistory(state *shellState, args []string, stdout *os.File) int {
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
func builtinVersion(state *shellState, args []string, stdout *os.File) int {
	fmt.Fprintf(stdout, "gosh %s\n", version)
	return 0
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
func builtinJobs(state *shellState, args []string, stdout *os.File) int {
	state.reapJobs()
	for _, j := range state.jobs {
		fmt.Fprintf(stdout, "[%d]+  %-24s%s\n", j.id, j.state, j.cmd)
	}
	return 0
}

// builtinFg brings a job to the foreground.
func builtinFg(state *shellState, args []string, stdout *os.File) int {
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
func builtinBg(state *shellState, args []string, stdout *os.File) int {
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
