package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"gosh/expander"
	"gosh/lexer"
	"gosh/parser"
)

// shellState holds the shell's mutable state: variables, export
// set, last exit status, and terminal control info.
type shellState struct {
	vars        map[string]string // shell variables
	exported    map[string]bool   // which variables are exported to children
	lastStatus  int               // $? — exit status of last command
	interactive bool              // true if stdin is a terminal
	shellPgid   int               // the shell's own process group ID
	termFd      int               // file descriptor of the controlling terminal
	exitFlag    bool              // set by exit builtin to stop the REPL
	debugTokens bool              // print tokens before parsing
	debugAST    bool              // print AST before expansion
}

func newShellState() *shellState {
	s := &shellState{
		vars:     make(map[string]string),
		exported: make(map[string]bool),
		termFd:   int(os.Stdin.Fd()),
	}

	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			s.vars[k] = v
			s.exported[k] = true
		}
	}

	s.interactive = isatty(s.termFd)

	if s.interactive {
		s.shellPgid = syscall.Getpgrp()
		signal.Ignore(syscall.SIGINT, syscall.SIGTSTP, syscall.SIGTTOU)
	}

	return s
}

func (s *shellState) lookup(name string) string {
	switch name {
	case "?":
		return strconv.Itoa(s.lastStatus)
	case "$":
		return strconv.Itoa(os.Getpid())
	default:
		return s.vars[name]
	}
}

func (s *shellState) environ() []string {
	var env []string
	for k := range s.exported {
		env = append(env, k+"="+s.vars[k])
	}
	return env
}

func (s *shellState) setVar(name, value string) {
	s.vars[name] = value
}

func (s *shellState) exportVar(name string) {
	s.exported[name] = true
}

func (s *shellState) unsetVar(name string) {
	delete(s.vars, name)
	delete(s.exported, name)
}

// --- Builtins ---

// builtinFunc is the signature for all builtin commands.
// stdout is the file to write output to (may be redirected).
type builtinFunc func(state *shellState, args []string, stdout *os.File) int

// builtins maps command names to their builtin implementations.
// These run in the shell process (not forked), which is required
// for cd (changes shell's cwd), exit (stops the REPL), export
// (modifies shell variables), and unset. echo/pwd/true/false are
// builtins for convenience and performance.
var builtins = map[string]builtinFunc{
	"cd":     builtinCd,
	"pwd":    builtinPwd,
	"echo":   builtinEcho,
	"exit":   builtinExit,
	"export": builtinExport,
	"unset":  builtinUnset,
	"true":         builtinTrue,
	"false":        builtinFalse,
	"debug-tokens": builtinDebugTokens,
	"debug-ast":    builtinDebugAST,
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
		dir = args[0]
	default:
		fmt.Fprintln(os.Stderr, "gosh: cd: too many arguments")
		return 1
	}

	if err := syscall.Chdir(dir); err != nil {
		fmt.Fprintf(os.Stderr, "gosh: cd: %s: %v\n", dir, err)
		return 1
	}

	// Update PWD to reflect the new directory.
	if wd, err := os.Getwd(); err == nil {
		state.setVar("PWD", wd)
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
		// With no args, print all exported variables (sorted would
		// be nicer, but keeping it simple).
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

// --- Main loop ---

func main() {
	state := newShellState()
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Fprintf(os.Stderr, "gosh$ ")

		if !scanner.Scan() {
			fmt.Fprintln(os.Stderr)
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		tokens, err := lexer.Lex(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			continue
		}

		if state.debugTokens {
			for _, tok := range tokens {
				fmt.Fprintf(os.Stderr, "  %s\n", tok)
			}
		}

		list, err := parser.Parse(tokens)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			continue
		}

		if state.debugAST {
			fmt.Fprintf(os.Stderr, "  %s\n", list)
		}

		expander.Expand(list, state.lookup)
		execList(state, list)

		if state.exitFlag {
			break
		}
	}

	os.Exit(state.lastStatus)
}

// --- Execution ---

// execList runs pipelines connected by ;, &&, and ||.
//
//	;  — always run the next pipeline
//	&& — run the next pipeline only if the previous succeeded (status 0)
//	|| — run the next pipeline only if the previous failed (status != 0)
func execList(state *shellState, list *parser.List) {
	for i, entry := range list.Entries {
		// Decide whether to run this pipeline based on the
		// previous operator and exit status.
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
			// ";" always falls through.
		}

		execPipeline(state, entry.Pipeline)
		if state.exitFlag {
			return
		}
	}
}

