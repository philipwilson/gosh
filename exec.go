package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"gosh/parser"
)

// execList runs pipelines connected by ;, &&, and ||.
//
//	;  — always run the next pipeline
//	&& — run the next pipeline only if the previous succeeded (status 0)
//	|| — run the next pipeline only if the previous failed (status != 0)
func execList(state *shellState, list *parser.List) {
	for i, entry := range list.Entries {
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

		fds := [3]*os.File{stdin, stdout, os.Stderr}
		proc, opened := startProcess(state, cmd, fds, pgid)
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

// execPipelineSubst runs a pipeline with stdout redirected to the given
// writer. Used by command substitution to capture output.
func execPipelineSubst(state *shellState, pipe *parser.Pipeline, stdout *os.File) {
	n := len(pipe.Cmds)

	if n == 1 {
		state.lastStatus = execSimple(state, pipe.Cmds[0], os.Stdin, stdout)
		return
	}

	// Multi-stage pipeline: same logic as execPipeline but the last
	// stage writes to the provided stdout instead of os.Stdout.
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
		var sin *os.File
		if i == 0 {
			sin = os.Stdin
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
		proc, opened := startProcess(state, cmd, fds, pgid)
		infos[i] = procInfo{proc: proc, files: opened}

		if i == 0 && proc != nil {
			pgid = proc.Pid
			syscall.Setpgid(proc.Pid, proc.Pid)
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
		// Per-command assignments are temporary for builtins:
		// save old values, set new ones, run, then restore.
		saved := make(map[string]savedVar, len(cmd.Assigns))
		for _, a := range cmd.Assigns {
			old, exists := state.vars[a.Name]
			saved[a.Name] = savedVar{value: old, exists: exists}
			state.setVar(a.Name, a.Value.String())
		}

		fds := [3]*os.File{stdin, stdout, os.Stderr}
		fds, opened, err := applyRedirects(cmd, fds)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			restoreVars(state, saved)
			return 1
		}
		_ = fds[0] // builtins don't currently read from stdin
		status := fn(state, args[1:], fds[1])
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

// savedVar records a variable's previous state for restoration.
type savedVar struct {
	value  string
	exists bool
}

// restoreVars undoes temporary per-command variable assignments.
func restoreVars(state *shellState, saved map[string]savedVar) {
	for name, sv := range saved {
		if sv.exists {
			state.setVar(name, sv.value)
		} else {
			delete(state.vars, name)
		}
	}
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
