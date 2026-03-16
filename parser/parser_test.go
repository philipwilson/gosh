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
	expectArgs(t, pipe.Cmds[0], "echo", "hello", "world")
	cmd := simpleCmd(t, pipe.Cmds[0])
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

	expectArgs(t, list.Entries[0].Pipeline.Cmds[0], "echo", "hello")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Redirects) != 1 {
		t.Fatalf("expected 1 redirect, got %d", len(cmd.Redirects))
	}
	if cmd.Redirects[0].Type != REDIR_OUT {
		t.Errorf("expected REDIR_OUT, got %d", cmd.Redirects[0].Type)
	}
	if cmd.Redirects[0].File.String() != "out.txt" {
		t.Errorf("expected out.txt, got %s", cmd.Redirects[0].File)
	}
}

func TestRedirectIn(t *testing.T) {
	list := mustParse(t, "wc -l < input.txt")

	expectArgs(t, list.Entries[0].Pipeline.Cmds[0], "wc", "-l")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Redirects) != 1 {
		t.Fatalf("expected 1 redirect, got %d", len(cmd.Redirects))
	}
	if cmd.Redirects[0].Type != REDIR_IN {
		t.Errorf("expected REDIR_IN, got %d", cmd.Redirects[0].Type)
	}
	if cmd.Redirects[0].File.String() != "input.txt" {
		t.Errorf("expected input.txt, got %s", cmd.Redirects[0].File)
	}
}

func TestRedirectAppend(t *testing.T) {
	list := mustParse(t, "echo line >> log.txt")

	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Redirects) != 1 {
		t.Fatalf("expected 1 redirect, got %d", len(cmd.Redirects))
	}
	if cmd.Redirects[0].Type != REDIR_APPEND {
		t.Errorf("expected REDIR_APPEND, got %d", cmd.Redirects[0].Type)
	}
}

func TestMultipleRedirects(t *testing.T) {
	list := mustParse(t, "sort < in.txt > out.txt")

	expectArgs(t, list.Entries[0].Pipeline.Cmds[0], "sort")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
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

	if len(simpleCmd(t, pipe.Cmds[0]).Redirects) != 1 {
		t.Errorf("cmd 0: expected 1 redirect, got %d", len(simpleCmd(t, pipe.Cmds[0]).Redirects))
	}
	if len(simpleCmd(t, pipe.Cmds[1]).Redirects) != 0 {
		t.Errorf("cmd 1: expected 0 redirects, got %d", len(simpleCmd(t, pipe.Cmds[1]).Redirects))
	}
	if len(simpleCmd(t, pipe.Cmds[2]).Redirects) != 1 {
		t.Errorf("cmd 2: expected 1 redirect, got %d", len(simpleCmd(t, pipe.Cmds[2]).Redirects))
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
	expectArgs(t, list.Entries[0].Pipeline.Cmds[0], "echo", "hello world", "foo bar")
}

// --- Assignment tests ---

func TestSimpleAssignment(t *testing.T) {
	list := mustParse(t, "FOO=bar")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Assigns) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(cmd.Assigns))
	}
	if cmd.Assigns[0].Name != "FOO" {
		t.Errorf("expected name FOO, got %q", cmd.Assigns[0].Name)
	}
	if cmd.Assigns[0].Value.String() != "bar" {
		t.Errorf("expected value bar, got %q", cmd.Assigns[0].Value)
	}
	if len(cmd.Args) != 0 {
		t.Errorf("expected 0 args, got %d", len(cmd.Args))
	}
}

func TestAssignmentBeforeCommand(t *testing.T) {
	list := mustParse(t, "FOO=bar echo hello")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Assigns) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(cmd.Assigns))
	}
	if cmd.Assigns[0].Name != "FOO" {
		t.Errorf("expected name FOO, got %q", cmd.Assigns[0].Name)
	}
	expectArgs(t, list.Entries[0].Pipeline.Cmds[0], "echo", "hello")
}

func TestMultipleAssignments(t *testing.T) {
	list := mustParse(t, "A=1 B=2 echo hello")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Assigns) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(cmd.Assigns))
	}
	if cmd.Assigns[0].Name != "A" || cmd.Assigns[0].Value.String() != "1" {
		t.Errorf("expected A=1, got %s=%s", cmd.Assigns[0].Name, cmd.Assigns[0].Value)
	}
	if cmd.Assigns[1].Name != "B" || cmd.Assigns[1].Value.String() != "2" {
		t.Errorf("expected B=2, got %s=%s", cmd.Assigns[1].Name, cmd.Assigns[1].Value)
	}
}

