package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"gosh/expander"
	"gosh/parser"
)

// execList runs pipelines connected by ;, &&, and ||.
// Each entry is expanded just before execution (lazy expansion) so
// that assignments in earlier entries are visible to later ones.
//
//	;  — always run the next pipeline
//	&& — run the next pipeline only if the previous succeeded (status 0)
//	|| — run the next pipeline only if the previous failed (status != 0)
func execList(state *shellState, list *parser.List, stdin, stdout, stderr *os.File) {
	for i := range list.Entries {
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

		// Suppress errexit for LHS of && and ||.
		suppressErrexit := list.Entries[i].Op == "&&" || list.Entries[i].Op == "||"
		if suppressErrexit {
			state.noErrexit++
		}

		// Expand this entry just before execution.
		singleList := &parser.List{Entries: []parser.ListEntry{list.Entries[i]}}
		expander.Expand(singleList, state.lookupNounset, state.cmdSubst, state.setVar, state.lookupArray, state.isVarSet)
		list.Entries[i] = singleList.Entries[0]

		// Check for nounset error during expansion.
		if state.nounsetError {
			state.nounsetError = false
			state.lastStatus = 1
			state.runTrapWithIO("ERR", stdin, stdout, stderr)
			if suppressErrexit {
				state.noErrexit--
			}
			if state.optErrexit && state.noErrexit == 0 {
				state.exitFlag = true
				return
			}
			continue
		}

		if state.debugExpanded {
			fmt.Fprintf(stderr, "  %s\n", list.Entries[i].Pipeline)
		}

		if list.Entries[i].Op == "&" {
			execBackground(state, list.Entries[i].Pipeline, stderr)
		} else {
			execPipeline(state, list.Entries[i].Pipeline, stdin, stdout, stderr)
		}

		// Run pending signal traps and ERR trap.
		state.runPendingTrapsWithIO(stdin, stdout, stderr)
		if state.lastStatus != 0 {
			state.runTrapWithIO("ERR", stdin, stdout, stderr)
		}

		if suppressErrexit {
			state.noErrexit--
		}

		// Errexit: exit if command failed and not suppressed.
		// Don't fire for the LHS of && or || (suppressErrexit was true).
		if !suppressErrexit && state.optErrexit && state.noErrexit == 0 && state.lastStatus != 0 {
			state.exitFlag = true
			return
		}

		if state.exitFlag || state.breakFlag || state.continueFlag || state.returnFlag {
			return
		}
	}
}

// execBackground starts a pipeline in the background without waiting.
func execBackground(state *shellState, pipe *parser.Pipeline, stderr *os.File) {
	n := len(pipe.Cmds)

	type pipePair struct{ r, w *os.File }
	var pipes []pipePair
	if n > 1 {
		pipes = make([]pipePair, n-1)
		for i := range pipes {
			r, w, err := os.Pipe()
			if err != nil {
				fmt.Fprintf(stderr, "gosh: pipe: %v\n", err)
				return
			}
			pipes[i] = pipePair{r, w}
		}
	}

	pgid := 0
	var pids []int
	goroutineOwned := make(map[*os.File]bool)

	for i, cmd := range pipe.Cmds {
		var sin *os.File
		if i == 0 {
			sin = os.Stdin
		} else {
			sin = pipes[i-1].r
		}

		var sout *os.File
		if i == n-1 {
			sout = os.Stdout
		} else {
			sout = pipes[i].w
		}

		if simple, ok := cmd.(*parser.SimpleCmd); ok {
			fds := [3]*os.File{sin, sout, stderr}
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
		} else {
			// Compound command: run in goroutine with cloned state.
			clone := cloneShellState(state)
			body := cmd
			cmdIn, cmdOut := sin, sout
			if i > 0 {
				goroutineOwned[cmdIn] = true
			}
			if i < n-1 {
				goroutineOwned[cmdOut] = true
			}
			go func() {
				execCommand(clone, body, cmdIn, cmdOut, stderr)
				if i > 0 {
					cmdIn.Close()
				}
				if i < n-1 {
					cmdOut.Close()
				}
			}()
		}
	}

	// Close parent-owned pipe ends.
	for _, p := range pipes {
		if !goroutineOwned[p.r] {
			p.r.Close()
		}
		if !goroutineOwned[p.w] {
			p.w.Close()
		}
	}

	if len(pids) == 0 {
		return
	}

	cmdParts := make([]string, n)
	for i, cmd := range pipe.Cmds {
		if simple, ok := cmd.(*parser.SimpleCmd); ok {
			cmdParts[i] = strings.Join(simple.ArgStrings(), " ")
		} else {
			cmdParts[i] = "<compound>"
		}
	}
	cmdText := strings.Join(cmdParts, " | ")

	j := state.addJob(pgid, pids, cmdText, jobRunning)
	state.lastBgPid = pgid
	fmt.Fprintf(stderr, "[%d] %d\n", j.id, pgid)
	state.lastStatus = 0
}

