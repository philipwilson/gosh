package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Simple commands ---

func TestExecEchoHello(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "echo hello")
	assertOutput(t, got, "hello")
}

func TestExecEchoN(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "echo -n hello")
	assertOutput(t, got, "hello")
}

func TestExecEchoMultipleArgs(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "echo a b c")
	assertOutput(t, got, "a b c")
}

func TestExecAssignmentThenEcho(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "X=42; echo $X")
	assertOutput(t, got, "42")
}

func TestExecAssignmentOnly(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "X=hello")
	assertStatus(t, s, 0)
	assertVar(t, s, "X", "hello")
}

// --- Command substitution ---

func TestExecCmdSubst(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "echo $(echo inner)")
	assertOutput(t, got, "inner")
}

func TestExecCmdSubstNested(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "echo $(echo $(echo deep))")
	assertOutput(t, got, "deep")
}

// --- Operators ---

func TestExecAndTrue(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "true && echo yes")
	assertOutput(t, got, "yes")
}

func TestExecAndFalse(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "false && echo yes")
	assertOutput(t, got, "")
}

func TestExecOrTrue(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "true || echo no")
	assertOutput(t, got, "")
}

func TestExecOrFalse(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "false || echo yes")
	assertOutput(t, got, "yes")
}

func TestExecSemicolon(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "echo a; echo b")
	assertOutput(t, got, "a\nb")
}

// --- If/elif/else ---

func TestExecIfTrue(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "if true; then echo yes; fi")
	assertOutput(t, got, "yes")
}

func TestExecIfFalse(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "if false; then echo yes; fi")
	assertOutput(t, got, "")
}

func TestExecIfElse(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "if false; then echo yes; else echo no; fi")
	assertOutput(t, got, "no")
}

func TestExecIfElif(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "if false; then echo a; elif true; then echo b; else echo c; fi")
	assertOutput(t, got, "b")
}

// --- While ---

func TestExecWhileCount(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "i=0; while test $i -lt 3; do echo $i; i=$(( i + 1 )); done")
	assertOutput(t, got, "0\n1\n2")
}

func TestExecWhileBreak(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "i=0; while true; do i=$(( i + 1 )); if test $i -eq 3; then break; fi; echo $i; done")
	assertOutput(t, got, "1\n2")
}

func TestExecWhileContinue(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "i=0; while test $i -lt 4; do i=$(( i + 1 )); if test $i -eq 2; then continue; fi; echo $i; done")
	assertOutput(t, got, "1\n3\n4")
}

// --- For ---

func TestExecForBasic(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "for x in a b c; do echo $x; done")
	assertOutput(t, got, "a\nb\nc")
}

func TestExecForBreak(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "for x in a b c d; do if test $x = c; then break; fi; echo $x; done")
	assertOutput(t, got, "a\nb")
}

func TestExecForEmpty(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "for x in; do echo $x; done")
	assertOutput(t, got, "")
	assertStatus(t, s, 0)
}

// --- Arithmetic for ---

func TestExecArithFor(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "for (( i=0; i<3; i++ )); do echo $i; done")
	assertOutput(t, got, "0\n1\n2")
}

// --- Case ---

func TestExecCaseExact(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "case hello in hello) echo matched;; esac")
	assertOutput(t, got, "matched")
}

func TestExecCaseGlob(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "case hello in h*) echo glob;; esac")
	assertOutput(t, got, "glob")
}

func TestExecCaseNoMatch(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "case hello in world) echo no;; esac")
	assertOutput(t, got, "")
	assertStatus(t, s, 0)
}

func TestExecCaseMultiPattern(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "case b in a|b) echo ab;; esac")
	assertOutput(t, got, "ab")
}

// --- Functions ---

func TestExecFuncDefAndCall(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "greet() { echo hello; }; greet")
	assertOutput(t, got, "hello")
}

func TestExecFuncArgs(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "f() { echo $1 $2; }; f alpha beta")
	assertOutput(t, got, "alpha beta")
}

