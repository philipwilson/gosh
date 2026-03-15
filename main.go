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
}

func newShellState() *shellState {
	s := &shellState{
		vars:     make(map[string]string),
		exported: make(map[string]bool),
		termFd:   int(os.Stdin.Fd()),
	}

	// Import all environment variables.
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			s.vars[k] = v
			s.exported[k] = true
		}
	}

	// Check if we're running interactively (stdin is a terminal).
	s.interactive = isatty(s.termFd)

	if s.interactive {
		s.shellPgid = syscall.Getpgrp()

		// Ignore job-control signals so they go to the foreground
		// process group, not the shell.
		//
		// SIGINT  (Ctrl-C)  — should kill foreground job, not shell
		// SIGTSTP (Ctrl-Z)  — should stop foreground job, not shell
		// SIGTTOU           — shell may write to terminal while in
		//                     background; ignore to prevent stopping
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

		if line == "exit" {
			break
		}

		tokens, err := lexer.Lex(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			continue
		}

		list, err := parser.Parse(tokens)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			continue
		}

		expander.Expand(list, state.lookup)
		execList(state, list)
	}
}

func execList(state *shellState, list *parser.List) {
	for _, entry := range list.Entries {
		execPipeline(state, entry.Pipeline)
	}
}

// execPipeline runs a pipeline, placing all its processes in a
// single process group. If interactive, the pipeline's process
// group is given the terminal (foreground) so that signals like
// SIGINT are delivered to it, not to the shell.
func execPipeline(state *shellState, pipe *parser.Pipeline) {
	n := len(pipe.Cmds)

	if n == 1 {
		state.lastStatus = execSimple(state, pipe.Cmds[0], os.Stdin, os.Stdout)
		return
	}

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

	// pgid for this pipeline: set to 0 for the first process
	// (creates a new group), then set to the first process's PID
	// for subsequent processes (they join the group).
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
			// Also call setpgid from the parent to avoid a race
			// between the parent and child.
			syscall.Setpgid(proc.Pid, proc.Pid)

			// Give the pipeline's process group the terminal.
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

	// Take back the terminal for the shell.
	if state.interactive {
		tcsetpgrp(state.termFd, state.shellPgid)
	}
}

// execSimple runs a single command. Returns exit status.
func execSimple(state *shellState, cmd *parser.SimpleCmd, stdin, stdout *os.File) int {
	// Handle assignment-only commands (no args): just set variables.
	if len(cmd.Args) == 0 {
		for _, a := range cmd.Assigns {
			state.setVar(a.Name, a.Value.String())
		}
		return 0
	}

	// Handle "export" builtin.
	args := cmd.ArgStrings()
	if args[0] == "export" {
		return builtinExport(state, args[1:])
	}

	proc, opened := startProcess(state, cmd, stdin, stdout, 0)
	if proc == nil {
		return 127
	}

	pgid := proc.Pid
	syscall.Setpgid(pgid, pgid)

	// Give the child's process group the terminal.
	if state.interactive {
		tcsetpgrp(state.termFd, pgid)
	}

	status := waitProc(proc)
	for _, f := range opened {
		f.Close()
	}

	// Take back the terminal.
	if state.interactive {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	return status
}

func builtinExport(state *shellState, args []string) int {
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

// startProcess resolves a command, applies redirections, and starts
// the process in the given process group. pgid=0 means create a new
// process group; pgid>0 means join that existing group.
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

// waitProc waits for a process and returns its exit status.
// If the process was killed by a signal, the exit status is
// 128 + signal number (bash convention).
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

// isatty returns true if the file descriptor refers to a terminal.
// We probe with TIOCGPGRP (get terminal foreground process group):
// if the ioctl succeeds, the fd is a terminal. This works on both
// Darwin and Linux, unlike TIOCGETA which is Darwin-only.
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

// tcsetpgrp sets the foreground process group of the terminal.
// This controls which process group receives keyboard signals
// (SIGINT from Ctrl-C, SIGTSTP from Ctrl-Z).
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
