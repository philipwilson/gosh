package main

import (
	"os"
	"testing"
)

func TestBraceGroupBasic(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "{ echo hello; echo world; }")
	assertOutput(t, got, "hello\nworld")
}

func TestBraceGroupRedirect(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	out := tmp + "/out.txt"
	runCapture(t, s, "{ echo line1; echo line2; } > "+out)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if got != "line1\nline2\n" {
		t.Errorf("got %q, want %q", got, "line1\nline2\n")
	}
}

func TestBraceGroupVariablesPersist(t *testing.T) {
	// Unlike subshells, brace group changes affect the parent.
	s := testState(t)
	s.setVar("x", "before")
	runCapture(t, s, "{ x=after; }")
	assertVar(t, s, "x", "after")
}

func TestBraceGroupVsSubshell(t *testing.T) {
	// Subshell isolates; brace group does not.
	s := testState(t)
	s.setVar("x", "outer")
	runCapture(t, s, "(x=inner)")
	assertVar(t, s, "x", "outer") // subshell: no change

	runCapture(t, s, "{ x=inner; }")
	assertVar(t, s, "x", "inner") // brace group: changed
}

func TestBraceGroupPipeline(t *testing.T) {
	s := testState(t)
	got := runCaptureWithStdin(t, s,
		`echo "hello world" | { read line; echo "got: $line"; }`, "")
	assertOutput(t, got, "got: hello world")
}

func TestBraceGroupAndOr(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "{ true; } && echo ok")
	assertOutput(t, got, "ok")

	got = runCapture(t, s, "{ false; } || echo caught")
	assertOutput(t, got, "caught")
}

func TestBraceGroupNested(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "{ { echo nested; }; }")
	assertOutput(t, got, "nested")
}

func TestBraceGroupExitStatus(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "{ true; }")
	assertStatus(t, s, 0)

	runCapture(t, s, "{ false; }")
	assertStatus(t, s, 1)
}

func TestBraceGroupMultiplePipeline(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `{ echo alpha; echo beta; } | { while read line; do echo "line=$line"; done; }`)
	assertOutput(t, got, "line=alpha\nline=beta")
}