// execPipeline runs a pipeline of one or more commands.
// Terminal foreground control is only applied when not inside a
// command substitution (state.substDepth == 0).
func execPipeline(state *shellState, pipe *parser.Pipeline, stdin, stdout, stderr *os.File) {
	n := len(pipe.Cmds)

	if n == 1 {
		state.lastStatus = execCommand(state, pipe.Cmds[0], stdin, stdout, stderr)
		return
	}

	// In a pipeline, builtins fall through to external command
	// lookup. Running a builtin in a pipeline would require forking
	// (to wire its output to a pipe), which defeats the purpose.
	// External /bin/echo, /bin/pwd etc. handle this case.

	foreground := state.interactive && state.substDepth == 0

	type pipePair struct{ r, w *os.File }
	pipes := make([]pipePair, n-1)
	for i := range pipes {
		r, w, err := os.Pipe()
		if err != nil {
			fmt.Fprintf(stderr, "gosh: pipe: %v\n", err)
			return
		}
		pipes[i] = pipePair{r, w}
	}

	type procInfo struct {
		proc       *os.Process
		files      []*os.File
		isCompound bool
	}
	infos := make([]procInfo, n)
	pgid := 0

	var wg sync.WaitGroup
	compoundStatus := make([]int, n)
	goroutineOwned := make(map[*os.File]bool)

	for i, cmd := range pipe.Cmds {
		var sin *os.File
		if i == 0 {
			sin = stdin
		} else {
			sin = pipes[i-1].r
		}

		var sout *os.File
		if i == n-1 {
			sout = stdout
		} else {
			sout = pipes[i].w
		}

		if simple, ok := cmd.(*parser.SimpleCmd); ok {
			fds := [3]*os.File{sin, sout, stderr}
			proc, opened := startProcess(state, simple, fds, pgid)
			infos[i] = procInfo{proc: proc, files: opened}

			if i == 0 && proc != nil {
				pgid = proc.Pid
				syscall.Setpgid(proc.Pid, proc.Pid)
				if foreground {
					tcsetpgrp(state.termFd, pgid)
				}
			}
		} else {
			// Compound command: run in goroutine with cloned state.
			infos[i] = procInfo{isCompound: true}
			clone := cloneShellState(state)
			idx := i
			body := cmd
			cmdIn, cmdOut := sin, sout
			if i > 0 {
				goroutineOwned[cmdIn] = true
			}
			if i < n-1 {
				goroutineOwned[cmdOut] = true
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				compoundStatus[idx] = execCommand(clone, body, cmdIn, cmdOut, stderr)
				if idx > 0 {
					cmdIn.Close()
				}
				if idx < n-1 {
					cmdOut.Close()
				}
			}()
		}
	}

	// Close parent-owned pipe ends.
	for _, p := range pipes {
		if !goroutineOwned[p.r] {
			p.r.Close()
		}
		if !goroutineOwned[p.w] {
			p.w.Close()
		}
	}

	var pids []int
	for _, info := range infos {
		if info.proc != nil {
			pids = append(pids, info.proc.Pid)
		}
	}

	anyStopped := false
	var lastNonZero int
	for i, info := range infos {
		if info.proc == nil || info.isCompound {
			continue
		}
		res := waitProc(info.proc)
		for _, f := range info.files {
			f.Close()
		}
		if res.stopped {
			anyStopped = true
		}
		if res.status != 0 {
			lastNonZero = res.status
		}
		if i == n-1 {
			if state.optPipefail && lastNonZero != 0 {
				state.lastStatus = lastNonZero
			} else {
				state.lastStatus = res.status
			}
		}
	}

	// Wait for compound command goroutines.
	wg.Wait()

	// Collect compound command statuses for pipefail.
	for i, info := range infos {
		if !info.isCompound {
			continue
		}
		if compoundStatus[i] != 0 {
			lastNonZero = compoundStatus[i]
		}
		if i == n-1 {
			if state.optPipefail && lastNonZero != 0 {
				state.lastStatus = lastNonZero
			} else {
				state.lastStatus = compoundStatus[i]
			}
		}
	}

	if foreground {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	if anyStopped {
		cmdParts := make([]string, n)
		for i, cmd := range pipe.Cmds {
			if simple, ok := cmd.(*parser.SimpleCmd); ok {
				cmdParts[i] = strings.Join(simple.ArgStrings(), " ")
			} else {
				cmdParts[i] = "<compound>"
			}
		}
		cmdText := strings.Join(cmdParts, " | ")
		j := state.addJob(pgid, pids, cmdText, jobStopped)
		fmt.Fprintf(stderr, "[%d]+  Stopped                 %s\n", j.id, j.cmd)
	}
}

// withRedirects applies redirections around a compound command body.
// If no redirects are present, it calls fn directly with the original fds.
func withRedirects(redirs []parser.Redirect, stdin, stdout, stderr *os.File, fn func(*os.File, *os.File, *os.File) int) int {
	if len(redirs) == 0 {
		return fn(stdin, stdout, stderr)
	}
	fds := [3]*os.File{stdin, stdout, stderr}
	fds, opened, err := applyRedirects(redirs, fds)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: %v\n", err)
		return 1
	}
	result := fn(fds[0], fds[1], fds[2])
	for _, f := range opened {
		f.Close()
	}
	return result
}

