package expander

import (
	"gosh/lexer"
	"gosh/parser"
	"testing"
)

// testLookup is a simple variable lookup for tests.
func testLookup(name string) string {
	vars := map[string]string{
		"HOME":  "/home/user",
		"USER":  "alice",
		"EMPTY": "",
		"?":     "0",
		"$":     "12345",
	}
	return vars[name]
}

func TestExpandUnquoted(t *testing.T) {
	list := mustParse(t, "echo $HOME")
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "/home/user")
}

func TestExpandDoubleQuoted(t *testing.T) {
	list := mustParse(t, `echo "$HOME"`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "/home/user")
}

func TestExpandSingleQuotedNoExpansion(t *testing.T) {
	list := mustParse(t, "echo '$HOME'")
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "$HOME")
}

func TestExpandBackslashDollarNoExpansion(t *testing.T) {
	list := mustParse(t, `echo \$HOME`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "$HOME")
}

func TestExpandDoubleQuoteBackslashDollar(t *testing.T) {
	list := mustParse(t, `echo "\$HOME"`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "$HOME")
}

func TestExpandBraces(t *testing.T) {
	list := mustParse(t, `echo "${HOME}/bin"`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "/home/user/bin")
}

func TestExpandMixed(t *testing.T) {
	list := mustParse(t, `echo "hello $USER, welcome"`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "hello alice, welcome")
}

func TestExpandMultipleVars(t *testing.T) {
	list := mustParse(t, `echo $USER@$HOME`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "alice@/home/user")
}

func TestExpandUndefined(t *testing.T) {
	list := mustParse(t, `echo $UNDEFINED`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "")
}

func TestExpandExitStatus(t *testing.T) {
	list := mustParse(t, `echo $?`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "0")
}

func TestExpandShellPid(t *testing.T) {
	list := mustParse(t, `echo $$`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "12345")
}

func TestExpandBareDollar(t *testing.T) {
	list := mustParse(t, `echo $ `)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "echo", "$")
}

func TestExpandAssignmentValue(t *testing.T) {
	list := mustParse(t, `DIR=$HOME/bin`)
	Expand(list, testLookup)
	cmd := list.Entries[0].Pipeline.Cmds[0]
	if len(cmd.Assigns) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(cmd.Assigns))
	}
	if cmd.Assigns[0].Value.String() != "/home/user/bin" {
		t.Errorf("expected /home/user/bin, got %q", cmd.Assigns[0].Value)
	}
}

func TestExpandRedirectFilename(t *testing.T) {
	list := mustParse(t, `echo hi > $HOME/out.txt`)
	Expand(list, testLookup)
	cmd := list.Entries[0].Pipeline.Cmds[0]
	if cmd.Redirects[0].File.String() != "/home/user/out.txt" {
		t.Errorf("expected /home/user/out.txt, got %q", cmd.Redirects[0].File)
	}
}

func TestExpandMixedQuoting(t *testing.T) {
	// he"$USER"'$HOME' → healice$HOME
	list := mustParse(t, `he"$USER"'$HOME'`)
	Expand(list, testLookup)
	expectArgs(t, list, 0, "healice$HOME")
}

// --- helpers ---

func mustParse(t *testing.T, input string) *parser.List {
	t.Helper()
	tokens, err := lexer.Lex(input)
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	list, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return list
}

func expectArgs(t *testing.T, list *parser.List, entryIdx int, want ...string) {
	t.Helper()
	cmd := list.Entries[entryIdx].Pipeline.Cmds[0]
	got := cmd.ArgStrings()
	if len(got) != len(want) {
		t.Fatalf("expected args %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("arg %d: expected %q, got %q", i, w, got[i])
		}
	}
}
