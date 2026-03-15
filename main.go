package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"gosh/lexer"
	"gosh/parser"
)

func main() {
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

		execList(list)
	}
}

// execList walks the list and runs each pipeline sequentially.
// For now, ;, &&, and || all just run the next pipeline unconditionally.
// Proper short-circuit semantics come in M10.
func execList(list *parser.List) {
	for _, entry := range list.Entries {
		execPipeline(entry.Pipeline)
	}
}

// execPipeline runs a pipeline by wiring adjacent commands together
// with os.Pipe(). All commands in the pipeline run concurrently,
// and we wait for all of them to finish.
//
// For a pipeline "A | B | C":
//
//	A: stdin=parent  stdout=pipe1_w
//	B: stdin=pipe1_r stdout=pipe2_w
//	C: stdin=pipe2_r stdout=parent
//
// Redirections on individual commands override the pipe defaults.
// For example, "A > file | B" sends A's output to file, not the pipe.
func execPipeline(pipe *parser.Pipeline) {
	n := len(pipe.Cmds)

	if n == 1 {
		execSimple(pipe.Cmds[0], os.Stdin, os.Stdout)
		return
	}

	// Create n-1 pipes connecting adjacent commands.
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

	// Start all commands, collecting processes and any files opened
	// for redirections so we can clean them up afterwards.
	type procInfo struct {
		proc  *os.Process
		files []*os.File // redirect files to close after wait
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

		proc, opened := startProcess(cmd, stdin, stdout)
		infos[i] = procInfo{proc: proc, files: opened}
	}

	// Close all pipe fds in the parent.
	for _, p := range pipes {
		p.r.Close()
		p.w.Close()
	}

	// Wait for all children, then close redirect files.
	for i, info := range infos {
		if info.proc == nil {
			continue
		}
		state, err := info.proc.Wait()

		// Close redirect files now that the child has finished.
		for _, f := range info.files {
			f.Close()
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: wait: %v\n", err)
			continue
		}
		if i == n-1 && !state.Success() {
			if status, ok := state.Sys().(syscall.WaitStatus); ok {
				fmt.Fprintf(os.Stderr, "gosh: exit status %d\n", status.ExitStatus())
			}
		}
	}
}

// applyRedirects opens files for each redirection on the command,
// overriding stdin/stdout as appropriate. It returns the final
// stdin, stdout, and a list of opened files that the caller must
// close after the process finishes.
func applyRedirects(cmd *parser.SimpleCmd, stdin, stdout *os.File) (*os.File, *os.File, []*os.File, error) {
	var opened []*os.File

	for _, r := range cmd.Redirects {
		switch r.Type {
		case parser.REDIR_IN:
			f, err := os.Open(r.File)
			if err != nil {
				// Clean up any files we already opened.
				for _, o := range opened {
					o.Close()
				}
				return nil, nil, nil, fmt.Errorf("%s: %v", r.File, err)
			}
			opened = append(opened, f)
			stdin = f

		case parser.REDIR_OUT:
			f, err := os.OpenFile(r.File, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				for _, o := range opened {
					o.Close()
				}
				return nil, nil, nil, fmt.Errorf("%s: %v", r.File, err)
			}
			opened = append(opened, f)
			stdout = f

		case parser.REDIR_APPEND:
			f, err := os.OpenFile(r.File, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
			if err != nil {
				for _, o := range opened {
					o.Close()
				}
				return nil, nil, nil, fmt.Errorf("%s: %v", r.File, err)
			}
			opened = append(opened, f)
			stdout = f
		}
	}

	return stdin, stdout, opened, nil
}

// startProcess resolves a command, applies redirections, and starts
// the process. Returns the process and any opened redirect files
// (which the caller must close after the process exits).
func startProcess(cmd *parser.SimpleCmd, stdin, stdout *os.File) (*os.Process, []*os.File) {
	if len(cmd.Args) == 0 {
		return nil, nil
	}

	path, err := exec.LookPath(cmd.Args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %s: command not found\n", cmd.Args[0])
		return nil, nil
	}

	stdin, stdout, opened, err := applyRedirects(cmd, stdin, stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
		return nil, nil
	}

	proc, err := os.StartProcess(path, cmd.Args, &os.ProcAttr{
		Env:   os.Environ(),
		Files: []*os.File{stdin, stdout, os.Stderr},
		Sys:   &syscall.SysProcAttr{},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %s: %v\n", cmd.Args[0], err)
		for _, f := range opened {
			f.Close()
		}
		return nil, nil
	}
	return proc, opened
}

// execSimple runs a single command (not part of a multi-stage pipeline).
func execSimple(cmd *parser.SimpleCmd, stdin, stdout *os.File) {
	proc, opened := startProcess(cmd, stdin, stdout)
	if proc == nil {
		return
	}

	state, err := proc.Wait()

	for _, f := range opened {
		f.Close()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: wait: %v\n", err)
		return
	}

	if !state.Success() {
		if status, ok := state.Sys().(syscall.WaitStatus); ok {
			fmt.Fprintf(os.Stderr, "gosh: exit status %d\n", status.ExitStatus())
		}
	}
}