// execCommand dispatches a Command (simple or compound) for execution.
func execCommand(state *shellState, cmd parser.Command, stdin, stdout, stderr *os.File) int {
	switch c := cmd.(type) {
	case *parser.SimpleCmd:
		return execSimple(state, c, stdin, stdout, stderr)
	case *parser.IfCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execIf(state, c, in, out, serr)
		})
	case *parser.WhileCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execWhile(state, c, in, out, serr)
		})
	case *parser.UntilCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execUntil(state, c, in, out, serr)
		})
	case *parser.ForCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execFor(state, c, in, out, serr)
		})
	case *parser.ArithForCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execArithFor(state, c, in, out, serr)
		})
	case *parser.CaseCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execCase(state, c, in, out, serr)
		})
	case *parser.SelectCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execSelect(state, c, in, out, serr)
		})
	case *parser.DblBracketCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execDblBracket(state, c, serr)
		})
	case *parser.SubshellCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execSubshell(state, c, in, out, serr)
		})
	case *parser.ArithCmd:
		return withRedirects(c.Redirects, stdin, stdout, stderr, func(in, out, serr *os.File) int {
			return execArithCmd(state, c, serr)
		})
	case *parser.FuncDef:
		state.funcs[c.Name] = c.Body
		return 0
	default:
		fmt.Fprintf(stderr, "gosh: unknown command type\n")
		return 1
	}
}

