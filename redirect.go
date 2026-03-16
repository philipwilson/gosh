package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"gosh/parser"
)

// applyRedirects processes a command's redirections, updating the
// fd table (stdin=0, stdout=1, stderr=2). Returns the updated fds
// and a list of opened files that must be closed after use.
func applyRedirects(redirs []parser.Redirect, fds [3]*os.File) ([3]*os.File, []*os.File, error) {
	var opened []*os.File

	fail := func(err error) ([3]*os.File, []*os.File, error) {
		for _, o := range opened {
			o.Close()
		}
		return fds, nil, err
	}

	for _, r := range redirs {
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

		case parser.REDIR_HEREDOC:
			body := r.File.String()
			pr, pw, err := os.Pipe()
			if err != nil {
				return fail(fmt.Errorf("heredoc pipe: %v", err))
			}
			go func() {
				pw.WriteString(body)
				pw.Close()
			}()
			opened = append(opened, pr)
			fds[fd] = pr

		case parser.REDIR_HERESTRING:
			body := r.File.String() + "\n"
			pr, pw, err := os.Pipe()
			if err != nil {
				return fail(fmt.Errorf("herestring pipe: %v", err))
			}
			go func() {
				pw.WriteString(body)
				pw.Close()
			}()
			opened = append(opened, pr)
			fds[fd] = pr

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
		fmt.Fprintf(fds[2], "gosh: %s: command not found\n", args[0])
		return nil, nil
	}

	fds, opened, err := applyRedirects(cmd.Redirects, fds)
	if err != nil {
		fmt.Fprintf(fds[2], "gosh: %v\n", err)
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
		fmt.Fprintf(fds[2], "gosh: %s: %v\n", args[0], err)
		for _, f := range opened {
			f.Close()
		}
		return nil, nil
	}
	return proc, opened
}

// waitResult holds the outcome of waiting on a process.
type waitResult struct {
	status  int  // exit status (or 128+signal)
	stopped bool // true if the process was stopped (e.g., SIGTSTP)
}

func waitProc(proc *os.Process) waitResult {
	var ws syscall.WaitStatus
	_, err := syscall.Wait4(proc.Pid, &ws, syscall.WUNTRACED, nil)
	// Release Go's internal process entry since we bypassed proc.Wait().
	proc.Release()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: wait: %v\n", err)
		return waitResult{status: 1}
	}
	if ws.Stopped() {
		return waitResult{status: 128 + int(ws.StopSignal()), stopped: true}
	}
	if ws.Signaled() {
		return waitResult{status: 128 + int(ws.Signal())}
	}
	return waitResult{status: ws.ExitStatus()}
}
