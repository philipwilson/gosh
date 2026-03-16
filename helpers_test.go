package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"gosh/lexer"
	"gosh/parser"
)

// testState creates a minimal non-interactive shell state for tests.
func testState(t *testing.T) *shellState {
	t.Helper()
	home := t.TempDir()
	s := &shellState{
		vars:           make(map[string]string),
		arrays:         make(map[string][]string),
		exported:       make(map[string]bool),
		aliases:        make(map[string]string),
		funcs:          make(map[string]*parser.List),
		traps:          make(map[string]string),
		pendingSignals: make(map[string]bool),
		sigCh:          make(chan os.Signal, 8),
	}
	s.vars["HOME"] = home
	s.vars["PATH"] = "/bin:/usr/bin"
	s.vars["PWD"], _ = os.Getwd()
	s.vars["USER"] = os.Getenv("USER")
	s.vars["IFS"] = " \t\n"
	return s
}

// runCapture lexes, parses, expands, and executes a command string,
// capturing stdout output. Mirrors the cmdSubst approach.
func runCapture(t *testing.T, state *shellState, cmd string) string {
	t.Helper()
	tokens, err := lexer.Lex(cmd)
	if err != nil {
		t.Fatalf("lex %q: %v", cmd, err)
	}

	// Resolve heredocs if present.
	if lexer.HasHeredocs(tokens) {
		// For tests with heredocs, the body should already be inline.
		// Split remaining lines as heredoc body.
		lines := strings.Split(cmd, "\n")
		lineIdx := 1
		err := lexer.ResolveHeredocs(tokens, func() (string, bool) {
			if lineIdx >= len(lines) {
				return "", false
			}
			line := lines[lineIdx]
			lineIdx++
			return line, true
		})
		if err != nil {
			t.Fatalf("resolve heredocs %q: %v", cmd, err)
		}
		// Re-lex just the first line for proper token stream.
		// Actually, the tokens are already resolved. Continue.
	}

	// Expand aliases.
	tokens = expandAliases(state, tokens)

	list, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse %q: %v", cmd, err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	execList(state, list, os.Stdin, w)
	w.Close()

	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}

	return strings.TrimRight(string(out), "\n")
}

func assertOutput(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func assertStatus(t *testing.T, state *shellState, want int) {
	t.Helper()
	if state.lastStatus != want {
		t.Errorf("status = %d, want %d", state.lastStatus, want)
	}
}

func assertVar(t *testing.T, state *shellState, name, want string) {
	t.Helper()
	got := state.lookup(name)
	if got != want {
		t.Errorf("$%s = %q, want %q", name, got, want)
	}
}

// runCaptureBoth runs a command capturing both stdout and stderr.
func runCaptureBoth(t *testing.T, state *shellState, cmd string) (stdout, stderr string) {
	t.Helper()
	tokens, err := lexer.Lex(cmd)
	if err != nil {
		t.Fatalf("lex %q: %v", cmd, err)
	}
	tokens = expandAliases(state, tokens)

	list, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse %q: %v", cmd, err)
	}

	// Capture stdout.
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	// Capture stderr.
	oldStderr := os.Stderr
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = wErr

	execList(state, list, os.Stdin, wOut)
	wOut.Close()
	wErr.Close()
	os.Stderr = oldStderr

	outBytes, _ := io.ReadAll(rOut)
	rOut.Close()
	errBytes, _ := io.ReadAll(rErr)
	rErr.Close()

	return strings.TrimRight(string(outBytes), "\n"), strings.TrimRight(string(errBytes), "\n")
}
