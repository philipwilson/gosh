package lexer

import "testing"

func TestHeredocToken(t *testing.T) {
	tokens, err := Lex("cat <<EOF")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens,
		TOKEN_WORD,    // cat
		TOKEN_HEREDOC, // <<EOF
		TOKEN_EOF,
	)
	if tokens[1].Heredoc == nil {
		t.Fatal("expected HeredocInfo")
	}
	if tokens[1].Heredoc.Delim != "EOF" {
		t.Errorf("delim: got %q, want %q", tokens[1].Heredoc.Delim, "EOF")
	}
	if !tokens[1].Heredoc.Expand {
		t.Error("expected Expand=true for unquoted delimiter")
	}
	if tokens[1].Heredoc.StripTabs {
		t.Error("expected StripTabs=false")
	}
}

func TestHeredocQuotedDelim(t *testing.T) {
	tokens, err := Lex("cat <<'END'")
	if err != nil {
		t.Fatal(err)
	}
	if tokens[1].Heredoc.Expand {
		t.Error("expected Expand=false for single-quoted delimiter")
	}
}

func TestHeredocDoubleQuotedDelim(t *testing.T) {
	tokens, err := Lex(`cat <<"END"`)
	if err != nil {
		t.Fatal(err)
	}
	if tokens[1].Heredoc.Expand {
		t.Error("expected Expand=false for double-quoted delimiter")
	}
	if tokens[1].Heredoc.Delim != "END" {
		t.Errorf("delim: got %q, want %q", tokens[1].Heredoc.Delim, "END")
	}
}

func TestHeredocStripTabs(t *testing.T) {
	tokens, err := Lex("cat <<-EOF")
	if err != nil {
		t.Fatal(err)
	}
	if !tokens[1].Heredoc.StripTabs {
		t.Error("expected StripTabs=true for <<-")
	}
	if !tokens[1].Heredoc.Expand {
		t.Error("expected Expand=true")
	}
}

func TestHeredocStripTabsQuoted(t *testing.T) {
	tokens, err := Lex("cat <<-'EOF'")
	if err != nil {
		t.Fatal(err)
	}
	if !tokens[1].Heredoc.StripTabs {
		t.Error("expected StripTabs=true")
	}
	if tokens[1].Heredoc.Expand {
		t.Error("expected Expand=false for quoted delimiter")
	}
}

func TestHeredocInPipeline(t *testing.T) {
	tokens, err := Lex("cat <<EOF | wc -l")
	if err != nil {
		t.Fatal(err)
	}
	expectTokenTypes(t, tokens,
		TOKEN_WORD,    // cat
		TOKEN_HEREDOC, // <<EOF
		TOKEN_PIPE,    // |
		TOKEN_WORD,    // wc
		TOKEN_WORD,    // -l
		TOKEN_EOF,
	)
}

func TestHeredocFdAbsorb(t *testing.T) {
	tokens, err := Lex("cmd 0<<EOF")
	if err != nil {
		t.Fatal(err)
	}
	if tokens[1].Type != TOKEN_HEREDOC {
		t.Fatalf("expected TOKEN_HEREDOC, got %s", tokens[1].Type)
	}
	if tokens[1].Fd != 0 {
		t.Errorf("fd: got %d, want 0", tokens[1].Fd)
	}
}

