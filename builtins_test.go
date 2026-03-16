package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureBuiltin runs a builtin function with a pipe for stdout and returns output.
func captureBuiltin(t *testing.T, fn builtinFunc, state *shellState, args []string) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	status := fn(state, args, os.Stdin, w, os.Stderr)
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	return strings.TrimRight(string(out), "\n"), status
}

// --- echo ---

func TestBuiltinEchoBasic(t *testing.T) {
	s := testState(t)
	out, st := captureBuiltin(t, builtinEcho, s, []string{"hello", "world"})
	if out != "hello world" || st != 0 {
		t.Errorf("echo = %q (status %d)", out, st)
	}
}

func TestBuiltinEchoN(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	builtinEcho(s, []string{"-n", "hi"}, os.Stdin, w, os.Stderr)
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	if string(out) != "hi" {
		t.Errorf("echo -n = %q, want %q", string(out), "hi")
	}
}

func TestBuiltinEchoEmpty(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	builtinEcho(s, nil, os.Stdin, w, os.Stderr)
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	if string(out) != "\n" {
		t.Errorf("echo (no args) = %q, want newline", string(out))
	}
}

// --- cd/pwd ---

func TestBuiltinCdAndPwd(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	// Resolve symlinks (macOS /var → /private/var).
	tmp, _ = filepath.EvalSymlinks(tmp)

	// Save and restore cwd.
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	st := builtinCd(s, []string{tmp}, os.Stdin, os.Stdout, os.Stderr)
	if st != 0 {
		t.Fatalf("cd %s: status %d", tmp, st)
	}

	out, _ := captureBuiltin(t, builtinPwd, s, nil)
	if out != tmp {
		t.Errorf("pwd = %q, want %q", out, tmp)
	}
}

func TestBuiltinCdHome(t *testing.T) {
	s := testState(t)
	home, _ := filepath.EvalSymlinks(s.vars["HOME"])
	s.vars["HOME"] = home
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	st := builtinCd(s, nil, os.Stdin, os.Stdout, os.Stderr)
	if st != 0 {
		t.Fatalf("cd (home): status %d", st)
	}
	wd, _ := os.Getwd()
	if wd != home {
		t.Errorf("cd to HOME: got %q, want %q", wd, home)
	}
}

func TestBuiltinCdDash(t *testing.T) {
	s := testState(t)
	tmp1, _ := filepath.EvalSymlinks(t.TempDir())
	tmp2, _ := filepath.EvalSymlinks(t.TempDir())
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	builtinCd(s, []string{tmp1}, os.Stdin, os.Stdout, os.Stderr)
	builtinCd(s, []string{tmp2}, os.Stdin, os.Stdout, os.Stderr)

	r, w, _ := os.Pipe()
	builtinCd(s, []string{"-"}, os.Stdin, w, os.Stderr)
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()

	wd, _ := os.Getwd()
	if wd != tmp1 {
		t.Errorf("cd -: got %q, want %q", wd, tmp1)
	}
	// cd - should print the directory.
	if strings.TrimSpace(string(out)) != tmp1 {
		t.Errorf("cd - output = %q, want %q", string(out), tmp1)
	}
}

func TestBuiltinCdNonexistent(t *testing.T) {
	s := testState(t)
	st := builtinCd(s, []string{"/no/such/dir"}, os.Stdin, os.Stdout, os.Stderr)
	if st != 1 {
		t.Errorf("cd nonexistent: status %d, want 1", st)
	}
}

// --- export ---

func TestBuiltinExportSetAndMark(t *testing.T) {
	s := testState(t)
	builtinExport(s, []string{"FOO=bar"}, os.Stdin, os.Stdout, os.Stderr)
	assertVar(t, s, "FOO", "bar")
	if !s.exported["FOO"] {
		t.Error("FOO should be exported")
	}
}

func TestBuiltinExportMarkOnly(t *testing.T) {
	s := testState(t)
	s.setVar("X", "val")
	builtinExport(s, []string{"X"}, os.Stdin, os.Stdout, os.Stderr)
	if !s.exported["X"] {
		t.Error("X should be exported")
	}
}

func TestBuiltinExportList(t *testing.T) {
	s := testState(t)
	s.setVar("MY", "val")
	s.exportVar("MY")
	out, _ := captureBuiltin(t, builtinExport, s, nil)
	if !strings.Contains(out, "MY") {
		t.Error("export list should contain MY")
	}
}

// --- unset ---

