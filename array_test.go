package main

import (
	"testing"
)

func TestArrayBasicAccess(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(a b c); echo ${arr[0]} ${arr[1]} ${arr[2]}")
	assertOutput(t, got, "a b c")
}

func TestArrayAll(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(x y z); echo ${arr[@]}")
	assertOutput(t, got, "x y z")
}

func TestArrayStar(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(x y z); echo ${arr[*]}")
	assertOutput(t, got, "x y z")
}

func TestArrayLength(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(a b c); echo ${#arr[@]}")
	assertOutput(t, got, "3")
}

func TestArrayAppend(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(a); arr+=(b c); echo ${arr[@]}")
	assertOutput(t, got, "a b c")
}

func TestArrayIndexedAssign(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(a b c); arr[2]=hello; echo ${arr[2]}")
	assertOutput(t, got, "hello")
}

func TestArrayBareName(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(first second); echo $arr")
	assertOutput(t, got, "first")
}

func TestArrayUnsetElement(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(a b c); unset 'arr[1]'; echo ${arr[@]}")
	assertOutput(t, got, "a c")
}

func TestArrayUnsetElementLength(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(a b c); unset 'arr[1]'; echo ${#arr[@]}")
	assertOutput(t, got, "2")
}

func TestArrayVariableSubscript(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(a b c); i=1; echo ${arr[$i]}")
	assertOutput(t, got, "b")
}

func TestArrayForLoop(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(x y z); for e in ${arr[@]}; do echo $e; done")
	assertOutput(t, got, "x\ny\nz")
}

func TestArrayReadA(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "echo 'a b c' | read -a arr; echo not-this")
	// Note: read in a pipeline runs in a subshell in bash, but gosh
	// runs builtins in pipelines as external. Let's test via redirect instead.
	_ = got

	// Use here string approach.
	got = runCapture(t, s, "read -a arr <<< 'a b c'; echo ${arr[0]} ${arr[1]} ${arr[2]}")
	assertOutput(t, got, "a b c")
}

func TestArrayLocalA(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "f() { local -a myarr; myarr=(x y); echo ${myarr[@]}; }; f; echo ${myarr[@]}")
	// After function returns, myarr should be gone (empty).
	assertOutput(t, got, "x y")
}

func TestArrayArithmetic(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(10 20 30); echo $(( arr[0] + arr[2] ))")
	assertOutput(t, got, "40")
}

func TestArrayArithAssign(t *testing.T) {
	s := testState(t)
	runCapture(t, s, "arr=(1 2 3); (( arr[1] = 99 ))")
	assertVar(t, s, "arr[1]", "99")
}

func TestArrayEmpty(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(); echo ${#arr[@]}")
	assertOutput(t, got, "0")
}

func TestArrayDefaultValue(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, "arr=(a); echo ${arr[99]:-fallback}")
	assertOutput(t, got, "fallback")
}

// --- "${arr[@]}" producing separate words ---

func TestArrayAtQuotedSeparateWords(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `arr=("a b" c); printf '%s\n' "${arr[@]}"`)
	assertOutput(t, got, "a b\nc")
}

func TestArrayStarQuotedSingleWord(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `arr=(a b); printf '%s\n' "${arr[*]}"`)
	assertOutput(t, got, "a b")
}

func TestPositionalAtQuotedSeparateWords(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `f() { printf '%s\n' "$@"; }; f x y z`)
	assertOutput(t, got, "x\ny\nz")
}

func TestArrayAtQuotedEmpty(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `arr=(); echo "before" "${arr[@]}" "after"`)
	assertOutput(t, got, "before after")
}

func TestArrayAtQuotedForLoop(t *testing.T) {
	s := testState(t)
	got := runCapture(t, s, `arr=("hello world" foo); for x in "${arr[@]}"; do echo "[$x]"; done`)
	assertOutput(t, got, "[hello world]\n[foo]")
}