func TestHeredocNewlineSuppressed(t *testing.T) {
	// Newline after <<EOF should not produce TOKEN_SEMI
	tokens, err := Lex("cat <<EOF\nhello\nEOF")
	if err != nil {
		t.Fatal(err)
	}
	// Should have: cat, <<EOF, hello, SEMI, EOF-word, EOF-token
	// The newline after <<EOF is suppressed, but "hello" and
	// "EOF" appear as regular words separated by SEMI from newlines.
	found := false
	for _, tok := range tokens {
		if tok.Type == TOKEN_HEREDOC {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TOKEN_HEREDOC in token stream")
	}
}

func TestResolveHeredocsBasic(t *testing.T) {
	tokens, err := Lex("cat <<EOF")
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{"hello world", "second line", "EOF"}
	idx := 0
	err = ResolveHeredocs(tokens, func() (string, bool) {
		if idx >= len(lines) {
			return "", false
		}
		l := lines[idx]
		idx++
		return l, true
	})
	if err != nil {
		t.Fatal(err)
	}

	body := tokens[1].Heredoc.Body.String()
	want := "hello world\nsecond line\n"
	if body != want {
		t.Errorf("body: got %q, want %q", body, want)
	}
}

func TestResolveHeredocsStripTabs(t *testing.T) {
	tokens, err := Lex("cat <<-END")
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{"\thello", "\t\tindented", "\tEND"}
	idx := 0
	err = ResolveHeredocs(tokens, func() (string, bool) {
		if idx >= len(lines) {
			return "", false
		}
		l := lines[idx]
		idx++
		return l, true
	})
	if err != nil {
		t.Fatal(err)
	}

	body := tokens[1].Heredoc.Body.String()
	want := "hello\nindented\n"
	if body != want {
		t.Errorf("body: got %q, want %q", body, want)
	}
}

func TestResolveHeredocsEOF(t *testing.T) {
	tokens, err := Lex("cat <<EOF")
	if err != nil {
		t.Fatal(err)
	}

	err = ResolveHeredocs(tokens, func() (string, bool) {
		return "", false
	})
	if err == nil {
		t.Fatal("expected error for missing delimiter")
	}
}

func TestResolveHeredocsLiteral(t *testing.T) {
	tokens, err := Lex("cat <<'EOF'")
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{"hello $VAR", "EOF"}
	idx := 0
	err = ResolveHeredocs(tokens, func() (string, bool) {
		if idx >= len(lines) {
			return "", false
		}
		l := lines[idx]
		idx++
		return l, true
	})
	if err != nil {
		t.Fatal(err)
	}

	// Literal heredoc: body should be SingleQuoted
	if len(tokens[1].Heredoc.Body) != 1 {
		t.Fatalf("expected 1 part, got %d", len(tokens[1].Heredoc.Body))
	}
	if tokens[1].Heredoc.Body[0].Quote != SingleQuoted {
		t.Errorf("expected SingleQuoted, got %d", tokens[1].Heredoc.Body[0].Quote)
	}
}

func TestResolveHeredocsExpandable(t *testing.T) {
	tokens, err := Lex("cat <<EOF")
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{"hello $VAR", "EOF"}
	idx := 0
	err = ResolveHeredocs(tokens, func() (string, bool) {
		if idx >= len(lines) {
			return "", false
		}
		l := lines[idx]
		idx++
		return l, true
	})
	if err != nil {
		t.Fatal(err)
	}

	// Expandable heredoc: body should contain DoubleQuoted parts
	// (parsed by LexHeredocBody).
	body := tokens[1].Heredoc.Body
	if len(body) == 0 {
		t.Fatal("expected non-empty body parts")
	}
	found := false
	for _, p := range body {
		if p.Quote == DoubleQuoted {
			found = true
		}
	}
	if !found {
		t.Error("expected at least one DoubleQuoted part in expandable heredoc")
	}
}

func TestLexHeredocBodyCmdSubst(t *testing.T) {
	parts, err := LexHeredocBody("hello $(echo world)\n")
	if err != nil {
		t.Fatal(err)
	}

	// Should have: DoubleQuoted("hello "), CmdSubstDQ("echo world"), DoubleQuoted("\n")
	foundCmd := false
	for _, p := range parts {
		if p.Quote == CmdSubstDQ && p.Text == "echo world" {
			foundCmd = true
		}
	}
	if !foundCmd {
		t.Errorf("expected CmdSubstDQ part, got %+v", parts)
	}
}

func TestLexHeredocBodyArith(t *testing.T) {
	parts, err := LexHeredocBody("result: $((2+3))\n")
	if err != nil {
		t.Fatal(err)
	}

	foundArith := false
	for _, p := range parts {
		if p.Quote == ArithSubstDQ && p.Text == "2+3" {
			foundArith = true
		}
	}
	if !foundArith {
		t.Errorf("expected ArithSubstDQ part, got %+v", parts)
	}
}

func TestResolveHeredocsEmpty(t *testing.T) {
	tokens, err := Lex("cat <<EOF")
	if err != nil {
		t.Fatal(err)
	}

	lines := []string{"EOF"}
	idx := 0
	err = ResolveHeredocs(tokens, func() (string, bool) {
		if idx >= len(lines) {
			return "", false
		}
		l := lines[idx]
		idx++
		return l, true
	})
	if err != nil {
		t.Fatal(err)
	}

	body := tokens[1].Heredoc.Body.String()
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}
