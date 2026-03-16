package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// --- Lookup special variables ---

func TestLookupExitStatus(t *testing.T) {
	s := testState(t)
	s.lastStatus = 42
	assertVar(t, s, "?", "42")
}

func TestLookupPID(t *testing.T) {
	s := testState(t)
	got := s.lookup("$")
	pid := strconv.Itoa(os.Getpid())
	if got != pid {
		t.Errorf("$$ = %q, want %q", got, pid)
	}
}

func TestLookupParamCount(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"a", "b", "c"}
	assertVar(t, s, "#", "3")
}

func TestLookupAtStar(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"x", "y"}
	assertVar(t, s, "@", "x y")
	assertVar(t, s, "*", "x y")
}

func TestLookupZero(t *testing.T) {
	s := testState(t)
	assertVar(t, s, "0", "gosh")
}

func TestLookupPositional(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"alpha", "beta", "gamma"}
	assertVar(t, s, "1", "alpha")
	assertVar(t, s, "2", "beta")
	assertVar(t, s, "3", "gamma")
	assertVar(t, s, "4", "") // out of range
}

// --- Scalar variables ---

func TestSetVarLookup(t *testing.T) {
	s := testState(t)
	s.setVar("FOO", "bar")
	assertVar(t, s, "FOO", "bar")
}

func TestUnsetVar(t *testing.T) {
	s := testState(t)
	s.setVar("X", "1")
	s.exportVar("X")
	s.unsetVar("X")
	assertVar(t, s, "X", "")
	if s.exported["X"] {
		t.Error("X still exported after unset")
	}
}

// --- Array variables ---

func TestSetArray(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"a", "b", "c"})
	assertVar(t, s, "arr[0]", "a")
	assertVar(t, s, "arr[1]", "b")
	assertVar(t, s, "arr[2]", "c")
}

func TestAppendArray(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"a"})
	s.appendArray("arr", []string{"b", "c"})
	assertVar(t, s, "arr[@]", "a b c")
}

func TestArrayAtStar(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"x", "y", "z"})
	assertVar(t, s, "arr[@]", "x y z")
	assertVar(t, s, "arr[*]", "x y z")
}

func TestArrayCount(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"a", "b", "c"})
	assertVar(t, s, "#arr[@]", "3")
}

func TestArrayBareNameReturnsElem0(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"first", "second"})
	assertVar(t, s, "arr", "first")
}

func TestUnsetArrayElement(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"a", "b", "c"})
	s.unsetVar("arr[1]")
	// Element 1 is now empty, so [@] skips it.
	assertVar(t, s, "arr[@]", "a c")
	assertVar(t, s, "#arr[@]", "2")
}

func TestUnsetWholeArray(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"a", "b"})
	s.unsetVar("arr")
	assertVar(t, s, "arr[@]", "")
}

func TestSetVarArrayElement(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"a", "b"})
	s.setVar("arr[1]", "B")
	assertVar(t, s, "arr[1]", "B")
}

func TestSetVarGrowsArray(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"a"})
	s.setVar("arr[3]", "d")
	assertVar(t, s, "arr[3]", "d")
	// Elements 1 and 2 are empty strings.
	assertVar(t, s, "arr[1]", "")
}

// --- parseArrayRef ---

func TestParseArrayRefValid(t *testing.T) {
	name, sub, ok := parseArrayRef("arr[0]")
	if !ok || name != "arr" || sub != "0" {
		t.Errorf("parseArrayRef(arr[0]) = (%q, %q, %v)", name, sub, ok)
	}
}

func TestParseArrayRefAt(t *testing.T) {
	name, sub, ok := parseArrayRef("arr[@]")
	if !ok || name != "arr" || sub != "@" {
		t.Errorf("parseArrayRef(arr[@]) = (%q, %q, %v)", name, sub, ok)
	}
}

func TestParseArrayRefInvalid(t *testing.T) {
	_, _, ok := parseArrayRef("foo")
	if ok {
		t.Error("parseArrayRef(foo) should return false")
	}
	_, _, ok = parseArrayRef("foo[bar")
	if ok {
		t.Error("parseArrayRef(foo[bar) should return false")
	}
}

// --- environ ---

func TestEnvironIncludesExports(t *testing.T) {
	s := testState(t)
	s.setVar("MYVAR", "val")
	s.exportVar("MYVAR")
	env := s.environ()
	found := false
	for _, e := range env {
		if e == "MYVAR=val" {
			found = true
			break
		}
	}
	if !found {
		t.Error("environ() should include MYVAR=val")
	}
}