func TestBuiltinUnsetScalar(t *testing.T) {
	s := testState(t)
	s.setVar("X", "1")
	builtinUnset(s, []string{"X"}, os.Stdin, os.Stdout, os.Stderr)
	assertVar(t, s, "X", "")
}

// --- read ---

func TestBuiltinReadReply(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	w.WriteString("hello world\n")
	w.Close()
	st := builtinRead(s, nil, r, os.Stdout, os.Stderr)
	r.Close()
	if st != 0 {
		t.Fatalf("read status = %d", st)
	}
	assertVar(t, s, "REPLY", "hello world")
}

func TestBuiltinReadMultiVar(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	w.WriteString("a b c d\n")
	w.Close()
	builtinRead(s, []string{"x", "y"}, r, os.Stdout, os.Stderr)
	r.Close()
	assertVar(t, s, "x", "a")
	assertVar(t, s, "y", "b c d")
}

func TestBuiltinReadEOF(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	w.Close()
	st := builtinRead(s, nil, r, os.Stdout, os.Stderr)
	r.Close()
	if st != 1 {
		t.Errorf("read on EOF: status %d, want 1", st)
	}
}

func TestBuiltinReadRaw(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	w.WriteString("hello\\nworld\n")
	w.Close()
	builtinRead(s, []string{"-r", "line"}, r, os.Stdout, os.Stderr)
	r.Close()
	// Raw mode: backslash is literal.
	assertVar(t, s, "line", "hello\\nworld")
}

func TestBuiltinReadArray(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	w.WriteString("a b c\n")
	w.Close()
	builtinRead(s, []string{"-a", "arr"}, r, os.Stdout, os.Stderr)
	r.Close()
	assertVar(t, s, "arr[0]", "a")
	assertVar(t, s, "arr[1]", "b")
	assertVar(t, s, "arr[2]", "c")
}

// --- printf ---

func TestBuiltinPrintfString(t *testing.T) {
	s := testState(t)
	out, _ := captureBuiltin(t, builtinPrintf, s, []string{"%s", "hello"})
	if out != "hello" {
		t.Errorf("printf %%s = %q", out)
	}
}

func TestBuiltinPrintfDecimal(t *testing.T) {
	s := testState(t)
	out, _ := captureBuiltin(t, builtinPrintf, s, []string{"%d", "42"})
	if out != "42" {
		t.Errorf("printf %%d = %q", out)
	}
}

func TestBuiltinPrintfHex(t *testing.T) {
	s := testState(t)
	out, _ := captureBuiltin(t, builtinPrintf, s, []string{"%x", "255"})
	if out != "ff" {
		t.Errorf("printf %%x = %q", out)
	}
}

func TestBuiltinPrintfEscapes(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	builtinPrintf(s, []string{"a\\nb\\t"}, os.Stdin, w, os.Stderr)
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	if string(out) != "a\nb\t" {
		t.Errorf("printf escapes = %q, want %q", string(out), "a\nb\t")
	}
}

func TestBuiltinPrintfOctalEscape(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	builtinPrintf(s, []string{"\\0101"}, os.Stdin, w, os.Stderr)
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	if string(out) != "A" {
		t.Errorf("printf \\0101 = %q, want %q", string(out), "A")
	}
}

func TestBuiltinPrintfHexEscape(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	builtinPrintf(s, []string{"\\x41"}, os.Stdin, w, os.Stderr)
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	if string(out) != "A" {
		t.Errorf("printf \\x41 = %q, want %q", string(out), "A")
	}
}

func TestBuiltinPrintfReuse(t *testing.T) {
	s := testState(t)
	r, w, _ := os.Pipe()
	builtinPrintf(s, []string{"%s ", "a", "b", "c"}, os.Stdin, w, os.Stderr)
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	if string(out) != "a b c " {
		t.Errorf("printf reuse = %q, want %q", string(out), "a b c ")
	}
}

func TestBuiltinPrintfPercent(t *testing.T) {
	s := testState(t)
	out, _ := captureBuiltin(t, builtinPrintf, s, []string{"100%%"})
	if out != "100%" {
		t.Errorf("printf %%%% = %q", out)
	}
}

// --- shift ---

func TestBuiltinShiftDefault(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"a", "b", "c"}
	st := builtinShift(s, nil, os.Stdin, os.Stdout, os.Stderr)
	if st != 0 {
		t.Fatalf("shift status = %d", st)
	}
	assertVar(t, s, "1", "b")
	assertVar(t, s, "#", "2")
}

func TestBuiltinShiftN(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"a", "b", "c", "d"}
	builtinShift(s, []string{"2"}, os.Stdin, os.Stdout, os.Stderr)
	assertVar(t, s, "1", "c")
	assertVar(t, s, "#", "2")
}