func execPipeline(state *shellState, pipe *parser.Pipeline) {
	n := len(pipe.Cmds)

	if n == 1 {
		state.lastStatus = execSimple(state, pipe.Cmds[0], os.Stdin, os.Stdout)
		return
	}

	// In a pipeline, builtins fall through to external command
	// lookup. Running a builtin in a pipeline would require forking
	// (to wire its output to a pipe), which defeats the purpose.
	// External /bin/echo, /bin/pwd etc. handle this case.

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

		proc, opened := startProcess(state, cmd, stdin, stdout, pgid)
		infos[i] = procInfo{proc: proc, files: opened}

		if i == 0 && proc != nil {
			pgid = proc.Pid
			syscall.Setpgid(proc.Pid, proc.Pid)
			if state.interactive {
				tcsetpgrp(state.termFd, pgid)
			}
		}
	}

	for _, p := range pipes {
		p.r.Close()
		p.w.Close()
	}

	for i, info := range infos {
		if info.proc == nil {
			continue
		}
		status := waitProc(info.proc)
		for _, f := range info.files {
			f.Close()
		}
		if i == n-1 {
			state.lastStatus = status
		}
	}

	if state.interactive {
		tcsetpgrp(state.termFd, state.shellPgid)
	}
}

// execSimple runs a single command (not in a pipeline).
// Builtins are handled here; in pipelines they fall through
// to external commands.
func execSimple(state *shellState, cmd *parser.SimpleCmd, stdin, stdout *os.File) int {
	// Handle assignment-only commands: just set variables.
	if len(cmd.Args) == 0 {
		for _, a := range cmd.Assigns {
			state.setVar(a.Name, a.Value.String())
		}
		return 0
	}

	args := cmd.ArgStrings()

	// Check for builtins.
	if fn, ok := builtins[args[0]]; ok {
		// Apply any per-command variable assignments.
		for _, a := range cmd.Assigns {
			state.setVar(a.Name, a.Value.String())
		}

		// Apply redirections so builtins can write to files.
		stdin, stdout, opened, err := applyRedirects(cmd, stdin, stdout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			return 1
		}
		_ = stdin // builtins don't currently read from stdin
		status := fn(state, args[1:], stdout)
		for _, f := range opened {
			f.Close()
		}
		return status
	}

	// External command.
	proc, opened := startProcess(state, cmd, stdin, stdout, 0)
	if proc == nil {
		return 127
	}

	pgid := proc.Pid
	syscall.Setpgid(pgid, pgid)

	if state.interactive {
		tcsetpgrp(state.termFd, pgid)
	}

	status := waitProc(proc)
	for _, f := range opened {
		f.Close()
	}

	if state.interactive {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	return status
}

// --- Process management ---

func applyRedirects(cmd *parser.SimpleCmd, stdin, stdout *os.File) (*os.File, *os.File, []*os.File, error) {
	var opened []*os.File

	for _, r := range cmd.Redirects {
		filename := r.File.String()

		switch r.Type {
		case parser.REDIR_IN:
			f, err := os.Open(filename)
			if err != nil {
				for _, o := range opened {
					o.Close()
				}
				return nil, nil, nil, fmt.Errorf("%s: %v", filename, err)
			}
			opened = append(opened, f)
			stdin = f

		case parser.REDIR_OUT:
			f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				for _, o := range opened {
					o.Close()
				}
				return nil, nil, nil, fmt.Errorf("%s: %v", filename, err)
			}
			opened = append(opened, f)
			stdout = f

		case parser.REDIR_APPEND:
			f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
			if err != nil {
				for _, o := range opened {
					o.Close()
				}
				return nil, nil, nil, fmt.Errorf("%s: %v", filename, err)
			}
			opened = append(opened, f)
			stdout = f
		}
	}

	return stdin, stdout, opened, nil
}

func startProcess(state *shellState, cmd *parser.SimpleCmd, stdin, stdout *os.File, pgid int) (*os.Process, []*os.File) {
	args := cmd.ArgStrings()
	if len(args) == 0 {
		return nil, nil
	}

	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %s: command not found\n", args[0])
		return nil, nil
	}

	stdin, stdout, opened, err := applyRedirects(cmd, stdin, stdout)
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
		Files: []*os.File{stdin, stdout, os.Stderr},
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

func waitProc(proc *os.Process) int {
	ps, err := proc.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: wait: %v\n", err)
		return 1
	}
	if status, ok := ps.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			return 128 + int(status.Signal())
		}
		return status.ExitStatus()
	}
	if ps.Success() {
		return 0
	}
	return 1
}

// --- Terminal control ---

func isatty(fd int) bool {
	var pgrp int
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCGPGRP),
		uintptr(unsafe.Pointer(&pgrp)),
	)
	return errno == 0
}

func tcsetpgrp(fd int, pgid int) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCSPGRP),
		uintptr(unsafe.Pointer(&pgid)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