func TestAssignmentWithQuotedValue(t *testing.T) {
	list := mustParse(t, `FOO="hello world"`)
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Assigns) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(cmd.Assigns))
	}
	if cmd.Assigns[0].Value.String() != "hello world" {
		t.Errorf("expected value 'hello world', got %q", cmd.Assigns[0].Value)
	}
}

func TestEqualsAfterCommandIsArg(t *testing.T) {
	// After the first non-assignment word, = is just part of an arg.
	list := mustParse(t, "echo FOO=bar")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Assigns) != 0 {
		t.Errorf("expected 0 assignments, got %d", len(cmd.Assigns))
	}
	expectArgs(t, list.Entries[0].Pipeline.Cmds[0], "echo", "FOO=bar")
}

// --- Fd redirect tests ---

func TestStderrRedirectParse(t *testing.T) {
	list := mustParse(t, "cmd 2>err.txt")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Redirects) != 1 {
		t.Fatalf("expected 1 redirect, got %d", len(cmd.Redirects))
	}
	r := cmd.Redirects[0]
	if r.Fd != 2 {
		t.Errorf("expected fd 2, got %d", r.Fd)
	}
	if r.Type != REDIR_OUT {
		t.Errorf("expected REDIR_OUT, got %d", r.Type)
	}
	if r.File.String() != "err.txt" {
		t.Errorf("expected err.txt, got %q", r.File.String())
	}
}

func TestStderrDupParse(t *testing.T) {
	list := mustParse(t, "cmd 2>&1")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Redirects) != 1 {
		t.Fatalf("expected 1 redirect, got %d", len(cmd.Redirects))
	}
	r := cmd.Redirects[0]
	if r.Fd != 2 {
		t.Errorf("expected fd 2, got %d", r.Fd)
	}
	if r.Type != REDIR_DUP {
		t.Errorf("expected REDIR_DUP, got %d", r.Type)
	}
	if r.File.String() != "1" {
		t.Errorf("expected target 1, got %q", r.File.String())
	}
}

func TestDefaultRedirectFdParse(t *testing.T) {
	list := mustParse(t, "echo hi > out.txt")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	r := cmd.Redirects[0]
	if r.Fd != -1 {
		t.Errorf("expected fd -1 (default), got %d", r.Fd)
	}
	if r.Type != REDIR_OUT {
		t.Errorf("expected REDIR_OUT, got %d", r.Type)
	}
}

func TestMultipleRedirectsParse(t *testing.T) {
	list := mustParse(t, "cmd >out.txt 2>&1")
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Redirects) != 2 {
		t.Fatalf("expected 2 redirects, got %d", len(cmd.Redirects))
	}
	if cmd.Redirects[0].Type != REDIR_OUT {
		t.Errorf("first redirect: expected REDIR_OUT")
	}
	if cmd.Redirects[1].Type != REDIR_DUP {
		t.Errorf("second redirect: expected REDIR_DUP")
	}
	if cmd.Redirects[1].Fd != 2 {
		t.Errorf("second redirect: expected fd 2, got %d", cmd.Redirects[1].Fd)
	}
}

// --- If/elif/else tests ---

func TestIfSimple(t *testing.T) {
	list := mustParse(t, "if true; then echo yes; fi")
	cmd := list.Entries[0].Pipeline.Cmds[0]
	ic, ok := cmd.(*IfCmd)
	if !ok {
		t.Fatalf("expected *IfCmd, got %T", cmd)
	}
	if len(ic.Clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(ic.Clauses))
	}
	if ic.ElseBody != nil {
		t.Errorf("expected no else body")
	}
	// Check condition: "true"
	condCmd := simpleCmd(t, ic.Clauses[0].Condition.Entries[0].Pipeline.Cmds[0])
	if condCmd.ArgStrings()[0] != "true" {
		t.Errorf("expected condition 'true', got %q", condCmd.ArgStrings()[0])
	}
	// Check body: "echo yes"
	bodyCmd := simpleCmd(t, ic.Clauses[0].Body.Entries[0].Pipeline.Cmds[0])
	got := bodyCmd.ArgStrings()
	if len(got) != 2 || got[0] != "echo" || got[1] != "yes" {
		t.Errorf("expected body [echo yes], got %v", got)
	}
}

