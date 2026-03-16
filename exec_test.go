package main

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// --- Colon vs non-colon parameter expansion operators ---

func TestParamDefaultColonEmpty(t *testing.T) {
	// ${X:-default} should return "default" when X is empty.
	s := testState(t)
	got := runCapture(t, s, `X=""; echo "${X:-default}"`)
	assertOutput(t, got, "default")
}

func TestParamDefaultNonColonEmpty(t *testing.T) {
	// ${X-default} should return "" when X is set but empty.
	s := testState(t)
	got := runCapture(t, s, `X=""; echo "[${X-default}]"`)
	assertOutput(t, got, "[]")
}

func TestParamDefaultNonColonUnset(t *testing.T) {
	// ${X-default} should return "default" when X is unset.
	s := testState(t)
	got := runCapture(t, s, `echo "[${UNSET_XYZ-default}]"`)
	assertOutput(t, got, "[default]")
}

func TestParamAltColonEmpty(t *testing.T) {
	// ${X:+alt} should return "" when X is empty.
	s := testState(t)
	got := runCapture(t, s, `X=""; echo "[${X:+alt}]"`)
	assertOutput(t, got, "[]")
}

func TestParamAltNonColonEmpty(t *testing.T) {
	// ${X+alt} should return "alt" when X is set but empty.
	s := testState(t)
	got := runCapture(t, s, `X=""; echo "[${X+alt}]"`)
	assertOutput(t, got, "[alt]")
}

func TestParamAltNonColonUnset(t *testing.T) {
	// ${X+alt} should return "" when X is unset.
	s := testState(t)
	got := runCapture(t, s, `echo "[${UNSET_XYZ+alt}]"`)
	assertOutput(t, got, "[]")
}

// --- $! variable ---

func TestBangVar(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `true & echo $!`)
	// Should output a number (the PID).
	got = strings.TrimSpace(got)
	if got == "" || got == "0" {
		t.Errorf("$! should be a non-zero PID, got %q", got)
	}
}

// --- wait builtin ---

func TestWaitAll(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `true & true & wait`)
	assertStatus(t, s, 0)
}

func TestWaitPid(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `sh -c 'exit 42' & wait $!; echo $?`)
	assertOutput(t, got, "42")
}

func TestWaitInvalid(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, `wait %99`)
	assertStatus(t, s, 127)
	if !strings.Contains(stderr, "no such job") {
		t.Errorf("expected 'no such job' error, got %q", stderr)
	}
}

// --- exec builtin ---

func TestExecNoOp(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `exec; echo ok`)
	assertOutput(t, got, "ok")
}

func TestExecNotFound(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, `exec nonexistent_xyz_cmd_12345`)
	assertStatus(t, s, 127)
	if !strings.Contains(stderr, "not found") {
		t.Errorf("expected 'not found' error, got %q", stderr)
	}
}

func TestExecBuiltinRedirectOut(t *testing.T) {
	// exec > file permanently redirects the shell's stdout via dup2.
	// This can't be tested via runCapture (which captures via a pipe),
	// so we test via a subprocess.
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "out.txt")
	script := filepath.Join(tmpDir, "test.sh")
	if err := os.WriteFile(script, []byte("exec > "+tmpFile+"\necho hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use go run to invoke gosh with the script.
	proc := goRun(t, ".", script)
	proc.Wait()

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("expected output file to contain 'hello', got %q", string(data))
	}
}

// goRun starts "go run . args..." as a subprocess and returns the process.
func goRun(t *testing.T, dir string, args ...string) *os.Process {
	t.Helper()
	goPath, err := osexec.LookPath("go")
	if err != nil {
		t.Skipf("go not found in PATH")
	}
	cmdArgs := append([]string{"go", "run", "."}, args...)
	proc, err := os.StartProcess(goPath, cmdArgs, &os.ProcAttr{
		Dir:   dir,
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		t.Fatalf("starting go run: %v", err)
	}
	return proc
}

// --- Process substitution ---

func TestProcSubstCat(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `cat <(echo hello)`)
	assertOutput(t, got, "hello")
}

func TestProcSubstDiff(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `diff <(echo a) <(echo b)`)
	if got == "" {
		t.Error("expected non-empty diff output")
	}
}

func TestProcSubstNested(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `cat <(echo $(echo nested))`)
	assertOutput(t, got, "nested")
}

