package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gosh/editor"
	"gosh/expander"
	"gosh/lexer"
	"gosh/parser"
)

// version is set at build time via -ldflags "-X main.version=...".
// Defaults to "dev" for plain `go build` without flags.
var version = "dev"

// shellState holds the shell's mutable state: variables, export
// set, last exit status, and terminal control info.
type shellState struct {
	vars          map[string]string // shell variables
	exported      map[string]bool   // which variables are exported to children
	lastStatus    int               // $? — exit status of last command
	interactive   bool              // true if stdin is a terminal
	shellPgid     int               // the shell's own process group ID
	termFd        int               // file descriptor of the controlling terminal
	exitFlag      bool              // set by exit builtin to stop the REPL
	jobs          []*job            // job table for background/stopped jobs
	nextJobID     int               // next job number to assign
	debugTokens   bool              // print tokens before parsing
	debugAST      bool              // print AST before expansion
	debugExpanded bool              // print AST after expansion
	ed            *editor.Editor    // line editor (nil if non-interactive)
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

		// SIGTTOU must be SIG_IGN so the shell can call tcsetpgrp
		// from a background process group. SIG_IGN persists across
		// exec, but that's acceptable — children in the foreground
		// group won't receive SIGTTOU anyway.
		signal.Ignore(syscall.SIGTTOU)

		// SIGINT and SIGTSTP use signal.Notify (not signal.Ignore).
		// signal.Ignore sets SIG_IGN at the OS level, which persists
		// across exec (POSIX: only caught handlers are reset to
		// SIG_DFL by exec). That would make Ctrl-C and Ctrl-Z
		// ineffective in child processes.
		//
		// signal.Notify installs Go's own caught handler. After exec,
		// POSIX resets caught handlers to SIG_DFL, so children get
		// default signal behavior.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTSTP)
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

// cmdSubst executes a command string and returns its stdout output
// with trailing newlines stripped. Used for $(cmd) and `cmd` expansion.
func (s *shellState) cmdSubst(cmd string) (string, error) {
	tokens, err := lexer.Lex(cmd)
	if err != nil {
		return "", err
	}
	if len(tokens) == 1 && tokens[0].Type == lexer.TOKEN_EOF {
		return "", nil
	}

	list, err := parser.Parse(tokens)
	if err != nil {
		return "", err
	}

	expander.Expand(list, s.lookup, s.cmdSubst)

	// Create a pipe to capture stdout.
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}

	// Execute the command with stdout directed to the pipe.
	oldStatus := s.lastStatus
	for _, entry := range list.Entries {
		execPipelineSubst(s, entry.Pipeline, w)
	}
	w.Close()

	// Read all output from the pipe.
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		s.lastStatus = oldStatus
		return "", err
	}

	// Strip trailing newlines (bash behavior).
	return strings.TrimRight(string(out), "\n"), nil
}

// --- Main loop ---

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("gosh %s\n", version)
		return
	}

	state := newShellState()

	if state.interactive {
		histPath := filepath.Join(state.vars["HOME"], ".gosh_history")
		ed, err := editor.New(state.termFd, histPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: editor init failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "gosh: falling back to simple input")
		} else {
			state.ed = ed
			state.ed.Complete = state.complete
			defer ed.Close()
		}
	}

	if state.ed != nil {
		runInteractive(state)
	} else {
		runNonInteractive(state)
	}

	os.Exit(state.lastStatus)
}

func runInteractive(state *shellState) {
	for {
		state.reapJobs()
		line, err := state.ed.ReadLine("gosh$ ")
		if err == io.EOF {
			fmt.Fprintln(os.Stderr)
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: read: %v\n", err)
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if runLine(state, line) {
			break
		}

		state.ed.History.Add(line)
	}
}

func runNonInteractive(state *shellState) {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		if state.interactive {
			fmt.Fprintf(os.Stderr, "gosh$ ")
		}

		if !scanner.Scan() {
			if state.interactive {
				fmt.Fprintln(os.Stderr)
			}
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if runLine(state, line) {
			break
		}
	}
}

// runLine lexes, parses, expands, and executes a single input line.
// Returns true if the shell should exit.
func runLine(state *shellState, line string) bool {
	tokens, err := lexer.Lex(line)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
		return false
	}

	if len(tokens) == 1 && tokens[0].Type == lexer.TOKEN_EOF {
		return false
	}

	if state.debugTokens {
		for _, tok := range tokens {
			fmt.Fprintf(os.Stderr, "  %s\n", tok)
		}
	}

	list, err := parser.Parse(tokens)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
		return false
	}

	if state.debugAST {
		fmt.Fprintf(os.Stderr, "  %s\n", list)
	}

	expander.Expand(list, state.lookup, state.cmdSubst)

	if state.debugExpanded {
		fmt.Fprintf(os.Stderr, "  %s\n", list)
	}

	execList(state, list)

	return state.exitFlag
}
