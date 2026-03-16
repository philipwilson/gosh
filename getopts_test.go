package main

import (
	"testing"
)

func TestGetoptsBasicOption(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-a"}
	out := runCapture(t, s, `getopts "abc" opt; echo $opt`)
	assertOutput(t, out, "a")
	assertStatus(t, s, 0)
}

func TestGetoptsOptionWithArgSameArg(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-avalue"}
	runCapture(t, s, `getopts "a:" opt`)
	assertVar(t, s, "opt", "a")
	assertVar(t, s, "OPTARG", "value")
}

func TestGetoptsOptionWithArgNextArg(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-a", "value"}
	runCapture(t, s, `getopts "a:" opt`)
	assertVar(t, s, "opt", "a")
	assertVar(t, s, "OPTARG", "value")
	assertVar(t, s, "OPTIND", "3")
}

func TestGetoptsBundledOptions(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-abc"}

	runCapture(t, s, `getopts "abc" opt`)
	assertVar(t, s, "opt", "a")

	runCapture(t, s, `getopts "abc" opt`)
	assertVar(t, s, "opt", "b")

	runCapture(t, s, `getopts "abc" opt`)
	assertVar(t, s, "opt", "c")
	// After consuming all chars, OPTIND should advance.
	assertVar(t, s, "OPTIND", "2")

	// Fourth call: no more options.
	runCapture(t, s, `getopts "abc" opt`)
	assertStatus(t, s, 1)
}

func TestGetoptsBundledWithArgValue(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-abval"}

	runCapture(t, s, `getopts "ab:" opt`)
	assertVar(t, s, "opt", "a")

	runCapture(t, s, `getopts "ab:" opt`)
	assertVar(t, s, "opt", "b")
	assertVar(t, s, "OPTARG", "val")
}

func TestGetoptsSilentUnknownOption(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-x"}
	_, stderr := runCaptureBoth(t, s, `getopts ":a:" opt`)
	assertVar(t, s, "opt", "?")
	assertVar(t, s, "OPTARG", "x")
	if stderr != "" {
		t.Errorf("expected no stderr in silent mode, got %q", stderr)
	}
}

func TestGetoptsSilentMissingArg(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-a"}
	_, stderr := runCaptureBoth(t, s, `getopts ":a:" opt`)
	assertVar(t, s, "opt", ":")
	assertVar(t, s, "OPTARG", "a")
	if stderr != "" {
		t.Errorf("expected no stderr in silent mode, got %q", stderr)
	}
}

func TestGetoptsNormalUnknownOption(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-x"}
	_, stderr := runCaptureBoth(t, s, `getopts "a:" opt`)
	assertVar(t, s, "opt", "?")
	if stderr == "" {
		t.Error("expected stderr message for unknown option")
	}
}

func TestGetoptsNormalMissingArg(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-a"}
	_, stderr := runCaptureBoth(t, s, `getopts "a:" opt`)
	assertVar(t, s, "opt", "?")
	if stderr == "" {
		t.Error("expected stderr message for missing argument")
	}
}

func TestGetoptsDoubleDash(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"--", "-a"}
	runCapture(t, s, `getopts "a" opt`)
	assertVar(t, s, "opt", "?")
	assertStatus(t, s, 1)
	// OPTIND should advance past --.
	assertVar(t, s, "OPTIND", "2")
}

func TestGetoptsNonOptionStops(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"foo", "-a"}
	runCapture(t, s, `getopts "a" opt`)
	assertVar(t, s, "opt", "?")
	assertStatus(t, s, 1)
}

func TestGetoptsCustomArgs(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `getopts "a" opt -a; echo $opt`)
	assertOutput(t, out, "a")
}

func TestGetoptsOptindReset(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-a", "-b"}

	runCapture(t, s, `getopts "ab" opt`)
	assertVar(t, s, "opt", "a")

	// Reset OPTIND to re-parse.
	s.setVar("OPTIND", "1")
	runCapture(t, s, `getopts "ab" opt`)
	assertVar(t, s, "opt", "a")
}

func TestGetoptsWhileLoop(t *testing.T) {
	s := testState(t)
	out := runCapture(t, s, `while getopts "a:b" opt -a hello -b; do echo "opt=$opt OPTARG=$OPTARG"; done`)
	assertOutput(t, out, "opt=a OPTARG=hello\nopt=b OPTARG=")
}

func TestGetoptsExhaustedReturnsOne(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"-a"}
	runCapture(t, s, `getopts "a" opt`)
	assertStatus(t, s, 0)
	runCapture(t, s, `getopts "a" opt`)
	assertStatus(t, s, 1)
}