func TestIfElse(t *testing.T) {
	list := mustParse(t, "if false; then echo no; else echo yes; fi")
	ic := list.Entries[0].Pipeline.Cmds[0].(*IfCmd)
	if len(ic.Clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(ic.Clauses))
	}
	if ic.ElseBody == nil {
		t.Fatal("expected else body")
	}
	elseCmd := simpleCmd(t, ic.ElseBody.Entries[0].Pipeline.Cmds[0])
	if elseCmd.ArgStrings()[1] != "yes" {
		t.Errorf("expected else body arg 'yes', got %q", elseCmd.ArgStrings()[1])
	}
}

func TestIfElif(t *testing.T) {
	list := mustParse(t, "if false; then echo 1; elif true; then echo 2; elif false; then echo 3; else echo 4; fi")
	ic := list.Entries[0].Pipeline.Cmds[0].(*IfCmd)
	if len(ic.Clauses) != 3 {
		t.Fatalf("expected 3 clauses (if + 2 elif), got %d", len(ic.Clauses))
	}
	if ic.ElseBody == nil {
		t.Fatal("expected else body")
	}
}

func TestIfMissingThen(t *testing.T) {
	tokens, _ := lexer.Lex("if true; fi")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'then'")
	}
}

func TestIfMissingFi(t *testing.T) {
	tokens, _ := lexer.Lex("if true; then echo yes")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'fi'")
	}
}

func TestIfAfterSemicolon(t *testing.T) {
	list := mustParse(t, "echo before; if true; then echo yes; fi")
	if len(list.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list.Entries))
	}
	expectArgs(t, list.Entries[0].Pipeline.Cmds[0], "echo", "before")
	if _, ok := list.Entries[1].Pipeline.Cmds[0].(*IfCmd); !ok {
		t.Fatalf("expected *IfCmd, got %T", list.Entries[1].Pipeline.Cmds[0])
	}
}

// --- While tests ---

func TestWhileSimple(t *testing.T) {
	list := mustParse(t, "while true; do echo yes; done")
	cmd := list.Entries[0].Pipeline.Cmds[0]
	wc, ok := cmd.(*WhileCmd)
	if !ok {
		t.Fatalf("expected *WhileCmd, got %T", cmd)
	}
	condCmd := simpleCmd(t, wc.Condition.Entries[0].Pipeline.Cmds[0])
	if condCmd.ArgStrings()[0] != "true" {
		t.Errorf("expected condition 'true', got %q", condCmd.ArgStrings()[0])
	}
	bodyCmd := simpleCmd(t, wc.Body.Entries[0].Pipeline.Cmds[0])
	got := bodyCmd.ArgStrings()
	if len(got) != 2 || got[0] != "echo" || got[1] != "yes" {
		t.Errorf("expected body [echo yes], got %v", got)
	}
}

func TestWhileMissingDo(t *testing.T) {
	tokens, _ := lexer.Lex("while true; done")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'do'")
	}
}

func TestWhileMissingDone(t *testing.T) {
	tokens, _ := lexer.Lex("while true; do echo yes")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'done'")
	}
}

func TestWhileAfterCommand(t *testing.T) {
	list := mustParse(t, "echo start; while false; do echo x; done")
	if len(list.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list.Entries))
	}
	if _, ok := list.Entries[1].Pipeline.Cmds[0].(*WhileCmd); !ok {
		t.Fatalf("expected *WhileCmd, got %T", list.Entries[1].Pipeline.Cmds[0])
	}
}

// --- For tests ---

func TestForSimple(t *testing.T) {
	list := mustParse(t, "for x in a b c; do echo $x; done")
	cmd := list.Entries[0].Pipeline.Cmds[0]
	fc, ok := cmd.(*ForCmd)
	if !ok {
		t.Fatalf("expected *ForCmd, got %T", cmd)
	}
	if fc.VarName != "x" {
		t.Errorf("expected var 'x', got %q", fc.VarName)
	}
	if len(fc.Words) != 3 {
		t.Fatalf("expected 3 words, got %d", len(fc.Words))
	}
	for i, want := range []string{"a", "b", "c"} {
		if fc.Words[i].String() != want {
			t.Errorf("word %d: expected %q, got %q", i, want, fc.Words[i].String())
		}
	}
}

func TestForEmptyList(t *testing.T) {
	list := mustParse(t, "for x in; do echo $x; done")
	fc := list.Entries[0].Pipeline.Cmds[0].(*ForCmd)
	if len(fc.Words) != 0 {
		t.Errorf("expected 0 words, got %d", len(fc.Words))
	}
}

func TestForMissingIn(t *testing.T) {
	tokens, _ := lexer.Lex("for x do echo $x; done")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'in'")
	}
}

func TestForMissingDo(t *testing.T) {
	tokens, _ := lexer.Lex("for x in a b c; echo $x; done")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'do'")
	}
}

