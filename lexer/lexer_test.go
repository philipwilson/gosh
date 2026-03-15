package lexer

import (
	"testing"
)

func TestSimpleWords(t *testing.T) {
	tokens, err := Lex("echo hello world")
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "hello", "world")
}

func TestSingleQuotes(t *testing.T) {
	tokens, err := Lex("echo 'hello world'")
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "hello world")
}

func TestDoubleQuotes(t *testing.T) {
	tokens, err := Lex(`echo "hello world"`)
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "hello world")
}

func TestDoubleQuoteEscapes(t *testing.T) {
	// \" inside double quotes → literal "
	tokens, err := Lex(`echo "say \"hi\""`)
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", `say "hi"`)
}

func TestDoubleQuoteBackslashPreserved(t *testing.T) {
	// \n inside double quotes → literal \n (backslash preserved)
	tokens, err := Lex(`echo "hello\nworld"`)
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", `hello\nworld`)
}

func TestBackslashOutsideQuotes(t *testing.T) {
	tokens, err := Lex(`echo hello\ world`)
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "hello world")
}

func TestMixedQuoting(t *testing.T) {
	// he"ll"o → hello (quotes can appear mid-word)
	tokens, err := Lex(`he"ll"o`)
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "hello")
}

func TestPipe(t *testing.T) {
	tokens, err := Lex("ls | grep foo")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, tokens, []Token{
		{TOKEN_WORD, "ls"},
		{TOKEN_PIPE, ""},
		{TOKEN_WORD, "grep"},
		{TOKEN_WORD, "foo"},
		{TOKEN_EOF, ""},
	})
}

func TestRedirections(t *testing.T) {
	tokens, err := Lex("cat < in.txt > out.txt")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, tokens, []Token{
		{TOKEN_WORD, "cat"},
		{TOKEN_LT, ""},
		{TOKEN_WORD, "in.txt"},
		{TOKEN_GT, ""},
		{TOKEN_WORD, "out.txt"},
		{TOKEN_EOF, ""},
	})
}

func TestAppend(t *testing.T) {
	tokens, err := Lex("echo hi >> log.txt")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, tokens, []Token{
		{TOKEN_WORD, "echo"},
		{TOKEN_WORD, "hi"},
		{TOKEN_APPEND, ""},
		{TOKEN_WORD, "log.txt"},
		{TOKEN_EOF, ""},
	})
}

func TestAndOr(t *testing.T) {
	tokens, err := Lex("make && make test || echo fail")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, tokens, []Token{
		{TOKEN_WORD, "make"},
		{TOKEN_AND, ""},
		{TOKEN_WORD, "make"},
		{TOKEN_WORD, "test"},
		{TOKEN_OR, ""},
		{TOKEN_WORD, "echo"},
		{TOKEN_WORD, "fail"},
		{TOKEN_EOF, ""},
	})
}

func TestSemicolon(t *testing.T) {
	tokens, err := Lex("echo a ; echo b")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, tokens, []Token{
		{TOKEN_WORD, "echo"},
		{TOKEN_WORD, "a"},
		{TOKEN_SEMI, ""},
		{TOKEN_WORD, "echo"},
		{TOKEN_WORD, "b"},
		{TOKEN_EOF, ""},
	})
}

func TestUnterminatedSingleQuote(t *testing.T) {
	_, err := Lex("echo 'hello")
	if err == nil {
		t.Fatal("expected error for unterminated single quote")
	}
}

func TestUnterminatedDoubleQuote(t *testing.T) {
	_, err := Lex(`echo "hello`)
	if err == nil {
		t.Fatal("expected error for unterminated double quote")
	}
}

func TestEmptyQuotedString(t *testing.T) {
	tokens, err := Lex(`echo "" ''`)
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "", "")
}

func TestOperatorsWithoutSpaces(t *testing.T) {
	tokens, err := Lex("ls|grep foo>out.txt")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, tokens, []Token{
		{TOKEN_WORD, "ls"},
		{TOKEN_PIPE, ""},
		{TOKEN_WORD, "grep"},
		{TOKEN_WORD, "foo"},
		{TOKEN_GT, ""},
		{TOKEN_WORD, "out.txt"},
		{TOKEN_EOF, ""},
	})
}

// --- helpers ---

func expectWords(t *testing.T, tokens []Token, words ...string) {
	t.Helper()
	// Should be len(words) WORD tokens + 1 EOF
	if len(tokens) != len(words)+1 {
		t.Fatalf("expected %d tokens, got %d: %v", len(words)+1, len(tokens), tokens)
	}
	for i, w := range words {
		if tokens[i].Type != TOKEN_WORD {
			t.Errorf("token %d: expected WORD, got %s", i, tokens[i].Type)
		}
		if tokens[i].Val != w {
			t.Errorf("token %d: expected %q, got %q", i, w, tokens[i].Val)
		}
	}
	if tokens[len(tokens)-1].Type != TOKEN_EOF {
		t.Errorf("last token: expected EOF, got %s", tokens[len(tokens)-1].Type)
	}
}

func expect(t *testing.T, got []Token, want []Token) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d tokens, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i].Type != want[i].Type {
			t.Errorf("token %d: expected type %s, got %s", i, want[i].Type, got[i].Type)
		}
		if want[i].Type == TOKEN_WORD && got[i].Val != want[i].Val {
			t.Errorf("token %d: expected val %q, got %q", i, want[i].Val, got[i].Val)
		}
	}
}