// --- Until loop ---

func TestUntilBasic(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=0; until [ $X -eq 3 ]; do X=$((X+1)); done; echo $X`)
	assertOutput(t, got, "3")
}

func TestUntilFalseBody(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `until true; do echo no; done; echo done`)
	assertOutput(t, got, "done")
}

func TestUntilBreak(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `until false; do break; done; echo ok`)
	assertOutput(t, got, "ok")
}

// --- $RANDOM ---

func TestRandom(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `echo $RANDOM`)
	n, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("$RANDOM not a number: %q", got)
	}
	if n < 0 || n >= 32768 {
		t.Errorf("$RANDOM = %d, want 0-32767", n)
	}
}

func TestRandomDiffers(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `A=$RANDOM; B=$RANDOM; test "$A" != "$B" && echo diff`)
	assertOutput(t, got, "diff")
}

// --- $SECONDS ---

func TestSeconds(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `echo $SECONDS`)
	n, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("$SECONDS not a number: %q", got)
	}
	if n < 0 {
		t.Errorf("$SECONDS = %d, want >= 0", n)
	}
}

func TestSecondsReset(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `SECONDS=0; echo $SECONDS`)
	assertOutput(t, got, "0")
}

// --- BASH_REMATCH ---

func TestRematchFull(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `RE='([a-z]+)([0-9]+)'; [[ "hello123" =~ $RE ]]; echo ${BASH_REMATCH[0]}`)
	assertOutput(t, got, "hello123")
}

func TestRematchGroup1(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `RE='([a-z]+)([0-9]+)'; [[ "hello123" =~ $RE ]]; echo ${BASH_REMATCH[1]}`)
	assertOutput(t, got, "hello")
}

func TestRematchGroup2(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `RE='([a-z]+)([0-9]+)'; [[ "hello123" =~ $RE ]]; echo ${BASH_REMATCH[2]}`)
	assertOutput(t, got, "123")
}

func TestRematchCount(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `RE='(.)(.)(.)'; [[ "abc" =~ $RE ]]; echo ${#BASH_REMATCH[@]}`)
	assertOutput(t, got, "4")
}

func TestRematchNoMatch(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `[[ "abc" =~ [0-9]+ ]]; echo ${#BASH_REMATCH[@]}`)
	assertOutput(t, got, "0")
}

// --- let builtin ---

func TestLetAssign(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "let x=5; echo $x")
	assertOutput(t, got, "5")
}

func TestLetMultiple(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "let a=2 b=3; echo $a $b")
	assertOutput(t, got, "2 3")
}

func TestLetExitTrue(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "let 1")
	assertStatus(t, s, 0)
}

func TestLetExitFalse(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "let 0")
	assertStatus(t, s, 1)
}

func TestLetExpr(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "let 'x=2+3'; echo $x")
	assertOutput(t, got, "5")
}

// --- Case conversion expansions ---

func TestCaseUpperAll(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=hello; echo ${X^^}`)
	assertOutput(t, got, "HELLO")
}

func TestCaseLowerAll(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=HELLO; echo ${X,,}`)
	assertOutput(t, got, "hello")
}

func TestCaseUpperFirst(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=hello; echo ${X^}`)
	assertOutput(t, got, "Hello")
}

func TestCaseLowerFirst(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=Hello; echo ${X,}`)
	assertOutput(t, got, "hello")
}

func TestCaseUpperPattern(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=foobar; echo ${X^^[fo]}`)
	assertOutput(t, got, "FOObar")
}

func TestCaseLowerPattern(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=FOOBAR; echo ${X,,[FO]}`)
	assertOutput(t, got, "fooBAR")
}

