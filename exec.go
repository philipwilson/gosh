package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gosh/expander"
	"gosh/lexer"
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

		if entry.Op == "&" {
			execBackground(state, entry.Pipeline)
		} else {
			execPipeline(state, entry.Pipeline)
		}
		if state.exitFlag || state.breakFlag || state.continueFlag || state.returnFlag {
			return
		}
	}
}

// execBackground starts a pipeline in the background without waiting.
func execBackground(state *shellState, pipe *parser.Pipeline) {
	n := len(pipe.Cmds)

	type pipePair struct{ r, w *os.File }
	var pipes []pipePair
	if n > 1 {
		pipes = make([]pipePair, n-1)
		for i := range pipes {
			r, w, err := os.Pipe()
			if err != nil {
				fmt.Fprintf(os.Stderr, "gosh: pipe: %v\n", err)
				return
			}
			pipes[i] = pipePair{r, w}
		}
	}

	pgid := 0
	var pids []int

	for i, cmd := range pipe.Cmds {
		simple, ok := cmd.(*parser.SimpleCmd)
		if !ok {
			fmt.Fprintf(os.Stderr, "gosh: compound commands not supported in background pipelines\n")
			continue
		}

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
		proc, opened := startProcess(state, simple, fds, pgid)
		// Close files opened by redirects immediately — the child has
		// inherited them.
		for _, f := range opened {
			f.Close()
		}
		if proc == nil {
			continue
		}
		pids = append(pids, proc.Pid)

		if i == 0 {
			pgid = proc.Pid
			syscall.Setpgid(proc.Pid, proc.Pid)
		}
	}

	for _, p := range pipes {
		p.r.Close()
		p.w.Close()
	}

	if len(pids) == 0 {
		return
	}

	cmdParts := make([]string, n)
	for i, cmd := range pipe.Cmds {
		if simple, ok := cmd.(*parser.SimpleCmd); ok {
			cmdParts[i] = strings.Join(simple.ArgStrings(), " ")
		}
	}
	cmdText := strings.Join(cmdParts, " | ")

	j := state.addJob(pgid, pids, cmdText, jobRunning)
	fmt.Fprintf(os.Stderr, "[%d] %d\n", j.id, pgid)
	state.lastStatus = 0
}

