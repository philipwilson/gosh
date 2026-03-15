package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"gosh/expander"
	"gosh/lexer"
	"gosh/parser"
)

// shellState holds the shell's mutable state: variables, export
// set, and the last command's exit status.
type shellState struct {
	vars       map[string]string // shell variables
	exported   map[string]bool   // which variables are exported to children
	lastStatus int               // $? — exit status of last command
}

func newShellState() *shellState {
	s := &shellState{
		vars:     make(map[string]string),
		exported: make(map[string]bool),
	}
	// Import all environment variables into the shell's variable
	// table and mark them as exported.
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			s.vars[k] = v
			s.exported[k] = true
		}
	}
	return s
}

// lookup returns the value of a variable, including special
// variables $? and $$. Returns "" for undefined variables.
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

// environ builds the environment for child processes: all
// exported variables as KEY=VALUE strings.
func (s *shellState) environ() []string {
	var env []string
	for k := range s.exported {
		env = append(env, k+"="+s.vars[k])
	}
	return env
}

// setVar sets a shell variable.
func (s *shellState) setVar(name, value string) {
	s.vars[name] = value
}

// exportVar marks a variable for export to child processes.
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

		// Expand variables in the AST.
		expander.Expand(list, state.lookup)

		execList(state, list)
	}
}

func execList(state *shellState, list *parser.List) {
	for _, entry := range list.Entries {
		execPipeline(state, entry.Pipeline)
	}
}

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

		proc, opened := startProcess(state, cmd, stdin, stdout)
		infos[i] = procInfo{proc: proc, files: opened}
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

	proc, opened := startProcess(state, cmd, stdin, stdout)
	if proc == nil {
		return 127 // command not found
	}

	status := waitProc(proc)
	for _, f := range opened {
		f.Close()
	}
	return status
}

// builtinExport handles "export VAR" and "export VAR=VALUE".
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

func startProcess(state *shellState, cmd *parser.SimpleCmd, stdin, stdout *os.File) (*os.Process, []*os.File) {
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

	// Build environment: start with exported vars, then add
	// any per-command assignments.
	env := state.environ()
	for _, a := range cmd.Assigns {
		env = append(env, a.Name+"="+a.Value.String())
	}

	proc, err := os.StartProcess(path, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{stdin, stdout, os.Stderr},
		Sys:   &syscall.SysProcAttr{},
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
func waitProc(proc *os.Process) int {
	ps, err := proc.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: wait: %v\n", err)
		return 1
	}
	if status, ok := ps.Sys().(syscall.WaitStatus); ok {
		return status.ExitStatus()
	}
	if ps.Success() {
		return 0
	}
	return 1
}