func TestExecFuncReturn(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "f() { return 42; }; f")
	assertStatus(t, s, 42)
}

func TestExecFuncLocal(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "x=outer; f() { local x=inner; echo $x; }; f; echo $x")
	assertOutput(t, got, "inner\nouter")
}

func TestExecFuncRecursion(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "countdown() { if test $1 -le 0; then echo done; return; fi; echo $1; countdown $(( $1 - 1 )); }; countdown 3")
	assertOutput(t, got, "3\n2\n1\ndone")
}

// --- Subshells ---

func TestExecSubshellOutput(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "(echo hello)")
	assertOutput(t, got, "hello")
}

func TestExecSubshellIsolation(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "x=1; (x=2; echo $x); echo $x")
	assertOutput(t, got, "2\n1")
}

// --- Arithmetic command ---

func TestExecArithCmdTrue(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "(( 5 > 3 ))")
	assertStatus(t, s, 0)
}

func TestExecArithCmdFalse(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "(( 0 ))")
	assertStatus(t, s, 1)
}

func TestExecArithCmdAssign(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "(( x = 5 ))")
	assertVar(t, s, "x", "5")
	assertStatus(t, s, 0)
}

// --- Redirections ---

func TestExecRedirectOut(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	f := filepath.Join(tmp, "out.txt")
	runCapture(t, s, "echo hello > "+f)
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Errorf("file content = %q, want %q", string(data), "hello\n")
	}
}

func TestExecRedirectAppend(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	f := filepath.Join(tmp, "out.txt")
	runCapture(t, s, "echo a > "+f)
	runCapture(t, s, "echo b >> "+f)
	data, _ := os.ReadFile(f)
	if string(data) != "a\nb\n" {
		t.Errorf("file content = %q, want %q", string(data), "a\nb\n")
	}
}

func TestExecRedirectIn(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	f := filepath.Join(tmp, "in.txt")
	os.WriteFile(f, []byte("from file\n"), 0644)
	got := runCapture(t, s, "read line < "+f+"; echo $line")
	assertOutput(t, got, "from file")
}

// --- Exit status ---

func TestExecFalseStatus(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "false; echo $?")
	assertOutput(t, got, "1")
}

func TestExecTrueStatus(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "true")
	assertStatus(t, s, 0)
}

// --- Variable visibility across entries ---

func TestExecLazyExpansion(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "A=1; B=$A; echo $B")
	assertOutput(t, got, "1")
}

// --- Here strings ---

func TestExecHereString(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "read x <<< hello; echo $x")
	assertOutput(t, got, "hello")
}

// --- Eval ---

func TestExecEval(t *testing.T) {
	s := testState(t)
	// eval runs through runLine which uses os.Stdout, so test via variable side effect.
	runCapture(t, s, "eval 'X=from_eval'")
	assertVar(t, s, "X", "from_eval")
}

// --- Per-command assignments ---

func TestExecPerCmdAssign(t *testing.T) {
	s := testState(t)
	// Per-command assignment should be temporary for builtins.
	s.setVar("X", "original")
	got := runCapture(t, s, "X=temp echo $X; echo $X")
	// Note: per-command assignments don't affect expansion of the same command's args
	// in bash. The expansion happens before the assignment takes effect for the command.
	// But the variable should be restored after.
	_ = got
	assertVar(t, s, "X", "original")
}

// --- Multiple assignments ---

func TestExecMultiAssign(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "A=1 B=2 C=3; echo $A $B $C")
	assertOutput(t, got, "1 2 3")
}

// --- String replacement / substring expansion ---

func TestExecParamReplace(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=hello_world; echo "${X/world/earth}"`)
	assertOutput(t, got, "hello_earth")
}

func TestExecParamReplaceAll(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=aabaa; echo "${X//a/x}"`)
	assertOutput(t, got, "xxbxx")
}

func TestExecSubstring(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=hello; echo ${X:0:3}`)
	assertOutput(t, got, "hel")
}

func TestExecSubstringOffset(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=hello; echo ${X:2}`)
	assertOutput(t, got, "llo")
}