func TestCaseEmpty(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=; echo "x${X^^}x"`)
	assertOutput(t, got, "xx")
}

// --- select loop ---

func TestSelectBasic(t *testing.T) {
	s := testState(t)
	got := runCaptureWithStdin(t, s, `select x in a b c; do echo $x; break; done`, "1\n")
	assertOutput(t, got, "a")
}

func TestSelectReply(t *testing.T) {
	s := testState(t)
	got := runCaptureWithStdin(t, s, `select x in a b c; do echo $REPLY; break; done`, "2\n")
	assertOutput(t, got, "2")
}

func TestSelectInvalid(t *testing.T) {
	s := testState(t)
	got := runCaptureWithStdin(t, s, `select x in a b; do if [ -n "$x" ]; then echo $x; break; fi; done`, "99\n1\n")
	assertOutput(t, got, "a")
}

func TestSelectEOF(t *testing.T) {
	s := testState(t)
	got := runCaptureWithStdin(t, s, `select x in a; do echo $x; done; echo done`, "")
	assertOutput(t, got, "done")
}

// --- gosh -c ---

func buildGosh(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gosh")
	cmd := osexec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func TestDashCSimple(t *testing.T) {
	bin := buildGosh(t)
	out, err := osexec.Command(bin, "-c", "echo hello").Output()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestDashCVar(t *testing.T) {
	bin := buildGosh(t)
	out, err := osexec.Command(bin, "-c", "X=42; echo $X").Output()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "42" {
		t.Fatalf("got %q, want %q", got, "42")
	}
}

func TestDashCArgs(t *testing.T) {
	bin := buildGosh(t)
	out, err := osexec.Command(bin, "-c", "echo $1", "_", "foo").Output()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "foo" {
		t.Fatalf("got %q, want %q", got, "foo")
	}
}

func TestDashCArg0(t *testing.T) {
	bin := buildGosh(t)
	out, err := osexec.Command(bin, "-c", "echo $0", "myname").Output()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "myname" {
		t.Fatalf("got %q, want %q", got, "myname")
	}
}

func TestDashCExitStatus(t *testing.T) {
	bin := buildGosh(t)
	err := osexec.Command(bin, "-c", "false").Run()
	if err == nil {
		t.Fatal("expected non-zero exit status")
	}
	if exitErr, ok := err.(*osexec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Fatalf("got exit code %d, want 1", exitErr.ExitCode())
		}
	} else {
		t.Fatalf("unexpected error type: %v", err)
	}
}

func TestDashCNoArg(t *testing.T) {
	bin := buildGosh(t)
	err := osexec.Command(bin, "-c").Run()
	if err == nil {
		t.Fatal("expected non-zero exit status")
	}
	if exitErr, ok := err.(*osexec.ExitError); ok {
		if exitErr.ExitCode() != 2 {
			t.Fatalf("got exit code %d, want 2", exitErr.ExitCode())
		}
	} else {
		t.Fatalf("unexpected error type: %v", err)
	}
}

// --- Compound commands in pipelines ---

func TestForInPipeline(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `for x in c b a; do echo $x; done | sort`)
	assertOutput(t, got, "a\nb\nc")
}

func TestIfInPipeline(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `if true; then echo yes; else echo no; fi | tr a-z A-Z`)
	assertOutput(t, got, "YES")
}

func TestCaseInPipeline(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `case foo in foo) echo matched;; esac | tr a-z A-Z`)
	assertOutput(t, got, "MATCHED")
}

func TestSubshellInPipeline(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `(echo hello) | tr a-z A-Z`)
	assertOutput(t, got, "HELLO")
}

func TestCompoundIsolation(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=before; for x in a; do X=after; echo $x; done | cat; echo $X`)
	assertOutput(t, got, "a\nbefore")
}

func TestCompoundMidPipeline(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `echo hello | (cat) | tr a-z A-Z`)
	assertOutput(t, got, "HELLO")
}

func TestWhileInPipeline(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `i=0; while [ $i -lt 3 ]; do echo $i; i=$((i+1)); done | sort -r`)
	assertOutput(t, got, "2\n1\n0")
}

func TestPipeIntoWhileRead(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `printf "a\nb\nc\n" | while read x; do echo "[$x]"; done`)
	assertOutput(t, got, "[a]\n[b]\n[c]")
}

func TestWhileReadLineContinuation(t *testing.T) {
	s := testState(t)
	got := runCaptureWithStdin(t, s, `while read line; do echo "got $line"; done`, "hello\nworld\n")
	assertOutput(t, got, "got hello\ngot world")
}

// --- Compound command redirects ---

func TestWhileReadFromFile(t *testing.T) {
	s := testState(t)
	tmp := filepath.Join(t.TempDir(), "input.txt")
	os.WriteFile(tmp, []byte("a\nb\n"), 0644)
	got := runCapture(t, s, `while read line; do echo "[$line]"; done < `+tmp)
	assertOutput(t, got, "[a]\n[b]")
}