// execSimple runs a single command (not in a pipeline).
// Builtins are handled here; in pipelines they fall through
// to external commands.
func execSimple(state *shellState, cmd *parser.SimpleCmd, stdin, stdout, stderr *os.File) int {
	// Handle assignment-only commands: just set variables.
	if len(cmd.Args) == 0 {
		for _, a := range cmd.Assigns {
			execAssignment(state, a, stderr)
		}
		return 0
	}

	// Process substitution: replace <(cmd) / >(cmd) args with /dev/fd/N.
	procCleanup := processProcSubsts(state, cmd, stdin, stdout, stderr)
	defer procCleanup()

	args := cmd.ArgStrings()

	// Special-case exec: needs access to the SimpleCmd redirects.
	if len(args) > 0 && args[0] == "exec" {
		return execExec(state, cmd, args[1:], stdin, stdout, stderr)
	}

	// Xtrace: print commands before execution.
	if state.optXtrace && len(args) > 0 {
		ps4 := state.vars["PS4"]
		if ps4 == "" {
			ps4 = "+ "
		}
		fmt.Fprintf(stderr, "%s%s\n", ps4, strings.Join(args, " "))
	}

	// Check for user-defined functions.
	if body, ok := state.funcs[args[0]]; ok {
		saved := make(map[string]savedVar, len(cmd.Assigns))
		for _, a := range cmd.Assigns {
			saveVarState(state, saved, a.Name)
			execAssignment(state, a, stderr)
		}

		fds := [3]*os.File{stdin, stdout, stderr}
		fds, opened, err := applyRedirects(cmd.Redirects, fds)
		if err != nil {
			fmt.Fprintf(stderr, "gosh: %v\n", err)
			restoreVars(state, saved)
			return 1
		}

		status := execFunction(state, body, args[1:], fds[0], fds[1], fds[2])
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
			saveVarState(state, saved, a.Name)
			execAssignment(state, a, stderr)
		}

		fds := [3]*os.File{stdin, stdout, stderr}
		fds, opened, err := applyRedirects(cmd.Redirects, fds)
		if err != nil {
			fmt.Fprintf(stderr, "gosh: %v\n", err)
			restoreVars(state, saved)
			return 1
		}
		status := fn(state, args[1:], fds[0], fds[1], fds[2])
		for _, f := range opened {
			f.Close()
		}

		restoreVars(state, saved)
		return status
	}

	// External command.
	fds := [3]*os.File{stdin, stdout, stderr}
	proc, opened := startProcess(state, cmd, fds, 0)
	if proc == nil {
		return 127
	}

	pgid := proc.Pid
	syscall.Setpgid(pgid, pgid)

	if state.interactive && state.substDepth == 0 {
		tcsetpgrp(state.termFd, pgid)
	}

	res := waitProc(proc)
	for _, f := range opened {
		f.Close()
	}

	if state.interactive && state.substDepth == 0 {
		tcsetpgrp(state.termFd, state.shellPgid)
	}

	if res.stopped {
		cmdText := cmd.ArgStrings()
		j := state.addJob(pgid, []int{pgid}, strings.Join(cmdText, " "), jobStopped)
		fmt.Fprintf(stderr, "[%d]+  Stopped                 %s\n", j.id, j.cmd)
		return res.status
	}

	return res.status
}

// execExec implements the exec builtin.
// With args: replaces the shell process with the given command.
// Without args but with redirects: permanently redirects shell fds.
// Without args or redirects: no-op.
func execExec(state *shellState, cmd *parser.SimpleCmd, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 {
		// exec with redirects only (or no-op).
		if len(cmd.Redirects) == 0 {
			return 0
		}
		fds := [3]*os.File{os.Stdin, os.Stdout, os.Stderr}
		fds, _, err := applyRedirects(cmd.Redirects, fds)
		if err != nil {
			fmt.Fprintf(stderr, "gosh: exec: %v\n", err)
			return 1
		}
		// Permanently apply redirects via dup2.
		for i, f := range fds {
			if f.Fd() != uintptr(i) {
				if err := syscall.Dup2(int(f.Fd()), i); err != nil {
					fmt.Fprintf(stderr, "gosh: exec: dup2: %v\n", err)
					return 1
				}
				switch i {
				case 0:
					os.Stdin = os.NewFile(0, f.Name())
				case 1:
					os.Stdout = os.NewFile(1, f.Name())
				case 2:
					os.Stderr = os.NewFile(2, f.Name())
				}
			}
		}
		return 0
	}

	// exec cmd args... — replace process.
	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "gosh: exec: %s: not found\n", args[0])
		return 127
	}

	// Apply redirects.
	fds := [3]*os.File{os.Stdin, os.Stdout, os.Stderr}
	fds, _, redirErr := applyRedirects(cmd.Redirects, fds)
	if redirErr != nil {
		fmt.Fprintf(stderr, "gosh: exec: %v\n", redirErr)
		return 1
	}
	for i, f := range fds {
		if f.Fd() != uintptr(i) {
			syscall.Dup2(int(f.Fd()), i)
		}
	}

	// Build environment.
	env := state.environ()
	for _, a := range cmd.Assigns {
		env = append(env, a.Name+"="+a.Value.String())
	}

	// Save history and fire EXIT trap.
	if state.ed != nil {
		state.ed.Close()
	}
	state.runTrap("EXIT")

	// Replace the process.
	execErr := syscall.Exec(path, args, env)
	// If we get here, exec failed.
	fmt.Fprintf(os.Stderr, "gosh: exec: %s: %v\n", args[0], execErr)
	return 126
}

