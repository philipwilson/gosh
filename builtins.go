package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
