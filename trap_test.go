package main

import (
	"testing"
)

func TestTrapERR(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "trap 'echo caught' ERR; false")
	assertOutput(t, got, "caught")
}

func TestTrapERRNotOnSuccess(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "trap 'echo caught' ERR; true")
	assertOutput(t, got, "")
}

func TestTrapRETURN(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "trap 'echo ret' RETURN; f() { true; }; f")
	assertOutput(t, got, "ret")
}

func TestTrapRemove(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "trap 'echo caught' ERR; trap - ERR; false")
	assertOutput(t, got, "")
}

func TestTrapList(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "trap 'echo hi' ERR; trap")
	assertOutput(t, got, `trap -- "echo hi" ERR`)
}

func TestTrapIgnore(t *testing.T) {
	s := testState(t)
	// Empty command = ignore; ERR trap should not fire.
	got := runCapture(t, s, "trap '' ERR; false; echo ok")
	assertOutput(t, got, "ok")
}

func TestTrapSubshellIsolation(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "trap 'echo outer' ERR; (trap 'echo inner' ERR; false); false")
	assertOutput(t, got, "inner\nouter")
}

func TestTrapMultipleERR(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "trap 'echo err' ERR; false; false")
	assertOutput(t, got, "err\nerr")
}