func TestBuiltinShiftOutOfRange(t *testing.T) {
	s := testState(t)
	s.positionalParams = []string{"a"}
	st := builtinShift(s, []string{"5"}, os.Stdin, os.Stdout, os.Stderr)
	if st != 1 {
		t.Errorf("shift out of range: status %d, want 1", st)
	}
}

// --- local ---

func TestBuiltinLocalOutsideFunc(t *testing.T) {
	s := testState(t)
	st := builtinLocal(s, []string{"x"}, os.Stdin, os.Stdout, os.Stderr)
	if st != 1 {
		t.Errorf("local outside func: status %d, want 1", st)
	}
}

func TestBuiltinLocalInsideFunc(t *testing.T) {
	s := testState(t)
	// Simulate being inside a function by pushing a scope.
	s.localScopes = append(s.localScopes, make(map[string]savedVar))
	s.setVar("x", "outer")
	builtinLocal(s, []string{"x=inner"}, os.Stdin, os.Stdout, os.Stderr)
	assertVar(t, s, "x", "inner")
	// Restore scope.
	scope := s.localScopes[len(s.localScopes)-1]
	s.localScopes = s.localScopes[:len(s.localScopes)-1]
	restoreVars(s, scope)
	assertVar(t, s, "x", "outer")
}

// --- alias/unalias ---

func TestBuiltinAliasDefine(t *testing.T) {
	s := testState(t)
	builtinAlias(s, []string{"ll=ls -la"}, os.Stdin, os.Stdout, os.Stderr)
	if s.aliases["ll"] != "ls -la" {
		t.Errorf("alias ll = %q", s.aliases["ll"])
	}
}

func TestBuiltinAliasList(t *testing.T) {
	s := testState(t)
	s.aliases["g"] = "git"
	out, _ := captureBuiltin(t, builtinAlias, s, nil)
	if !strings.Contains(out, "g=") {
		t.Errorf("alias list should contain g, got %q", out)
	}
}

func TestBuiltinUnalias(t *testing.T) {
	s := testState(t)
	s.aliases["g"] = "git"
	builtinUnalias(s, []string{"g"}, os.Stdin, os.Stdout, os.Stderr)
	if _, ok := s.aliases["g"]; ok {
		t.Error("unalias should remove g")
	}
}

func TestBuiltinUnaliasAll(t *testing.T) {
	s := testState(t)
	s.aliases["a"] = "x"
	s.aliases["b"] = "y"
	builtinUnalias(s, []string{"-a"}, os.Stdin, os.Stdout, os.Stderr)
	if len(s.aliases) != 0 {
		t.Errorf("unalias -a: still %d aliases", len(s.aliases))
	}
}

// --- splitByIFS ---

func TestSplitByIFSDefault(t *testing.T) {
	fields := splitByIFS("  a  b  c  ", " \t\n", 100)
	if len(fields) != 3 || fields[0] != "a" || fields[1] != "b" || fields[2] != "c" {
		t.Errorf("splitByIFS default = %q", fields)
	}
}

func TestSplitByIFSColon(t *testing.T) {
	fields := splitByIFS("a:b:c", ":", 100)
	if len(fields) != 3 || fields[0] != "a" || fields[1] != "b" || fields[2] != "c" {
		t.Errorf("splitByIFS colon = %q", fields)
	}
}

func TestSplitByIFSLastField(t *testing.T) {
	fields := splitByIFS("a b c d", " \t\n", 2)
	if len(fields) != 2 || fields[0] != "a" || fields[1] != "b c d" {
		t.Errorf("splitByIFS last field = %q", fields)
	}
}

// --- source ---

func TestBuiltinSource(t *testing.T) {
	s := testState(t)
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.sh")
	os.WriteFile(f, []byte("X=from_source\n"), 0644)
	builtinSource(s, []string{f}, os.Stdin, os.Stdout, os.Stderr)
	assertVar(t, s, "X", "from_source")
}

// --- true/false ---

func TestBuiltinTrue(t *testing.T) {
	s := testState(t)
	st := builtinTrue(s, nil, os.Stdin, os.Stdout, os.Stderr)
	if st != 0 {
		t.Errorf("true status = %d", st)
	}
}

func TestBuiltinFalse(t *testing.T) {
	s := testState(t)
	st := builtinFalse(s, nil, os.Stdin, os.Stdout, os.Stderr)
	if st != 1 {
		t.Errorf("false status = %d", st)
	}
}
