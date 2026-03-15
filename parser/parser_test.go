package parser

import (
	"gosh/lexer"
	"testing"
)

func mustParse(t *testing.T, input string) *List {
	t.Helper()
	tokens, err := lexer.Lex(input)
	if err != nil {
		t.Fatalf("lex error: %v", err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return list
}

func TestSimpleCommand(t *testing.T) {
	list := mustParse(t, "echo hello world")

	if len(list.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list.Entries))
	}
	pipe := list.Entries[0].Pipeline
	if len(pipe.Cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(pipe.Cmds))
	}
	cmd := pipe.Cmds[0]
	expectArgs(t, cmd, "echo", "hello", "world")
	if len(cmd.Redirects) != 0 {
		t.Errorf("expected 0 redirects, got %d", len(cmd.Redirects))
	}
}

func TestPipeline(t *testing.T) {
	list := mustParse(t, "ls -l | grep foo | wc -l")

	pipe := list.Entries[0].Pipeline
	if len(pipe.Cmds) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(pipe.Cmds))
	}
	expectArgs(t, pipe.Cmds[0], "ls", "-l")
	expectArgs(t, pipe.Cmds[1], "grep", "foo")
	expectArgs(t, pipe.Cmds[2], "wc", "-l")
}

func TestRedirectOut(t *testing.T) {
	list := mustParse(t, "echo hello > out.txt")

	cmd := list.Entries[0].Pipeline.Cmds[0]
	expectArgs(t, cmd, "echo", "hello")
	if len(cmd.Redirects) != 1 {
		t.Fatalf("expected 1 redirect, got %d", len(cmd.Redirects))
	}
	if cmd.Redirects[0].Type != REDIR_OUT {
		t.Errorf("expected REDIR_OUT, got %d", cmd.Redirects[0].Type)
	}
	if cmd.Redirects[0].File != "out.txt" {
		t.Errorf("expected out.txt, got %s", cmd.Redirects[0].File)
	}
}

func TestRedirectIn(t *testing.T) {
	list := mustParse(t, "wc -l < input.txt")

	cmd := list.Entries[0].Pipeline.Cmds[0]
	expectArgs(t, cmd, "wc", "-l")
	if len(cmd.Redirects) != 1 {
		t.Fatalf("expected 1 redirect, got %d", len(cmd.Redirects))
	}
	if cmd.Redirects[0].Type != REDIR_IN {
		t.Errorf("expected REDIR_IN, got %d", cmd.Redirects[0].Type)
	}
	if cmd.Redirects[0].File != "input.txt" {
		t.Errorf("expected input.txt, got %s", cmd.Redirects[0].File)
	}
}

func TestRedirectAppend(t *testing.T) {
	list := mustParse(t, "echo line >> log.txt")

	cmd := list.Entries[0].Pipeline.Cmds[0]
	if len(cmd.Redirects) != 1 {
		t.Fatalf("expected 1 redirect, got %d", len(cmd.Redirects))
	}
	if cmd.Redirects[0].Type != REDIR_APPEND {
		t.Errorf("expected REDIR_APPEND, got %d", cmd.Redirects[0].Type)
	}
}

func TestMultipleRedirects(t *testing.T) {
	list := mustParse(t, "sort < in.txt > out.txt")

	cmd := list.Entries[0].Pipeline.Cmds[0]
	expectArgs(t, cmd, "sort")
	if len(cmd.Redirects) != 2 {
		t.Fatalf("expected 2 redirects, got %d", len(cmd.Redirects))
	}
	if cmd.Redirects[0].Type != REDIR_IN {
		t.Errorf("redirect 0: expected REDIR_IN, got %d", cmd.Redirects[0].Type)
	}
	if cmd.Redirects[1].Type != REDIR_OUT {
		t.Errorf("redirect 1: expected REDIR_OUT, got %d", cmd.Redirects[1].Type)
	}
}

func TestSemicolon(t *testing.T) {
	list := mustParse(t, "echo a ; echo b")

	if len(list.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list.Entries))
	}
	if list.Entries[0].Op != ";" {
		t.Errorf("expected op ';', got %q", list.Entries[0].Op)
	}
	expectArgs(t, list.Entries[0].Pipeline.Cmds[0], "echo", "a")
	expectArgs(t, list.Entries[1].Pipeline.Cmds[0], "echo", "b")
	if list.Entries[1].Op != "" {
		t.Errorf("expected empty op for last entry, got %q", list.Entries[1].Op)
	}
}

func TestTrailingSemicolon(t *testing.T) {
	list := mustParse(t, "echo hi ;")

	if len(list.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list.Entries))
	}
	if list.Entries[0].Op != ";" {
		t.Errorf("expected op ';', got %q", list.Entries[0].Op)
	}
}

func TestAndOr(t *testing.T) {
	list := mustParse(t, "make && make test || echo fail")

	if len(list.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(list.Entries))
	}
	if list.Entries[0].Op != "&&" {
		t.Errorf("expected '&&', got %q", list.Entries[0].Op)
	}
	if list.Entries[1].Op != "||" {
		t.Errorf("expected '||', got %q", list.Entries[1].Op)
	}
}

func TestPipelineWithRedirects(t *testing.T) {
	list := mustParse(t, "cat < in.txt | sort | head -5 > out.txt")

	pipe := list.Entries[0].Pipeline
	if len(pipe.Cmds) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(pipe.Cmds))
	}

	// First command: cat < in.txt
	if len(pipe.Cmds[0].Redirects) != 1 {
		t.Errorf("cmd 0: expected 1 redirect, got %d", len(pipe.Cmds[0].Redirects))
	}

	// Middle command: sort (no redirects)
	if len(pipe.Cmds[1].Redirects) != 0 {
		t.Errorf("cmd 1: expected 0 redirects, got %d", len(pipe.Cmds[1].Redirects))
	}

	// Last command: head -5 > out.txt
	if len(pipe.Cmds[2].Redirects) != 1 {
		t.Errorf("cmd 2: expected 1 redirect, got %d", len(pipe.Cmds[2].Redirects))
	}
}

func TestParseError_EmptyPipeline(t *testing.T) {
	tokens, _ := lexer.Lex("echo hi |")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for empty pipeline stage")
	}
}

func TestParseError_MissingRedirectTarget(t *testing.T) {
	tokens, _ := lexer.Lex("echo hi >")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing redirect target")
	}
}

func TestQuotedArgsPreserved(t *testing.T) {
	list := mustParse(t, `echo "hello world" 'foo bar'`)
	cmd := list.Entries[0].Pipeline.Cmds[0]
	expectArgs(t, cmd, "echo", "hello world", "foo bar")
}

// --- helpers ---

func expectArgs(t *testing.T, cmd *SimpleCmd, want ...string) {
	t.Helper()
	if len(cmd.Args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, cmd.Args)
	}
	for i, w := range want {
		if cmd.Args[i] != w {
			t.Errorf("arg %d: expected %q, got %q", i, w, cmd.Args[i])
		}
	}
}
