package main

import (
	"strings"
	"testing"
)

// --- set -- (positional parameters) ---

func TestSetPositionalParams(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -- a b c; echo $1 $2 $3")
	assertOutput(t, got, "a b c")
}

func TestSetPositionalParamsCount(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -- x y; echo $#")
	assertOutput(t, got, "2")
}

// --- set -e (errexit) ---

func TestSetErrexit(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -e; false; echo no")
	assertOutput(t, got, "")
}

func TestSetErrexitIfCondition(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -e; if false; then echo no; fi; echo yes")
	assertOutput(t, got, "yes")
}

func TestSetErrexitOrLHS(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -e; false || true; echo yes")
	assertOutput(t, got, "yes")
}

func TestSetErrexitAndRHS(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -e; true && false; echo no")
	assertOutput(t, got, "")
}

func TestSetErrexitWhileCondition(t *testing.T) {
	s := testState(t)
	// The while condition should not trigger errexit.
	got := runCapture(t, s, "set -e; while false; do echo no; done; echo yes")
	assertOutput(t, got, "yes")
}

func TestSetErrexitDisable(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -e; set +e; false; echo survived")
	assertOutput(t, got, "survived")
}

// --- set -u (nounset) ---

func TestSetNounset(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, "set -u; echo $UNDEFINED_VAR_XYZ")
	if !strings.Contains(stderr, "unbound variable") {
		t.Errorf("expected 'unbound variable' in stderr, got %q", stderr)
	}
}

func TestSetNounsetDefined(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -u; X=hello; echo $X")
	assertOutput(t, got, "hello")
}

func TestSetNounsetWithErrexit(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -eu; echo $UNDEFINED_VAR_XYZ; echo no")
	assertOutput(t, got, "")
}

func TestSetNounsetCombined(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -eu; X=1; echo $X")
	assertOutput(t, got, "1")
}

// --- set -x (xtrace) ---

func TestSetXtrace(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, "set -x; echo hello")
	if !strings.Contains(stderr, "+ echo hello") {
		t.Errorf("expected xtrace output, got stderr=%q", stderr)
	}
}

func TestSetXtraceDisable(t *testing.T) {
	s := testState(t)
	_, stderr := runCaptureBoth(t, s, "set -x; set +x; echo hello")
	// After +x, the echo should not be traced.
	if strings.Contains(stderr, "+ echo hello") {
		t.Errorf("xtrace should be off, got stderr=%q", stderr)
	}
}

// --- set -o pipefail ---

func TestSetPipefail(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -o pipefail; false | true; echo $?")
	assertOutput(t, got, "1")
}

func TestSetPipefailAllPass(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -o pipefail; true | true; echo $?")
	assertOutput(t, got, "0")
}

func TestSetPipefailDisable(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -o pipefail; set +o pipefail; false | true; echo $?")
	assertOutput(t, got, "0")
}

// --- set -o (list options) ---

func TestSetListOptions(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "set -o")
	if !strings.Contains(got, "errexit") || !strings.Contains(got, "off") {
		t.Errorf("set -o output = %q, expected option listing", got)
	}
}

// --- Combined flags ---

func TestSetCombinedFlags(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "set -eu")
	if !s.optErrexit {
		t.Error("errexit should be set")
	}
	if !s.optNounset {
		t.Error("nounset should be set")
	}
}
