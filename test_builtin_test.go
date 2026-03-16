package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --- evalTest direct unit tests ---

func TestEvalTestEmpty(t *testing.T) {
	result, err := evalTest(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result {
		t.Error("evalTest(nil) should be false")
	}
}

func TestEvalTestZEmpty(t *testing.T) {
	result, _ := evalTest([]string{"-z", ""})
	if !result {
		t.Error(`-z "" should be true`)
	}
}

func TestEvalTestZNonEmpty(t *testing.T) {
	result, _ := evalTest([]string{"-z", "x"})
	if result {
		t.Error(`-z "x" should be false`)
	}
}

func TestEvalTestNNonEmpty(t *testing.T) {
	result, _ := evalTest([]string{"-n", "x"})
	if !result {
		t.Error(`-n "x" should be true`)
	}
}

func TestEvalTestNEmpty(t *testing.T) {
	result, _ := evalTest([]string{"-n", ""})
	if result {
		t.Error(`-n "" should be false`)
	}
}

func TestEvalTestStringEqual(t *testing.T) {
	result, _ := evalTest([]string{"abc", "=", "abc"})
	if !result {
		t.Error(`"abc" = "abc" should be true`)
	}
}

func TestEvalTestStringNotEqual(t *testing.T) {
	result, _ := evalTest([]string{"a", "!=", "b"})
	if !result {
		t.Error(`"a" != "b" should be true`)
	}
}

func TestEvalTestIntEq(t *testing.T) {
	result, _ := evalTest([]string{"5", "-eq", "5"})
	if !result {
		t.Error("5 -eq 5 should be true")
	}
}

func TestEvalTestIntNe(t *testing.T) {
	result, _ := evalTest([]string{"3", "-ne", "5"})
	if !result {
		t.Error("3 -ne 5 should be true")
	}
}

func TestEvalTestIntLt(t *testing.T) {
	result, _ := evalTest([]string{"2", "-lt", "5"})
	if !result {
		t.Error("2 -lt 5 should be true")
	}
}

func TestEvalTestIntLe(t *testing.T) {
	result, _ := evalTest([]string{"5", "-le", "5"})
	if !result {
		t.Error("5 -le 5 should be true")
	}
}

func TestEvalTestIntGt(t *testing.T) {
	result, _ := evalTest([]string{"7", "-gt", "3"})
	if !result {
		t.Error("7 -gt 3 should be true")
	}
}

func TestEvalTestIntGe(t *testing.T) {
	result, _ := evalTest([]string{"5", "-ge", "5"})
	if !result {
		t.Error("5 -ge 5 should be true")
	}
}

func TestEvalTestIntLtFalse(t *testing.T) {
	result, _ := evalTest([]string{"5", "-lt", "3"})
	if result {
		t.Error("5 -lt 3 should be false")
	}
}

func TestEvalTestFileExists(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "exists.txt")
	os.WriteFile(f, []byte("hi"), 0644)

	result, _ := evalTest([]string{"-e", f})
	if !result {
		t.Error("-e should be true for existing file")
	}
}

func TestEvalTestFileNotExists(t *testing.T) {
	result, _ := evalTest([]string{"-e", "/no/such/path/ever"})
	if result {
		t.Error("-e should be false for nonexistent file")
	}
}

func TestEvalTestFileRegular(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "regular.txt")
	os.WriteFile(f, []byte("hi"), 0644)

	result, _ := evalTest([]string{"-f", f})
	if !result {
		t.Error("-f should be true for regular file")
	}
}

func TestEvalTestFileDir(t *testing.T) {
	tmp := t.TempDir()
	result, _ := evalTest([]string{"-d", tmp})
	if !result {
		t.Error("-d should be true for directory")
	}
}

func TestEvalTestFileSize(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "nonempty.txt")
	os.WriteFile(f, []byte("data"), 0644)

	result, _ := evalTest([]string{"-s", f})
	if !result {
		t.Error("-s should be true for non-empty file")
	}

	empty := filepath.Join(tmp, "empty.txt")
	os.WriteFile(empty, nil, 0644)
	result, _ = evalTest([]string{"-s", empty})
	if result {
		t.Error("-s should be false for empty file")
	}
}

func TestEvalTestNot(t *testing.T) {
	result, _ := evalTest([]string{"!", "-z", "hello"})
	if !result {
		t.Error(`! -z "hello" should be true`)
	}
}

func TestEvalTestAndOp(t *testing.T) {
	result, _ := evalTest([]string{"-n", "a", "-a", "-n", "b"})
	if !result {
		t.Error(`-n "a" -a -n "b" should be true`)
	}
}

func TestEvalTestOrOp(t *testing.T) {
	result, _ := evalTest([]string{"-z", "a", "-o", "-n", "b"})
	if !result {
		t.Error(`-z "a" -o -n "b" should be true`)
	}
}

func TestEvalTestParens(t *testing.T) {
	result, _ := evalTest([]string{"(", "-z", "", ")"})
	if !result {
		t.Error(`( -z "" ) should be true`)
	}
}

func TestEvalTestNonIntegerError(t *testing.T) {
	_, err := evalTest([]string{"abc", "-eq", "5"})
	if err == nil {
		t.Error("non-integer in -eq should error")
	}
}

func TestEvalTestExtraArgsError(t *testing.T) {
	_, err := evalTest([]string{"a", "b", "c", "d"})
	if err == nil {
		t.Error("extra arguments should error")
	}
}

func TestEvalTestBareString(t *testing.T) {
	result, _ := evalTest([]string{"hello"})
	if !result {
		t.Error("bare non-empty string should be true")
	}
	result, _ = evalTest([]string{""})
	if result {
		t.Error("bare empty string should be false")
	}
}

// --- builtinBracket ---

func TestBuiltinBracketMissingClose(t *testing.T) {
	s := testState(t)
	status := builtinBracket(s, []string{"-n", "hello"}, os.Stdin, os.Stdout, os.Stderr)
	if status != 2 {
		t.Errorf("[ without ] should return 2, got %d", status)
	}
}

func TestBuiltinBracketWorks(t *testing.T) {
	s := testState(t)
	status := builtinBracket(s, []string{"-n", "hello", "]"}, os.Stdin, os.Stdout, os.Stderr)
	if status != 0 {
		t.Errorf("[ -n hello ] should return 0, got %d", status)
	}
}
