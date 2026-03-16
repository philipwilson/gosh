package main

import (
	"os"
	"strings"
	"testing"
)

// --- readonly ---

func TestReadonlyBasic(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `readonly x=5; echo $x`)
	assertOutput(t, out, "5")
}

func TestReadonlyRejectAssignment(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, `readonly x=5; x=10; echo $x`)
	if !strings.Contains(stderr, "readonly variable") {
		t.Errorf("expected readonly error, got stderr=%q", stderr)
	}
	assertVar(t, s, "x", "5")
}

func TestReadonlyRejectUnset(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, `readonly x=5; unset x`)
	if !strings.Contains(stderr, "readonly variable") {
		t.Errorf("expected readonly error, got stderr=%q", stderr)
	}
	assertVar(t, s, "x", "5")
}

func TestReadonlyList(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `readonly x=5`)
	out := runCapture(t, s, `readonly`)
	if !strings.Contains(out, "x=") {
		t.Errorf("readonly list should contain x, got %q", out)
	}
}

func TestDeclareReadonly(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `declare -r x=5; echo $x`)
	assertOutput(t, out, "5")
	_, stderr := runCaptureBoth(t, s, `x=10`)
	if !strings.Contains(stderr, "readonly variable") {
		t.Errorf("expected readonly error, got stderr=%q", stderr)
	}
}

// --- integer ---

func TestDeclareInteger(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `declare -i x; x=5+3; echo $x`)
	assertOutput(t, out, "8")
}

func TestDeclareIntegerNonNumeric(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `declare -i x; x=hello; echo $x`)
	assertOutput(t, out, "0")
}

func TestDeclareIntegerInitialValue(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `declare -i x=5+3; echo $x`)
	assertOutput(t, out, "8")
}

func TestDeclareIntegerAppend(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `declare -i x=10; x+=5; echo $x`)
	assertOutput(t, out, "15")
}

// --- declare flags ---

func TestDeclareExport(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `declare -x MYVAR=val`)
	if s.attrs["MYVAR"]&attrExport == 0 {
		t.Error("MYVAR should be exported")
	}
}

func TestDeclareClearExport(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `declare -x MYVAR=val; declare +x MYVAR`)
	if s.attrs["MYVAR"]&attrExport != 0 {
		t.Error("MYVAR should not be exported after +x")
	}
}

func TestDeclareClearReadonlyError(t *testing.T) {
	s := testState(t)
	status := builtinDeclare(s, []string{"+r", "x"}, os.Stdin, os.Stdout, os.Stderr)
	if status == 0 {
		t.Error("declare +r should return error")
	}
}

func TestDeclareCombinedFlags(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `declare -ri x=5+3; echo $x`)
	assertOutput(t, out, "8")
	_, stderr := runCaptureBoth(t, s, `x=10`)
	if !strings.Contains(stderr, "readonly variable") {
		t.Errorf("expected readonly error, got stderr=%q", stderr)
	}
}

// --- declare -p ---

func TestDeclarePrintInteger(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `declare -i x=5`)
	out := runCapture(t, s, `declare -p x`)
	if !strings.Contains(out, "-i") || !strings.Contains(out, "x=") {
		t.Errorf("declare -p should show -i flag, got %q", out)
	}
}

func TestDeclarePrintExport(t *testing.T) {
	s := testState(t)
	runCapture(t, s, `export Z=val`)
	out := runCapture(t, s, `declare -p Z`)
	if !strings.Contains(out, "-x") || !strings.Contains(out, "Z=") {
		t.Errorf("declare -p should show -x flag, got %q", out)
	}
}

// --- function-local scoping ---

func TestDeclareFunctionLocal(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `f() { declare x=inner; }; x=outer; f; echo $x`)
	assertOutput(t, out, "outer")
}

func TestDeclareFunctionLocalInteger(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `f() { declare -i x; x=2+3; echo $x; }; f`)
	assertOutput(t, out, "5")
}

// --- typeset alias ---

func TestTypesetAlias(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `typeset -i x=5+3; echo $x`)
	assertOutput(t, out, "8")
}

// --- attribute removal ---

func TestDeclareRemoveInteger(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `declare -i x=5; declare +i x; x=hello; echo $x`)
	assertOutput(t, out, "hello")
}

// --- readonly in function scope ---

func TestReadonlyFunctionScope(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `x=5; f() { declare -r x=10; }; f; x=20; echo $x`)
	assertOutput(t, out, "20")
}

// --- integer for loop counter ---

func TestIntegerForLoop(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `declare -i n=0; for x in 1 2 3; do n+=1; done; echo $n`)
	assertOutput(t, out, "3")
}