func TestForMissingDone(t *testing.T) {
	tokens, _ := lexer.Lex("for x in a b c; do echo $x")
	_, err := Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'done'")
	}
}

// --- helpers ---

func simpleCmd(t *testing.T, cmd Command) *SimpleCmd {
	t.Helper()
	sc, ok := cmd.(*SimpleCmd)
	if !ok {
		t.Fatalf("expected *SimpleCmd, got %T", cmd)
	}
	return sc
}

func TestCaseSimple(t *testing.T) {
	list := mustParse(t, "case x in\nfoo) echo yes;;\nesac")
	pipe := list.Entries[0].Pipeline
	cmd, ok := pipe.Cmds[0].(*CaseCmd)
	if !ok {
		t.Fatalf("expected *CaseCmd, got %T", pipe.Cmds[0])
	}
	if cmd.Word.String() != "x" {
		t.Errorf("expected word 'x', got %q", cmd.Word.String())
	}
	if len(cmd.Clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(cmd.Clauses))
	}
	cl := cmd.Clauses[0]
	if len(cl.Patterns) != 1 || cl.Patterns[0].String() != "foo" {
		t.Errorf("expected pattern 'foo', got %v", cl.Patterns)
	}
	if len(cl.Body.Entries) != 1 {
		t.Errorf("expected 1 body entry, got %d", len(cl.Body.Entries))
	}
}

func TestCaseMultiplePatterns(t *testing.T) {
	list := mustParse(t, "case x in\na | b | c) echo match;;\nesac")
	cmd, ok := list.Entries[0].Pipeline.Cmds[0].(*CaseCmd)
	if !ok {
		t.Fatalf("expected *CaseCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if len(cmd.Clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(cmd.Clauses))
	}
	cl := cmd.Clauses[0]
	if len(cl.Patterns) != 3 {
		t.Fatalf("expected 3 patterns, got %d", len(cl.Patterns))
	}
	for i, want := range []string{"a", "b", "c"} {
		if cl.Patterns[i].String() != want {
			t.Errorf("pattern %d: expected %q, got %q", i, want, cl.Patterns[i].String())
		}
	}
}

func TestCaseMultipleClauses(t *testing.T) {
	list := mustParse(t, "case x in\nfoo) echo a;;\nbar) echo b;;\n*) echo c;;\nesac")
	cmd, ok := list.Entries[0].Pipeline.Cmds[0].(*CaseCmd)
	if !ok {
		t.Fatalf("expected *CaseCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if len(cmd.Clauses) != 3 {
		t.Fatalf("expected 3 clauses, got %d", len(cmd.Clauses))
	}
}

func TestCaseLastClauseNoSemi(t *testing.T) {
	// Last clause before esac doesn't need ;;
	list := mustParse(t, "case x in\nfoo) echo yes\nesac")
	cmd, ok := list.Entries[0].Pipeline.Cmds[0].(*CaseCmd)
	if !ok {
		t.Fatalf("expected *CaseCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if len(cmd.Clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(cmd.Clauses))
	}
}

func TestCaseMissingEsac(t *testing.T) {
	tokens, err := lexer.Lex("case x in\nfoo) echo yes;;")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'esac'")
	}
}

func TestCaseMissingIn(t *testing.T) {
	tokens, err := lexer.Lex("case x\nfoo) echo yes;;\nesac")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing 'in'")
	}
}

func TestFuncDefSimple(t *testing.T) {
	tokens, err := lexer.Lex("greet() { echo hello; }")
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list.Entries))
	}
	fd, ok := list.Entries[0].Pipeline.Cmds[0].(*FuncDef)
	if !ok {
		t.Fatalf("expected FuncDef, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if fd.Name != "greet" {
		t.Errorf("expected name 'greet', got %q", fd.Name)
	}
	if len(fd.Body.Entries) != 1 {
		t.Fatalf("expected 1 body entry, got %d", len(fd.Body.Entries))
	}
	expectArgs(t, fd.Body.Entries[0].Pipeline.Cmds[0], "echo", "hello")
}