// execFunction runs a user-defined function body with positional params.
func execFunction(state *shellState, body *parser.List, args []string, stdin, stdout, stderr *os.File) int {
	// Save and set positional parameters.
	savedParams := state.positionalParams
	state.positionalParams = args

	// Push a new local scope for this function call.
	state.localScopes = append(state.localScopes, make(map[string]savedVar))

	cloned := parser.CloneList(body)
	execList(state, cloned, stdin, stdout, stderr)

	// Fire RETURN trap before restoring state.
	state.runTrapWithIO("RETURN", stdin, stdout, stderr)

	// Pop and restore local variables.
	scope := state.localScopes[len(state.localScopes)-1]
	state.localScopes = state.localScopes[:len(state.localScopes)-1]
	restoreVars(state, scope)

	status := state.lastStatus
	state.returnFlag = false
	state.positionalParams = savedParams

	return status
}

// execAssignment processes a single assignment, handling scalar, array,
// and indexed assignments.
func execAssignment(state *shellState, a parser.Assignment, stderr *os.File) {
	if a.Array != nil {
		// Array assignment: arr=(a b c) or arr+=(x y)
		var vals []string
		for _, w := range a.Array {
			vals = append(vals, w.String())
		}
		if a.Append {
			state.appendArray(a.Name, vals)
		} else {
			state.setArray(a.Name, vals)
		}
		return
	}
	if a.Index != "" {
		// Indexed assignment: arr[expr]=val
		// Evaluate subscript as arithmetic.
		subscript := expander.ExpandDollar(a.Index, state.lookup)
		idx, err := expander.EvalArith(subscript, state.lookup, state.setVar)
		if err != nil {
			fmt.Fprintf(stderr, "gosh: %s: bad array subscript\n", a.Index)
			return
		}
		key := a.Name + "[" + strconv.FormatInt(idx, 10) + "]"
		state.setVar(key, a.Value.String())
		return
	}
	if a.Append {
		// Integer append: arithmetic addition.
		if state.isInteger(a.Name) {
			state.setVar(a.Name, state.vars[a.Name]+"+"+a.Value.String())
			return
		}
		// String append: var+=value
		state.setVar(a.Name, state.vars[a.Name]+a.Value.String())
		return
	}
	state.setVar(a.Name, a.Value.String())
}

// savedVar records a variable's previous state for restoration.
type savedVar struct {
	value    string
	exists   bool
	isArray  bool
	arrayVal []string
	attrs    uint8
}

// restoreVars undoes temporary per-command variable assignments.
func restoreVars(state *shellState, saved map[string]savedVar) {
	for name, sv := range saved {
		// Restore attrs first (before setVar, which checks readonly).
		if sv.exists {
			if sv.attrs != 0 {
				state.attrs[name] = sv.attrs
			} else {
				delete(state.attrs, name)
			}
		} else {
			delete(state.attrs, name)
		}
		if sv.isArray {
			if sv.exists {
				state.arrays[name] = sv.arrayVal
			} else {
				delete(state.arrays, name)
			}
		} else {
			if sv.exists {
				state.vars[name] = sv.value
			} else {
				delete(state.vars, name)
			}
		}
	}
}

// saveVarState saves the current state of a variable (scalar or array)
// for later restoration.
func saveVarState(state *shellState, saved map[string]savedVar, name string) {
	if _, already := saved[name]; already {
		return
	}
	if arr, isArr := state.arrays[name]; isArr {
		cp := make([]string, len(arr))
		copy(cp, arr)
		saved[name] = savedVar{exists: true, isArray: true, arrayVal: cp, attrs: state.attrs[name]}
	} else {
		old, exists := state.vars[name]
		saved[name] = savedVar{value: old, exists: exists, attrs: state.attrs[name]}
	}
}
