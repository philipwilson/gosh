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
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_PIPE, TOKEN_WORD, TOKEN_WORD, TOKEN_EOF)
	expectWordVal(t, tokens, 0, "ls")
	expectWordVal(t, tokens, 2, "grep")
	expectWordVal(t, tokens, 3, "foo")
}

func TestRedirections(t *testing.T) {
	tokens, err := Lex("cat < in.txt > out.txt")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_LT, TOKEN_WORD, TOKEN_GT, TOKEN_WORD, TOKEN_EOF)
}

func TestAppend(t *testing.T) {
	tokens, err := Lex("echo hi >> log.txt")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_WORD, TOKEN_APPEND, TOKEN_WORD, TOKEN_EOF)
}

func TestAndOr(t *testing.T) {
	tokens, err := Lex("make && make test || echo fail")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens,
		TOKEN_WORD, TOKEN_AND, TOKEN_WORD, TOKEN_WORD, TOKEN_OR, TOKEN_WORD, TOKEN_WORD, TOKEN_EOF)
}

func TestSemicolon(t *testing.T) {
	tokens, err := Lex("echo a ; echo b")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens,
		TOKEN_WORD, TOKEN_WORD, TOKEN_SEMI, TOKEN_WORD, TOKEN_WORD, TOKEN_EOF)
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
	expectTokenTypes(t, tokens,
		TOKEN_WORD, TOKEN_PIPE, TOKEN_WORD, TOKEN_WORD, TOKEN_GT, TOKEN_WORD, TOKEN_EOF)
}

// --- Comment tests ---

func TestCommentOnly(t *testing.T) {
	tokens, err := Lex("# this is a comment")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_EOF)
}

func TestCommentAfterCommand(t *testing.T) {
	tokens, err := Lex("echo hello # world")
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "hello")
}

func TestHashInQuotes(t *testing.T) {
	tokens, err := Lex(`echo "hello # world"`)
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "hello # world")
}

func TestHashInSingleQuotes(t *testing.T) {
	tokens, err := Lex("echo 'hello # world'")
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "hello # world")
}

func TestHashMidWord(t *testing.T) {
	// # only starts a comment when it's the first char of a token
	tokens, err := Lex("echo foo#bar")
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "foo#bar")
}

// --- Word parts tests ---

func TestPartsUnquoted(t *testing.T) {
	tokens, err := Lex("echo $HOME")
	if err != nil {
		t.Fatal(err)
	}
	// "$HOME" should be a single Unquoted part
	expectParts(t, tokens[1].Parts, WordPart{"$HOME", Unquoted})
}

func TestPartsSingleQuoted(t *testing.T) {
	tokens, err := Lex("echo '$HOME'")
	if err != nil {
		t.Fatal(err)
	}
	// '$HOME' should be SingleQuoted — no expansion
	expectParts(t, tokens[1].Parts, WordPart{"$HOME", SingleQuoted})
}

func TestPartsDoubleQuoted(t *testing.T) {
	tokens, err := Lex(`echo "$HOME"`)
	if err != nil {
		t.Fatal(err)
	}
	// "$HOME" should be DoubleQuoted — expansion will happen
	expectParts(t, tokens[1].Parts, WordPart{"$HOME", DoubleQuoted})
}

func TestPartsBackslashDollar(t *testing.T) {
	tokens, err := Lex(`echo \$HOME`)
	if err != nil {
		t.Fatal(err)
	}
	// \$ → SingleQuoted("$"), HOME → Unquoted("HOME")
	expectParts(t, tokens[1].Parts,
		WordPart{"$", SingleQuoted},
		WordPart{"HOME", Unquoted},
	)
}

func TestPartsDoubleQuoteBackslashDollar(t *testing.T) {
	tokens, err := Lex(`echo "\$HOME"`)
	if err != nil {
		t.Fatal(err)
	}
	// \$ inside "" → SingleQuoted("$"), HOME stays DoubleQuoted
	expectParts(t, tokens[1].Parts,
		WordPart{"$", SingleQuoted},
		WordPart{"HOME", DoubleQuoted},
	)
}

func TestPartsMixed(t *testing.T) {
	tokens, err := Lex(`he"$USER"'!'`)
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "he$USER!")
	expectParts(t, tokens[0].Parts,
		WordPart{"he", Unquoted},
		WordPart{"$USER", DoubleQuoted},
		WordPart{"!", SingleQuoted},
	)
}

func TestPartsBackslashSpace(t *testing.T) {
	tokens, err := Lex(`hello\ world`)
	if err != nil {
		t.Fatal(err)
	}
	// \<space> → SingleQuoted(" ")
	expectParts(t, tokens[0].Parts,
		WordPart{"hello", Unquoted},
		WordPart{" ", SingleQuoted},
		WordPart{"world", Unquoted},
	)
}

// --- helpers ---

func expectWords(t *testing.T, tokens []Token, words ...string) {
	t.Helper()
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

func expectTokenTypes(t *testing.T, tokens []Token, types ...TokenType) {
	t.Helper()
	if len(tokens) != len(types) {
		t.Fatalf("expected %d tokens, got %d: %v", len(types), len(tokens), tokens)
	}
	for i, tt := range types {
		if tokens[i].Type != tt {
			t.Errorf("token %d: expected type %s, got %s", i, tt, tokens[i].Type)
		}
	}
}

func expectWordVal(t *testing.T, tokens []Token, idx int, val string) {
	t.Helper()
	if tokens[idx].Val != val {
		t.Errorf("token %d: expected val %q, got %q", idx, val, tokens[idx].Val)
	}
}

func expectParts(t *testing.T, got Word, want ...WordPart) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d parts, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i].Text != want[i].Text {
			t.Errorf("part %d: expected text %q, got %q", i, want[i].Text, got[i].Text)
		}
		if got[i].Quote != want[i].Quote {
			t.Errorf("part %d: expected quote %d, got %d", i, want[i].Quote, got[i].Quote)
		}
	}
}