func TestEnvironExcludesArrays(t *testing.T) {
	s := testState(t)
	s.setArray("arr", []string{"a", "b"})
	s.exportVar("arr")
	env := s.environ()
	for _, e := range env {
		if strings.HasPrefix(e, "arr=") {
			t.Error("environ() should exclude arrays")
		}
	}
}

// --- formatPrompt ---

func TestFormatPromptUser(t *testing.T) {
	s := testState(t)
	s.vars["USER"] = "testuser"
	got := s.formatPrompt(`\u`)
	if got != "testuser" {
		t.Errorf(`formatPrompt(\u) = %q, want "testuser"`, got)
	}
}

func TestFormatPromptCwd(t *testing.T) {
	s := testState(t)
	s.vars["HOME"] = "/home/test"
	s.vars["PWD"] = "/home/test/projects"
	got := s.formatPrompt(`\w`)
	if got != "~/projects" {
		t.Errorf(`formatPrompt(\w) = %q, want "~/projects"`, got)
	}
}

func TestFormatPromptBasename(t *testing.T) {
	s := testState(t)
	s.vars["HOME"] = "/home/test"
	s.vars["PWD"] = "/home/test"
	got := s.formatPrompt(`\W`)
	if got != "~" {
		t.Errorf(`formatPrompt(\W) = %q, want "~"`, got)
	}
}

func TestFormatPromptDollar(t *testing.T) {
	s := testState(t)
	got := s.formatPrompt(`\$`)
	// Non-root should give $.
	if os.Getuid() != 0 && got != "$" {
		t.Errorf(`formatPrompt(\$) = %q, want "$"`, got)
	}
}

func TestFormatPromptNewline(t *testing.T) {
	s := testState(t)
	got := s.formatPrompt(`a\nb`)
	if got != "a\nb" {
		t.Errorf(`formatPrompt(a\nb) = %q, want "a\nb"`, got)
	}
}

func TestFormatPromptEscape(t *testing.T) {
	s := testState(t)
	got := s.formatPrompt(`\e`)
	if got != "\x1b" {
		t.Errorf(`formatPrompt(\e) = %q, want ESC`, got)
	}
}

func TestFormatPromptBackslash(t *testing.T) {
	s := testState(t)
	got := s.formatPrompt(`\\`)
	if got != `\` {
		t.Errorf(`formatPrompt(\\) = %q, want "\"`, got)
	}
}

func TestFormatPromptBrackets(t *testing.T) {
	s := testState(t)
	got := s.formatPrompt(`\[X\]`)
	if got != "X" {
		t.Errorf(`formatPrompt(\[X\]) = %q, want "X"`, got)
	}
}

func TestFormatPromptUnknown(t *testing.T) {
	s := testState(t)
	got := s.formatPrompt(`\z`)
	if got != `\z` {
		t.Errorf(`formatPrompt(\z) = %q, want "\\z"`, got)
	}
}

// --- needsMore ---

func TestNeedsMoreTrailingBackslash(t *testing.T) {
	if !needsMore(`echo hello \`) {
		t.Error(`needsMore("echo hello \") should be true`)
	}
}

func TestNeedsMoreUnclosedQuote(t *testing.T) {
	if !needsMore(`echo "hello`) {
		t.Error(`needsMore(echo "hello) should be true`)
	}
}

func TestNeedsMoreTrailingPipe(t *testing.T) {
	if !needsMore(`echo hello |`) {
		t.Error(`needsMore("echo hello |") should be true`)
	}
}

func TestNeedsMoreIncompleteIf(t *testing.T) {
	if !needsMore(`if true`) {
		t.Error(`needsMore("if true") should be true`)
	}
}

func TestNeedsMoreComplete(t *testing.T) {
	if needsMore(`echo hello`) {
		t.Error(`needsMore("echo hello") should be false`)
	}
}

func TestNeedsMoreTrailingAnd(t *testing.T) {
	if !needsMore(`true &&`) {
		t.Error(`needsMore("true &&") should be true`)
	}
}

func TestNeedsMoreTrailingOr(t *testing.T) {
	if !needsMore(`true ||`) {
		t.Error(`needsMore("true ||") should be true`)
	}
}
