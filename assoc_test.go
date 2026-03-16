package main

import (
	"strings"
	"testing"
)

func TestAssocArrayDeclare(t *testing.T) {
	s := testState(t)
	assertOutput(t, runCapture(t, s, "declare -A m; echo ${#m[@]}"), "0")
}

func TestAssocArrayDeclareWithValues(t *testing.T) {
	s := testState(t)
	assertOutput(t, runCapture(t, s, "declare -A m=([foo]=bar [baz]=qux)"), "")
	assertOutput(t, runCapture(t, s, "echo ${m[foo]}"), "bar")
	assertOutput(t, runCapture(t, s, "echo ${m[baz]}"), "qux")
}

func TestAssocArrayElementAssignment(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m")
	runCapture(t, s, "m[key]=val")
	assertOutput(t, runCapture(t, s, "echo ${m[key]}"), "val")
}

func TestAssocArrayCount(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([a]=1 [b]=2 [c]=3)")
	assertOutput(t, runCapture(t, s, "echo ${#m[@]}"), "3")
}

func TestAssocArrayValueExpansion(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([a]=1 [b]=2)")
	// Values should be sorted by key.
	got := runCapture(t, s, `for v in "${m[@]}"; do echo $v; done`)
	assertOutput(t, got, "1\n2")
}

func TestAssocArrayKeyEnumeration(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([foo]=1 [bar]=2)")
	// Keys should be sorted alphabetically.
	assertOutput(t, runCapture(t, s, "echo ${!m[@]}"), "bar foo")
}

func TestAssocArrayKeyEnumerationMultiWord(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([foo]=1 [bar]=2)")
	// "${!m[@]}" should produce separate words.
	got := runCapture(t, s, `for k in "${!m[@]}"; do echo $k; done`)
	assertOutput(t, got, "bar\nfoo")
}

func TestAssocArrayVariableSubscript(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([foo]=bar)")
	s.setVar("k", "foo")
	assertOutput(t, runCapture(t, s, "echo ${m[$k]}"), "bar")
}

func TestAssocArrayModify(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([a]=1)")
	runCapture(t, s, "m[b]=2")
	assertOutput(t, runCapture(t, s, "echo ${#m[@]}"), "2")
}

func TestAssocArrayUnsetElement(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([a]=1 [b]=2)")
	runCapture(t, s, "unset 'm[a]'")
	assertOutput(t, runCapture(t, s, "echo ${#m[@]}"), "1")
}

func TestAssocArrayOverwrite(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([k]=old)")
	runCapture(t, s, "m[k]=new")
	assertOutput(t, runCapture(t, s, "echo ${m[k]}"), "new")
}

func TestAssocArrayAppend(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([a]=1)")
	runCapture(t, s, "m+=([b]=2 [c]=3)")
	assertOutput(t, runCapture(t, s, "echo ${#m[@]}"), "3")
}

func TestAssocArraySubshellIsolation(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([k]=v)")
	runCapture(t, s, "(m[k]=changed)")
	assertOutput(t, runCapture(t, s, "echo ${m[k]}"), "v")
}

func TestAssocArrayLocalA(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `f() { local -A m; m[k]=v; echo ${m[k]}; }; f`)
	assertOutput(t, got, "v")
	assertOutput(t, runCapture(t, s, "echo ${#m[@]}"), "0")
}

func TestDeclareG(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "f() { declare -g x=global; }; f")
	assertOutput(t, runCapture(t, s, "echo $x"), "global")
}

func TestDeclareGA(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "f() { declare -gA gm; gm[k]=v; }; f")
	assertOutput(t, runCapture(t, s, "echo ${gm[k]}"), "v")
}

func TestDeclareNoGStaysLocal(t *testing.T) {
	s := testState(t)
	s.setVar("x", "outer")
	runCapture(t, s, "f() { declare x=inner; }; f")
	assertOutput(t, runCapture(t, s, "echo $x"), "outer")
}

func TestDeclarePAssoc(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([k]=v)")
	got := runCapture(t, s, "declare -p m")
	if !strings.Contains(got, "-A") {
		t.Errorf("declare -p output should contain -A, got: %s", got)
	}
	if !strings.Contains(got, "m=") {
		t.Errorf("declare -p output should contain m=, got: %s", got)
	}
}

func TestDeclareConflictingFlags(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, "declare -aA x")
	if !strings.Contains(stderr, "cannot use -a and -A") {
		t.Errorf("expected error for -aA, got stderr: %s", stderr)
	}
	assertStatus(t, s, 1)
}

func TestDeclarePlusA(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, "declare +A x")
	if !strings.Contains(stderr, "cannot remove") {
		t.Errorf("expected error for +A, got stderr: %s", stderr)
	}
	assertStatus(t, s, 1)
}

func TestAssocArrayBareNameEmpty(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "declare -A m=([k]=v)")
	// Bare associative array name returns empty (bash behavior).
	assertOutput(t, runCapture(t, s, "echo $m"), "")
}

func TestDeclareArrayAssignment(t *testing.T) {
	// Test that declare -a arr=(...) works through the parser.
	s := testState(t)
	runCapture(t, s, "declare -a arr=(x y z)")
	assertOutput(t, runCapture(t, s, "echo ${arr[1]}"), "y")
}