func TestForRedirectOut(t *testing.T) {
	s := testState(t)
	tmp := filepath.Join(t.TempDir(), "out.txt")
	got := runCapture(t, s, `for x in a b c; do echo $x; done > `+tmp+`; cat `+tmp)
	assertOutput(t, got, "a\nb\nc")
}

func TestIfRedirectOut(t *testing.T) {
	s := testState(t)
	tmp := filepath.Join(t.TempDir(), "out.txt")
	got := runCapture(t, s, `if true; then echo yes; fi > `+tmp+`; cat `+tmp)
	assertOutput(t, got, "yes")
}

func TestCaseRedirectOut(t *testing.T) {
	s := testState(t)
	tmp := filepath.Join(t.TempDir(), "out.txt")
	got := runCapture(t, s, `case foo in foo) echo matched;; esac > `+tmp+`; cat `+tmp)
	assertOutput(t, got, "matched")
}

func TestSubshellRedirectOut(t *testing.T) {
	s := testState(t)
	tmp := filepath.Join(t.TempDir(), "out.txt")
	got := runCapture(t, s, `(echo hello) > `+tmp+`; cat `+tmp)
	assertOutput(t, got, "hello")
}

func TestCompoundHerestring(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `while read line; do echo "[$line]"; done <<< "hello world"`)
	assertOutput(t, got, "[hello world]")
}

func TestUntilRedirectOut(t *testing.T) {
	s := testState(t)
	tmp := filepath.Join(t.TempDir(), "out.txt")
	got := runCapture(t, s, `X=0; until test $X -eq 3; do echo $X; X=$((X+1)); done > `+tmp+`; cat `+tmp)
	assertOutput(t, got, "0\n1\n2")
}

func TestCompoundRedirInPipeline(t *testing.T) {
	s := testState(t)
	tmp := filepath.Join(t.TempDir(), "out.txt")
	got := runCapture(t, s, `for x in c b a; do echo $x; done | sort > `+tmp+`; cat `+tmp)
	assertOutput(t, got, "a\nb\nc")
}

// --- Compound stderr threading tests ---

func TestCompoundStderrCapture(t *testing.T) {
	s := testState(t)
	tmp := filepath.Join(t.TempDir(), "out.txt")
	got := runCapture(t, s, `for x in a b; do echo $x; echo err >&2; done > `+tmp+` 2>&1; cat `+tmp)
	assertOutput(t, got, "a\nerr\nb\nerr")
}

func TestIfStderrCapture(t *testing.T) {
	s := testState(t)
	got, _ := runCaptureBoth(t, s, `if true; then echo err >&2; fi 2>&1`)
	assertOutput(t, got, "err")
}

func TestSubshellStderrCapture(t *testing.T) {
	s := testState(t)
	got, _ := runCaptureBoth(t, s, `(echo err >&2) 2>&1`)
	assertOutput(t, got, "err")
}

func TestCompoundStderrSuppressed(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, `for x in a; do echo err >&2; done 2>/dev/null`)
	if stderr != "" {
		t.Errorf("expected empty stderr, got %q", stderr)
	}
}

func TestFunctionStderrRedirect(t *testing.T) {
	s := testState(t)
	got, _ := runCaptureBoth(t, s, `f() { echo err >&2; }; f 2>&1`)
	assertOutput(t, got, "err")
}

// --- [[ -v var ]] ---

