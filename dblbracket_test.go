package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDblBracketStringEqual(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ hello == hello ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketStringNotEqual(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ hello != world ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketStringLt(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ abc < def ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketStringGt(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ def > abc ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketGlobMatch(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ hello == h* ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketGlobNoMatch(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ hello == w* ]]")
	assertStatus(t, s, 1)
}

func TestDblBracketIntGt(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ 5 -gt 3 ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketIntEq(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ 7 -eq 7 ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketIntLt(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ 2 -lt 5 ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketZEmpty(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `[[ -z "" ]]`)
	assertStatus(t, s, 0)
}

func TestDblBracketNNonEmpty(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ -n hello ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketFileExists(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.txt")
	os.WriteFile(f, []byte("hi"), 0644)
	runCapture(t, s, "[[ -e "+f+" ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketFileIsDir(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	runCapture(t, s, "[[ -d "+tmp+" ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketAnd(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ -n hello && -n world ]]")
	assertStatus(t, s, 0)
}

func TestDblBracketOr(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `[[ -z hello || -n world ]]`)
	assertStatus(t, s, 0)
}

func TestDblBracketNot(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `[[ ! -z hello ]]`)
	assertStatus(t, s, 0)
}

func TestDblBracketVarExpansion(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "x=hello; if [[ $x == hello ]]; then echo yes; fi")
	assertOutput(t, got, "yes")
}

func TestDblBracketFalse(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "[[ hello == world ]]")
	assertStatus(t, s, 1)
}

// --- Regex matching ---

func TestDblBracketRegexMatch(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `[[ hello =~ ^hel ]]`)
	assertStatus(t, s, 0)
}

func TestDblBracketRegexNoMatch(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `[[ hello =~ ^world ]]`)
	assertStatus(t, s, 1)
}

func TestDblBracketRegexDigits(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `[[ foo123bar =~ [0-9]+ ]]`)
	assertStatus(t, s, 0)
}

func TestDblBracketRegexEmpty(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `[[ "" =~ ^$ ]]`)
	assertStatus(t, s, 0)
}

func TestDblBracketRegexInConditional(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `if [[ "hello123" =~ [0-9]+ ]]; then echo matched; fi`)
	assertOutput(t, got, "matched")
}

func TestDblBracketRegexPartialMatch(t *testing.T) {
	s := testState(t)
	// Partial match should work (like bash =~).
	runCapture(t, s, `[[ foobar =~ ob ]]`)
	assertStatus(t, s, 0)
}

func TestDblBracketRegexInvalid(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `[[ hello =~ "[invalid" ]]`)
	assertStatus(t, s, 2)
}