func execPipeline(state *shellState, pipe *parser.Pipeline) {
	n := len(pipe.Cmds)

	if n == 1 {
		state.lastStatus = execCommand(state, pipe.Cmds[0], os.Stdin, os.Stdout)
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
		simple, ok := cmd.(*parser.SimpleCmd)
		if !ok {
			fmt.Fprintf(os.Stderr, "gosh: compound commands not supported in pipelines\n")
			continue
		}

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
		proc, opened := startProcess(state, simple, fds, pgid)
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

	var pids []int
	for _, info := range infos {
		if info.proc != nil {
			pids = append(pids, info.proc.Pid)
		}
	}

	anyStopped := false
	for i, info := range infos {
		if info.proc == nil {
			continue
		}
		res := waitProc(info.proc)
		for _, f := range info.files {
			f.Close()
		}
		if res.stopped {
			anyStopped = true
		}
		if i == n-1 {
			state.lastStatus = res.status
		}
	}

	if state.interactive {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	if anyStopped {
		cmdParts := make([]string, n)
		for i, cmd := range pipe.Cmds {
			if simple, ok := cmd.(*parser.SimpleCmd); ok {
				cmdParts[i] = strings.Join(simple.ArgStrings(), " ")
			}
		}
		cmdText := strings.Join(cmdParts, " | ")
		j := state.addJob(pgid, pids, cmdText, jobStopped)
		fmt.Fprintf(os.Stderr, "[%d]+  Stopped                 %s\n", j.id, j.cmd)
	}
}

// execPipelineSubst runs a pipeline with stdout redirected to the given
// writer. Used by command substitution to capture output.
func execPipelineSubst(state *shellState, pipe *parser.Pipeline, stdout *os.File) {
	n := len(pipe.Cmds)

	if n == 1 {
		state.lastStatus = execCommand(state, pipe.Cmds[0], os.Stdin, stdout)
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
		simple, ok := cmd.(*parser.SimpleCmd)
		if !ok {
			fmt.Fprintf(os.Stderr, "gosh: compound commands not supported in pipelines\n")
			continue
		}

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
		proc, opened := startProcess(state, simple, fds, pgid)
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
		res := waitProc(info.proc)
		for _, f := range info.files {
			f.Close()
		}
		if i == n-1 {
			state.lastStatus = res.status
		}
	}
}

// execCommand dispatches a Command (simple or compound) for execution.
func execCommand(state *shellState, cmd parser.Command, stdin, stdout *os.File) int {
	switch c := cmd.(type) {
	case *parser.SimpleCmd:
		return execSimple(state, c, stdin, stdout)
	case *parser.IfCmd:
		return execIf(state, c)
	case *parser.WhileCmd:
		return execWhile(state, c)
	case *parser.ForCmd:
		return execFor(state, c)
	case *parser.CaseCmd:
		return execCase(state, c)
	case *parser.FuncDef:
		state.funcs[c.Name] = c.Body
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gosh: unknown command type\n")
		return 1
	}
}

// execIf evaluates an if/elif/else/fi command. Each condition and
// body is expanded lazily — only the taken branch is expanded.
func execIf(state *shellState, cmd *parser.IfCmd) int {
	for _, clause := range cmd.Clauses {
		expander.Expand(clause.Condition, state.lookup, state.cmdSubst)
		execList(state, clause.Condition)

		if state.lastStatus == 0 {
			expander.Expand(clause.Body, state.lookup, state.cmdSubst)
			execList(state, clause.Body)
			return state.lastStatus
		}
	}

	if cmd.ElseBody != nil {
		expander.Expand(cmd.ElseBody, state.lookup, state.cmdSubst)
		execList(state, cmd.ElseBody)
		return state.lastStatus
	}

	// No branch taken — exit status is 0 (bash behavior).
	state.lastStatus = 0
	return 0
}

// execWhile evaluates a while/do/done loop. The condition and body
// are cloned before each expansion so that $VAR references in the
// original AST are preserved for re-expansion on each iteration.
func execWhile(state *shellState, cmd *parser.WhileCmd) int {
	state.loopDepth++
	defer func() { state.loopDepth-- }()

	for {
		cond := parser.CloneList(cmd.Condition)
		expander.Expand(cond, state.lookup, state.cmdSubst)
		execList(state, cond)

		if state.lastStatus != 0 || state.breakFlag {
			state.breakFlag = false
			break
		}

		body := parser.CloneList(cmd.Body)
		expander.Expand(body, state.lookup, state.cmdSubst)
		execList(state, body)

		if state.exitFlag || state.returnFlag || state.breakFlag {
			if state.breakFlag {
				state.breakFlag = false
			}
			return state.lastStatus
		}
		if state.continueFlag {
			state.continueFlag = false
		}
	}

	return state.lastStatus
}

// execFor evaluates a for/in/do/done loop. The word list is expanded
// once (variables + globs), then the body runs for each resulting word
// with the loop variable set.
func execFor(state *shellState, cmd *parser.ForCmd) int {
	// Expand the word list: variable expansion + glob expansion.
	// Build a temporary SimpleCmd to reuse the expander's word logic.
	expandedWords := make([]lexer.Word, len(cmd.Words))
	for i, w := range cmd.Words {
		expandedWords[i] = parser.CloneWord(w)
	}
	tmpCmd := &parser.SimpleCmd{Args: expandedWords}
	expander.Expand(&parser.List{
		Entries: []parser.ListEntry{{
			Pipeline: &parser.Pipeline{Cmds: []parser.Command{tmpCmd}},
		}},
	}, state.lookup, state.cmdSubst)

	// Collect the expanded arg strings.
	values := tmpCmd.ArgStrings()

	if len(values) == 0 {
		state.lastStatus = 0
		return 0
	}

	state.loopDepth++
	defer func() { state.loopDepth-- }()

	for _, val := range values {
		state.setVar(cmd.VarName, val)

		body := parser.CloneList(cmd.Body)
		expander.Expand(body, state.lookup, state.cmdSubst)
		execList(state, body)

		if state.exitFlag || state.returnFlag || state.breakFlag {
			if state.breakFlag {
				state.breakFlag = false
			}
			return state.lastStatus
		}
		if state.continueFlag {
			state.continueFlag = false
		}
	}

	return state.lastStatus
}

// execCase evaluates a case/in/esac command. The word is expanded once,
// then each clause's patterns are expanded and matched using filepath.Match.
// The body of the first matching clause is executed.
func execCase(state *shellState, cmd *parser.CaseCmd) int {
	// Expand the subject word.
	word := parser.CloneWord(cmd.Word)
	tmpCmd := &parser.SimpleCmd{Args: []lexer.Word{word}}
	expander.Expand(&parser.List{
		Entries: []parser.ListEntry{{
			Pipeline: &parser.Pipeline{Cmds: []parser.Command{tmpCmd}},
		}},
	}, state.lookup, state.cmdSubst)
	subject := tmpCmd.ArgStrings()[0]

	for _, clause := range cmd.Clauses {
		for _, pat := range clause.Patterns {
			// Expand variables in the pattern but NOT globs —
			// glob metacharacters are used for matching, not file expansion.
			pattern := expander.ExpandWord(parser.CloneWord(pat), state.lookup)

			matched, err := filepath.Match(pattern, subject)
			if err != nil {
				continue
			}
			if matched {
				body := parser.CloneList(clause.Body)
				expander.Expand(body, state.lookup, state.cmdSubst)
				execList(state, body)
				return state.lastStatus
			}
		}
	}

	// No match — exit status 0.
	state.lastStatus = 0
	return 0
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

	// Check for user-defined functions.
	if body, ok := state.funcs[args[0]]; ok {
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
		_ = fds // redirects applied to child via function body

		status := execFunction(state, body, args[1:])
		for _, f := range opened {
			f.Close()
		}

		restoreVars(state, saved)
		return status
	}

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

	res := waitProc(proc)
	for _, f := range opened {
		f.Close()
	}

	if state.interactive {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	if res.stopped {
		cmdText := cmd.ArgStrings()
		j := state.addJob(pgid, []int{pgid}, strings.Join(cmdText, " "), jobStopped)
		fmt.Fprintf(os.Stderr, "[%d]+  Stopped                 %s\n", j.id, j.cmd)
		return res.status
	}

	return res.status
}

// execFunction runs a user-defined function body with positional params.
func execFunction(state *shellState, body *parser.List, args []string) int {
	// Save and set positional parameters.
	savedParams := state.positionalParams
	state.positionalParams = args

	cloned := parser.CloneList(body)
	expander.Expand(cloned, state.lookup, state.cmdSubst)
	execList(state, cloned)

	status := state.lastStatus
	state.returnFlag = false
	state.positionalParams = savedParams

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