func TestFuncDefMultiLine(t *testing.T) {
	tokens, err := lexer.Lex("greet() {\n  echo hello\n  echo world\n}")
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	fd, ok := list.Entries[0].Pipeline.Cmds[0].(*FuncDef)
	if !ok {
		t.Fatalf("expected FuncDef, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if len(fd.Body.Entries) != 2 {
		t.Fatalf("expected 2 body entries, got %d", len(fd.Body.Entries))
	}
}

func TestFuncDefMissingBrace(t *testing.T) {
	tokens, err := lexer.Lex("greet() { echo hello")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Parse(tokens)
	if err == nil {
		t.Fatal("expected error for missing '}'")
	}
}

func expectArgs(t *testing.T, cmd Command, want ...string) {
	t.Helper()
	sc := simpleCmd(t, cmd)
	got := sc.ArgStrings()
	if len(got) != len(want) {
		t.Fatalf("expected args %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("arg %d: expected %q, got %q", i, w, got[i])
		}
	}
}

func TestSubshell(t *testing.T) {
	tokens, err := lexer.Lex("(echo hello)")
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list.Entries))
	}
	sub, ok := list.Entries[0].Pipeline.Cmds[0].(*SubshellCmd)
	if !ok {
		t.Fatalf("expected SubshellCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if len(sub.Body.Entries) != 1 {
		t.Fatalf("expected 1 entry in subshell body, got %d", len(sub.Body.Entries))
	}
}

func TestSubshellMultipleCommands(t *testing.T) {
	tokens, err := lexer.Lex("(echo a; echo b)")
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	sub, ok := list.Entries[0].Pipeline.Cmds[0].(*SubshellCmd)
	if !ok {
		t.Fatalf("expected SubshellCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if len(sub.Body.Entries) != 2 {
		t.Fatalf("expected 2 entries in subshell body, got %d", len(sub.Body.Entries))
	}
}

func TestSubshellInList(t *testing.T) {
	tokens, err := lexer.Lex("(echo a) && echo b")
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list.Entries))
	}
	_, ok := list.Entries[0].Pipeline.Cmds[0].(*SubshellCmd)
	if !ok {
		t.Fatalf("expected SubshellCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if list.Entries[0].Op != "&&" {
		t.Errorf("expected op '&&', got %q", list.Entries[0].Op)
	}
}

func TestDblBracket(t *testing.T) {
	tokens, err := lexer.Lex(`[[ $x == hello ]]`)
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list.Entries))
	}
	db, ok := list.Entries[0].Pipeline.Cmds[0].(*DblBracketCmd)
	if !ok {
		t.Fatalf("expected DblBracketCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	// Should have 3 items: $x, ==, hello
	if len(db.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(db.Items))
	}
}

func TestDblBracketWithLogical(t *testing.T) {
	tokens, err := lexer.Lex(`[[ -f foo && -d bar ]]`)
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	db, ok := list.Entries[0].Pipeline.Cmds[0].(*DblBracketCmd)
	if !ok {
		t.Fatalf("expected DblBracketCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	// Should have 5 items: -f, foo, &&, -d, bar
	if len(db.Items) != 5 {
		t.Fatalf("expected 5 items, got %d: %v", len(db.Items), db)
	}
	if db.Items[2].String() != "&&" {
		t.Errorf("expected '&&', got %q", db.Items[2].String())
	}
}

func TestDblBracketInList(t *testing.T) {
	tokens, err := lexer.Lex(`[[ -n hello ]] && echo yes`)
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list.Entries))
	}
	_, ok := list.Entries[0].Pipeline.Cmds[0].(*DblBracketCmd)
	if !ok {
		t.Fatalf("expected DblBracketCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if list.Entries[0].Op != "&&" {
		t.Errorf("expected op '&&', got %q", list.Entries[0].Op)
	}
}

func TestArithCmdParse(t *testing.T) {
	tokens, err := lexer.Lex("(( x + 1 ))")
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	ac, ok := list.Entries[0].Pipeline.Cmds[0].(*ArithCmd)
	if !ok {
		t.Fatalf("expected ArithCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if ac.Expr != "x + 1" {
		t.Errorf("expected expr %q, got %q", "x + 1", ac.Expr)
	}
}

func TestArithFor(t *testing.T) {
	tokens, err := lexer.Lex("for ((i=0; i<5; i++)); do echo $i; done")
	if err != nil {
		t.Fatal(err)
	}
	list, err := Parse(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list.Entries))
	}
	af, ok := list.Entries[0].Pipeline.Cmds[0].(*ArithForCmd)
	if !ok {
		t.Fatalf("expected ArithForCmd, got %T", list.Entries[0].Pipeline.Cmds[0])
	}
	if af.Init != "i=0" {
		t.Errorf("expected init %q, got %q", "i=0", af.Init)
	}
	if af.Cond != "i<5" {
		t.Errorf("expected cond %q, got %q", "i<5", af.Cond)
	}
	if af.Step != "i++" {
		t.Errorf("expected step %q, got %q", "i++", af.Step)
	}
}
