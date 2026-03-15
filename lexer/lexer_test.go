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

// --- Fd redirect tests ---

func TestStderrRedirect(t *testing.T) {
	tokens, err := Lex("cmd 2>err.txt")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_GT, TOKEN_WORD, TOKEN_EOF)
	if tokens[1].Fd != 2 {
		t.Errorf("expected fd 2, got %d", tokens[1].Fd)
	}
	expectWordVal(t, tokens, 2, "err.txt")
}

func TestStderrAppend(t *testing.T) {
	tokens, err := Lex("cmd 2>>err.txt")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_APPEND, TOKEN_WORD, TOKEN_EOF)
	if tokens[1].Fd != 2 {
		t.Errorf("expected fd 2, got %d", tokens[1].Fd)
	}
}

func TestStderrToStdout(t *testing.T) {
	tokens, err := Lex("cmd 2>&1")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_DUP, TOKEN_EOF)
	if tokens[1].Fd != 2 {
		t.Errorf("expected fd 2, got %d", tokens[1].Fd)
	}
	if tokens[1].Val != "1" {
		t.Errorf("expected dup target 1, got %q", tokens[1].Val)
	}
}

func TestStdoutToStderr(t *testing.T) {
	tokens, err := Lex("cmd >&2")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_DUP, TOKEN_EOF)
	if tokens[1].Fd != 1 {
		t.Errorf("expected fd 1 (default), got %d", tokens[1].Fd)
	}
	if tokens[1].Val != "2" {
		t.Errorf("expected dup target 2, got %q", tokens[1].Val)
	}
}

func TestDefaultRedirectFd(t *testing.T) {
	// > without explicit fd should have Fd=-1.
	tokens, err := Lex("echo hi > out.txt")
	if err != nil {
		t.Fatal(err)
	}
	if tokens[2].Type != TOKEN_GT {
		t.Fatalf("expected GT, got %s", tokens[2].Type)
	}
	if tokens[2].Fd != -1 {
		t.Errorf("expected fd -1 (default), got %d", tokens[2].Fd)
	}
}

func TestStdinDup(t *testing.T) {
	tokens, err := Lex("cmd <&3")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_DUP, TOKEN_EOF)
	if tokens[1].Fd != 0 {
		t.Errorf("expected fd 0 (default for <&), got %d", tokens[1].Fd)
	}
	if tokens[1].Val != "3" {
		t.Errorf("expected dup target 3, got %q", tokens[1].Val)
	}
}

func TestFdRedirectNoSpace(t *testing.T) {
	// 2>err.txt with no spaces — the "2" should be absorbed.
	tokens, err := Lex("cmd 2>err.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Should be: WORD("cmd"), GT(fd=2), WORD("err.txt"), EOF
	// NOT: WORD("cmd"), WORD("2"), GT, WORD("err.txt"), EOF
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_GT, TOKEN_WORD, TOKEN_EOF)
	expectWordVal(t, tokens, 0, "cmd")
}

func TestWordTwoNotFd(t *testing.T) {
	// "22>file" — "22" is not a single digit, so should not be absorbed.
	tokens, err := Lex("22>file")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens, TOKEN_WORD, TOKEN_GT, TOKEN_WORD, TOKEN_EOF)
	expectWordVal(t, tokens, 0, "22")
	if tokens[1].Fd != -1 {
		t.Errorf("expected fd -1 (22 is not a single digit), got %d", tokens[1].Fd)
	}
}

func TestComplexRedirects(t *testing.T) {
	// echo hello >out.txt 2>&1
	tokens, err := Lex("echo hello >out.txt 2>&1")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens,
		TOKEN_WORD, TOKEN_WORD, TOKEN_GT, TOKEN_WORD, TOKEN_DUP, TOKEN_EOF)
	if tokens[2].Fd != -1 {
		t.Errorf("expected fd -1 for >, got %d", tokens[2].Fd)
	}
	if tokens[4].Fd != 2 {
		t.Errorf("expected fd 2 for dup, got %d", tokens[4].Fd)
	}
	if tokens[4].Val != "1" {
		t.Errorf("expected dup target 1, got %q", tokens[4].Val)
	}
}

// --- Command substitution tests ---

func TestCmdSubstDollarParen(t *testing.T) {
	tokens, err := Lex("echo $(whoami)")
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "whoami")
	expectParts(t, tokens[1].Parts, WordPart{"whoami", CmdSubst})
}

func TestCmdSubstBacktick(t *testing.T) {
	tokens, err := Lex("echo `whoami`")
	if err != nil {
		t.Fatal(err)
	}
	expectWords(t, tokens, "echo", "whoami")
	expectParts(t, tokens[1].Parts, WordPart{"whoami", CmdSubst})
}

func TestCmdSubstInDoubleQuotes(t *testing.T) {
	tokens, err := Lex(`echo "hello $(whoami)"`)
	if err != nil {
		t.Fatal(err)
	}
	expectParts(t, tokens[1].Parts,
		WordPart{"hello ", DoubleQuoted},
		WordPart{"whoami", CmdSubstDQ},
	)
}

func TestCmdSubstBacktickInDoubleQuotes(t *testing.T) {
	tokens, err := Lex("echo \"hello `whoami`\"")
	if err != nil {
		t.Fatal(err)
	}
	expectParts(t, tokens[1].Parts,
		WordPart{"hello ", DoubleQuoted},
		WordPart{"whoami", CmdSubstDQ},
	)
}

func TestCmdSubstNested(t *testing.T) {
	tokens, err := Lex("echo $(echo $(whoami))")
	if err != nil {
		t.Fatal(err)
	}
	expectParts(t, tokens[1].Parts, WordPart{"echo $(whoami)", CmdSubst})
}

func TestCmdSubstMixedWithText(t *testing.T) {
	tokens, err := Lex("echo pre$(whoami)post")
	if err != nil {
		t.Fatal(err)
	}
	expectParts(t, tokens[1].Parts,
		WordPart{"pre", Unquoted},
		WordPart{"whoami", CmdSubst},
		WordPart{"post", Unquoted},
	)
}

func TestCmdSubstUnterminated(t *testing.T) {
	_, err := Lex("echo $(whoami")
	if err == nil {
		t.Fatal("expected error for unterminated $(")
	}
}

func TestBacktickUnterminated(t *testing.T) {
	_, err := Lex("echo `whoami")
	if err == nil {
		t.Fatal("expected error for unterminated backtick")
	}
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