func TestDblBracketVarSetScalar(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=hello; [[ -v X ]] && echo yes || echo no`)
	assertOutput(t, got, "yes")
}

func TestDblBracketVarUnset(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `unset X; [[ -v X ]] && echo yes || echo no`)
	assertOutput(t, got, "no")
}

func TestDblBracketVarEmpty(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `X=""; [[ -v X ]] && echo yes || echo no`)
	assertOutput(t, got, "yes")
}

func TestDblBracketVarArray(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `arr=(a b); [[ -v arr ]] && echo yes || echo no`)
	assertOutput(t, got, "yes")
}

// --- ${!var} indirect expansion ---

func TestIndirectExpansion(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `REF=X; X=42; echo ${!REF}`)
	assertOutput(t, got, "42")
}

func TestIndirectExpansionUnset(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `echo "x${!UNSET}y"`)
	assertOutput(t, got, "xy")
}

// --- &> and &>> redirects ---

func TestAndGtRedirect(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	got := runCapture(t, s, `echo hello &>`+tmp+`/out; cat `+tmp+`/out`)
	assertOutput(t, got, "hello")
}

func TestAndGtCapturesBothStreams(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	got := runCapture(t, s, `(echo out; echo err >&2) &>`+tmp+`/out; cat `+tmp+`/out`)
	// Both stdout and stderr should be in the file.
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Errorf("&> should capture both streams, got %q", got)
	}
}

func TestAndAppendRedirect(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	got := runCapture(t, s, `echo hello &>>`+tmp+`/out; echo world &>>`+tmp+`/out; cat `+tmp+`/out`)
	assertOutput(t, got, "hello\nworld")
}

// --- IFS handling ---

func TestIFSEmptyNoSplitting(t *testing.T) {
	// IFS="" should disable word splitting entirely.
	s := testState(t)
	s.vars["IFS"] = ""
	s.setVar("x", "a b c")
	got := runCapture(t, s, `echo $x`)
	assertOutput(t, got, "a b c")
}

func TestIFSUnsetDefaultSplitting(t *testing.T) {
	// IFS unset should use default splitting (space/tab/newline).
	s := testState(t)
	delete(s.vars, "IFS")
	s.setVar("x", "a b c")
	got := runCapture(t, s, `echo $x`)
	// Default IFS splits on spaces; echo re-joins with single spaces.
	assertOutput(t, got, "a b c")
}

func TestIFSEmptyPreservesSpaces(t *testing.T) {
	// IFS="" should keep multiple spaces intact in expansions.
	s := testState(t)
	s.vars["IFS"] = ""
	s.setVar("x", "a  b  c")
	// With no splitting, $x stays as one word with internal spaces.
	got := runCapture(t, s, `printf '%s\n' $x`)
	assertOutput(t, got, "a  b  c")
}

func TestIFSEmptySetArgs(t *testing.T) {
	// IFS="" with set -- $x should produce one argument.
	s := testState(t)
	got := runCapture(t, s, `x="a b c"; IFS=""; set -- $x; echo $#`)
	assertOutput(t, got, "1")
}

func TestIFSUnsetSetArgs(t *testing.T) {
	// Unset IFS with set -- $x should produce three arguments.
	s := testState(t)
	got := runCapture(t, s, `x="a b c"; unset IFS; set -- $x; echo $#`)
	assertOutput(t, got, "3")
}

func TestIFSEmptyRead(t *testing.T) {
	// read with IFS="" should not split.
	s := testState(t)
	s.vars["IFS"] = ""
	got := runCaptureWithStdin(t, s, "read a b; echo \"a=$a\" \"b=$b\"", "hello world\n")
	assertOutput(t, got, "a=hello world b=")
}

func TestIFSUnsetRead(t *testing.T) {
	// read with IFS unset should split on default whitespace.
	s := testState(t)
	delete(s.vars, "IFS")
	got := runCaptureWithStdin(t, s, "read a b; echo \"a=$a\" \"b=$b\"", "hello world\n")
	assertOutput(t, got, "a=hello b=world")
}

// --- $'...' ANSI-C quoting ---

func TestAnsiCQuoteEcho(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `echo $'\t'`)
	assertOutput(t, got, "\t")
}

func TestAnsiCQuoteHexEcho(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `printf '%s\n' $'\x41\x42\x43'`)
	assertOutput(t, got, "ABC")
}

func TestAnsiCQuoteInVariable(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `x=$'\n'; printf 'a%sb' "$x"`)
	assertOutput(t, got, "a\nb")
}

// --- Scanner buffer (>64KB lines) ---

func TestLongLineScript(t *testing.T) {
	// Test that lines >64KB work in scripts (scanner buffer fix).
	s := testState(t)
	tmp := t.TempDir()
	longStr := strings.Repeat("a", 70000)
	script := filepath.Join(tmp, "long.sh")
	outFile := filepath.Join(tmp, "out.txt")
	os.WriteFile(script, []byte("echo "+longStr+" > "+outFile+"\n"), 0644)
	runCapture(t, s, "source "+script)
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != longStr {
		t.Errorf("long line: got %d chars, want %d", len(got), len(longStr))
	}
}
