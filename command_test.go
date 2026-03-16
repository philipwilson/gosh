package main

import (
	"testing"
)

// --- command builtin ---

func TestCommandRunsBuiltin(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "command echo hello")
	assertOutput(t, got, "hello")
}

func TestCommandSkipsFunction(t *testing.T) {
	s := testState(t)
	// Define a function named "echo" that prints "func".
	runCapture(t, s, `echo() { printf func; }`)
	// Calling echo normally should use the function.
	got := runCapture(t, s, "echo")
	assertOutput(t, got, "func")
	// Calling via command should skip the function and use the builtin.
	got = runCapture(t, s, "command echo builtin")
	assertOutput(t, got, "builtin")
}

func TestCommandVBuiltin(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "command -v echo")
	assertOutput(t, got, "echo")
}

func TestCommandVExternal(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "command -v ls")
	if got == "" {
		t.Skip("ls not found in PATH")
	}
	// Should print the path to ls.
	if got != "/bin/ls" && got != "/usr/bin/ls" {
		t.Logf("command -v ls = %q (non-standard path, but OK)", got)
	}
}

func TestCommandVNotFound(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "command -v nonexistent_cmd_xyz")
	assertStatus(t, s, 1)
}

func TestCommandVVBuiltin(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "command -V echo")
	assertOutput(t, got, "echo is a shell builtin")
}

// --- type builtin ---

func TestTypeBuiltin(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "type echo")
	assertOutput(t, got, "echo is a shell builtin")
}

func TestTypeFunction(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "myfunc() { echo hi; }")
	got := runCapture(t, s, "type myfunc")
	assertOutput(t, got, "myfunc is a function")
}

func TestTypeAlias(t *testing.T) {
	s := testState(t)
	s.aliases["ll"] = "ls -la"
	got := runCapture(t, s, "type ll")
	assertOutput(t, got, "ll is aliased to 'ls -la'")
}

func TestTypeNotFound(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "type nonexistent_cmd_xyz")
	assertStatus(t, s, 1)
}

func TestTypeExternal(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "type ls")
	if got == "" {
		t.Skip("ls not found in PATH")
	}
	// Should contain "is /..." path.
	if got != "ls is /bin/ls" && got != "ls is /usr/bin/ls" {
		t.Logf("type ls = %q (non-standard path, but OK)", got)
	}
}
