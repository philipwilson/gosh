package expander

import (
	"gosh/lexer"
	"gosh/parser"
	"os"
	"path/filepath"
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

// --- Variable expansion tests ---

func TestExpandUnquoted(t *testing.T) {
	list := mustParse(t, "echo $HOME")
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "/home/user")
}

func TestExpandDoubleQuoted(t *testing.T) {
	list := mustParse(t, `echo "$HOME"`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "/home/user")
}

func TestExpandSingleQuotedNoExpansion(t *testing.T) {
	list := mustParse(t, "echo '$HOME'")
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "$HOME")
}

func TestExpandBackslashDollarNoExpansion(t *testing.T) {
	list := mustParse(t, `echo \$HOME`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "$HOME")
}

func TestExpandDoubleQuoteBackslashDollar(t *testing.T) {
	list := mustParse(t, `echo "\$HOME"`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "$HOME")
}

func TestExpandBraces(t *testing.T) {
	list := mustParse(t, `echo "${HOME}/bin"`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "/home/user/bin")
}

func TestExpandMixed(t *testing.T) {
	list := mustParse(t, `echo "hello $USER, welcome"`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello alice, welcome")
}

func TestExpandMultipleVars(t *testing.T) {
	list := mustParse(t, `echo $USER@$HOME`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "alice@/home/user")
}

func TestExpandUndefined(t *testing.T) {
	// Unquoted $UNDEFINED expands to empty and is removed by word splitting.
	list := mustParse(t, `echo $UNDEFINED`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo")
}

func TestExpandExitStatus(t *testing.T) {
	list := mustParse(t, `echo $?`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "0")
}

func TestExpandShellPid(t *testing.T) {
	list := mustParse(t, `echo $$`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "12345")
}

func TestExpandBareDollar(t *testing.T) {
	list := mustParse(t, `echo $ `)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "$")
}

func TestExpandAssignmentValue(t *testing.T) {
	list := mustParse(t, `DIR=$HOME/bin`)
	Expand(list, testLookup, nil, nil, nil)
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Assigns) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(cmd.Assigns))
	}
	if cmd.Assigns[0].Value.String() != "/home/user/bin" {
		t.Errorf("expected /home/user/bin, got %q", cmd.Assigns[0].Value)
	}
}

func TestExpandRedirectFilename(t *testing.T) {
	list := mustParse(t, `echo hi > $HOME/out.txt`)
	Expand(list, testLookup, nil, nil, nil)
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if cmd.Redirects[0].File.String() != "/home/user/out.txt" {
		t.Errorf("expected /home/user/out.txt, got %q", cmd.Redirects[0].File)
	}
}

func TestExpandMixedQuoting(t *testing.T) {
	list := mustParse(t, `he"$USER"'$HOME'`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "healice$HOME")
}

// --- Tilde expansion tests ---

func TestTildeAlone(t *testing.T) {
	list := mustParse(t, "echo ~")
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "/home/user")
}

func TestTildeSlashPath(t *testing.T) {
	list := mustParse(t, "echo ~/bin")
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "/home/user/bin")
}

func TestTildeQuotedNoExpansion(t *testing.T) {
	list := mustParse(t, `echo "~"`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "~")
}

func TestTildeSingleQuotedNoExpansion(t *testing.T) {
	list := mustParse(t, "echo '~/bin'")
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "~/bin")
}

func TestTildeMidWord(t *testing.T) {
	// ~ only expands at start of word
	list := mustParse(t, "echo foo~bar")
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "foo~bar")
}

func TestTildeInAssignment(t *testing.T) {
	list := mustParse(t, "DIR=~/bin")
	Expand(list, testLookup, nil, nil, nil)
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if cmd.Assigns[0].Value.String() != "/home/user/bin" {
		t.Errorf("expected /home/user/bin, got %q", cmd.Assigns[0].Value)
	}
}

// --- Glob expansion tests ---

// setupGlobDir creates a temp directory with known files for glob testing.
func setupGlobDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"foo.go", "bar.go", "baz.txt", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestGlobStar(t *testing.T) {
	dir := setupGlobDir(t)
	list := mustParse(t, "echo "+dir+"/*.go")
	Expand(list, testLookup, nil, nil, nil)

	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	// Should expand to bar.go and foo.go (sorted)
	args := cmd.ArgStrings()
	if len(args) != 3 {
		t.Fatalf("expected 3 args (echo + 2 files), got %d: %v", len(args), args)
	}
	if args[1] != filepath.Join(dir, "bar.go") {
		t.Errorf("expected bar.go, got %s", args[1])
	}
	if args[2] != filepath.Join(dir, "foo.go") {
		t.Errorf("expected foo.go, got %s", args[2])
	}
}

func TestGlobQuestion(t *testing.T) {
	dir := setupGlobDir(t)
	list := mustParse(t, "echo "+dir+"/ba?.go")
	Expand(list, testLookup, nil, nil, nil)

	args := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0]).ArgStrings()
	if len(args) != 2 {
		t.Fatalf("expected 2 args (echo + bar.go), got %d: %v", len(args), args)
	}
	if args[1] != filepath.Join(dir, "bar.go") {
		t.Errorf("expected bar.go, got %s", args[1])
	}
}

func TestGlobNoMatch(t *testing.T) {
	dir := setupGlobDir(t)
	pattern := dir + "/*.rs"
	list := mustParse(t, "echo "+pattern)
	Expand(list, testLookup, nil, nil, nil)

	// No .rs files exist — glob should keep the pattern as-is.
	args := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0]).ArgStrings()
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[1] != pattern {
		t.Errorf("expected pattern kept as-is %q, got %q", pattern, args[1])
	}
}

func TestGlobQuotedStar(t *testing.T) {
	dir := setupGlobDir(t)
	// Quoted * should NOT glob-expand.
	list := mustParse(t, `echo "`+dir+`/*.go"`)
	Expand(list, testLookup, nil, nil, nil)

	args := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0]).ArgStrings()
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[1] != dir+"/*.go" {
		t.Errorf("expected literal %s/*.go, got %s", dir, args[1])
	}
}

func TestGlobSingleQuotedStar(t *testing.T) {
	dir := setupGlobDir(t)
	list := mustParse(t, "echo '"+dir+"/*.go'")
	Expand(list, testLookup, nil, nil, nil)

	args := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0]).ArgStrings()
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[1] != dir+"/*.go" {
		t.Errorf("expected literal pattern, got %s", args[1])
	}
}

func TestGlobAllFiles(t *testing.T) {
	dir := setupGlobDir(t)
	list := mustParse(t, "echo "+dir+"/*")
	Expand(list, testLookup, nil, nil, nil)

	args := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0]).ArgStrings()
	// echo + 4 files
	if len(args) != 5 {
		t.Fatalf("expected 5 args (echo + 4 files), got %d: %v", len(args), args)
	}
}

// --- Command substitution tests ---

// mockSubst simulates command execution for testing.
func mockSubst(cmd string) (string, error) {
	switch cmd {
	case "whoami":
		return "alice", nil
	case "echo hello":
		return "hello", nil
	case "uname":
		return "Linux", nil
	default:
		return "", nil
	}
}

func TestCmdSubstBasic(t *testing.T) {
	list := mustParse(t, "echo $(whoami)")
	Expand(list, testLookup, mockSubst, nil, nil)
	expectArgs(t, list, 0, "echo", "alice")
}

func TestCmdSubstBacktick(t *testing.T) {
	list := mustParse(t, "echo `whoami`")
	Expand(list, testLookup, mockSubst, nil, nil)
	expectArgs(t, list, 0, "echo", "alice")
}

func TestCmdSubstInDoubleQuotes(t *testing.T) {
	list := mustParse(t, `echo "hello $(whoami)"`)
	Expand(list, testLookup, mockSubst, nil, nil)
	expectArgs(t, list, 0, "echo", "hello alice")
}

func TestCmdSubstMixedWithText(t *testing.T) {
	list := mustParse(t, "echo pre$(whoami)post")
	Expand(list, testLookup, mockSubst, nil, nil)
	expectArgs(t, list, 0, "echo", "prealicepost")
}

func TestCmdSubstInAssignment(t *testing.T) {
	list := mustParse(t, "USER=$(whoami)")
	Expand(list, testLookup, mockSubst, nil, nil)
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	if len(cmd.Assigns) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(cmd.Assigns))
	}
	if cmd.Assigns[0].Value.String() != "alice" {
		t.Errorf("expected alice, got %q", cmd.Assigns[0].Value)
	}
}

func TestCmdSubstNilSubstFunc(t *testing.T) {
	// With nil SubstFunc, command substitutions are left as-is
	// (the part remains but no replacement happens).
	list := mustParse(t, "echo $(whoami)")
	Expand(list, testLookup, nil, nil, nil)
	// The CmdSubst part should remain with its text "whoami".
	cmd := simpleCmd(t, list.Entries[0].Pipeline.Cmds[0])
	args := cmd.ArgStrings()
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[1] != "whoami" {
		t.Errorf("expected 'whoami' (unexpanded), got %q", args[1])
	}
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

func TestExpandPositionalParams(t *testing.T) {
	lookup := func(name string) string {
		switch name {
		case "1":
			return "hello"
		case "2":
			return "world"
		case "#":
			return "2"
		case "@", "*":
			return "hello world"
		case "0":
			return "gosh"
		}
		return ""
	}

	list := mustParse(t, `echo $1 $2 $# "$@" $0`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello", "world", "2", "hello world", "gosh")
}

// --- Word splitting tests ---

func TestWordSplitBasic(t *testing.T) {
	// $X where X="a b c" → three separate args
	lookup := func(name string) string {
		if name == "X" {
			return "a b c"
		}
		return ""
	}
	list := mustParse(t, `echo $X`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "a", "b", "c")
}

func TestWordSplitNoSplitInDoubleQuotes(t *testing.T) {
	// "$X" prevents word splitting
	lookup := func(name string) string {
		if name == "X" {
			return "a b c"
		}
		return ""
	}
	list := mustParse(t, `echo "$X"`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "a b c")
}

func TestWordSplitEmptyRemoved(t *testing.T) {
	// $EMPTY (empty string, unquoted) is removed
	lookup := func(name string) string { return "" }
	list := mustParse(t, `echo $EMPTY world`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "world")
}

func TestWordSplitEmptyQuotedPreserved(t *testing.T) {
	// "$EMPTY" preserves the empty argument
	lookup := func(name string) string { return "" }
	list := mustParse(t, `echo "$EMPTY" world`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "", "world")
}

func TestWordSplitMixedWithLiteral(t *testing.T) {
	// hello${X}world where X="a b" → "helloa" "bworld"
	lookup := func(name string) string {
		if name == "X" {
			return "a b"
		}
		return ""
	}
	list := mustParse(t, `echo hello${X}world`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "helloa", "bworld")
}

func TestWordSplitLeadingIFS(t *testing.T) {
	// hello$X where X=" a b" → "hello" "a" "b"
	lookup := func(name string) string {
		if name == "X" {
			return " a b"
		}
		return ""
	}
	list := mustParse(t, `echo hello$X`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello", "a", "b")
}

func TestWordSplitMultipleSpaces(t *testing.T) {
	// Consecutive IFS whitespace is collapsed
	lookup := func(name string) string {
		if name == "X" {
			return "a   b"
		}
		return ""
	}
	list := mustParse(t, `echo $X`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "a", "b")
}

func TestWordSplitLeadingTrailingWhitespace(t *testing.T) {
	// Leading/trailing IFS whitespace is trimmed
	lookup := func(name string) string {
		if name == "X" {
			return "  a  b  "
		}
		return ""
	}
	list := mustParse(t, `echo $X`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "a", "b")
}

func TestWordSplitCmdSubst(t *testing.T) {
	// Command substitution results are also split
	subst := func(cmd string) (string, error) {
		return "a b c", nil
	}
	list := mustParse(t, `echo $(cmd)`)
	Expand(list, testLookup, subst, nil, nil)
	expectArgs(t, list, 0, "echo", "a", "b", "c")
}

func TestWordSplitCmdSubstQuoted(t *testing.T) {
	// Quoted command substitution is not split
	subst := func(cmd string) (string, error) {
		return "a b c", nil
	}
	list := mustParse(t, `echo "$(cmd)"`)
	Expand(list, testLookup, subst, nil, nil)
	expectArgs(t, list, 0, "echo", "a b c")
}

func TestWordSplitForLoop(t *testing.T) {
	// Word splitting in for loop word list: $X produces multiple words
	lookup := func(name string) string {
		if name == "X" {
			return "a b c"
		}
		return ""
	}
	list := mustParse(t, `echo $X done`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "a", "b", "c", "done")
}

// --- Parameter expansion tests ---

func TestParamDefault(t *testing.T) {
	// ${var:-default} returns default when var is empty
	list := mustParse(t, `echo ${X:-fallback}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "fallback")
}

func TestParamDefaultSet(t *testing.T) {
	// ${var:-default} returns var when set
	list := mustParse(t, `echo ${USER:-nobody}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "alice")
}

func TestParamDefaultWithExpansion(t *testing.T) {
	// ${var:-$OTHER} expands the default word
	list := mustParse(t, `echo ${X:-$HOME}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "/home/user")
}

func TestParamAlternative(t *testing.T) {
	// ${var:+alt} returns alt when var is set and non-empty
	list := mustParse(t, `echo ${USER:+yes}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "yes")
}

func TestParamAlternativeEmpty(t *testing.T) {
	// ${var:+alt} returns empty when var is unset
	list := mustParse(t, `echo ${X:+yes}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo")
}

func TestParamLength(t *testing.T) {
	// ${#var} returns string length
	list := mustParse(t, `echo ${#USER}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "5") // "alice" = 5
}

func TestParamLengthEmpty(t *testing.T) {
	// ${#var} for unset var returns 0
	list := mustParse(t, `echo ${#UNDEFINED}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "0")
}

func TestParamLengthSpecial(t *testing.T) {
	// ${#?} = length of $?
	list := mustParse(t, `echo ${#?}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "1") // "0" = 1 char
}

func TestParamStripSuffix(t *testing.T) {
	// ${var%pattern} removes shortest suffix
	lookup := func(name string) string {
		if name == "FILE" {
			return "hello.tar.gz"
		}
		return ""
	}
	list := mustParse(t, `echo ${FILE%.*}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello.tar")
}

func TestParamStripSuffixLong(t *testing.T) {
	// ${var%%pattern} removes longest suffix
	lookup := func(name string) string {
		if name == "FILE" {
			return "hello.tar.gz"
		}
		return ""
	}
	list := mustParse(t, `echo ${FILE%%.*}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello")
}

func TestParamStripPrefix(t *testing.T) {
	// ${var#pattern} removes shortest prefix
	lookup := func(name string) string {
		if name == "PATH" {
			return "/usr/local/bin"
		}
		return ""
	}
	list := mustParse(t, `echo ${PATH#*/}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "usr/local/bin")
}

func TestParamStripPrefixLong(t *testing.T) {
	// ${var##pattern} removes longest prefix
	lookup := func(name string) string {
		if name == "PATH" {
			return "/usr/local/bin"
		}
		return ""
	}
	list := mustParse(t, `echo ${PATH##*/}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "bin")
}

func TestParamNoMatch(t *testing.T) {
	// Pattern that doesn't match — value unchanged
	lookup := func(name string) string {
		if name == "X" {
			return "hello"
		}
		return ""
	}
	list := mustParse(t, `echo ${X%*.go}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello")
}

func TestParamErrorMsg(t *testing.T) {
	// ${var:?msg} with unset var returns empty (error goes to stderr)
	list := mustParse(t, `echo ${X:?missing}`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo")
}

func TestParamInDoubleQuotes(t *testing.T) {
	// Parameter expansion works inside double quotes
	list := mustParse(t, `echo "${USER:-nobody}"`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "alice")
}

func TestParamDefaultInDoubleQuotes(t *testing.T) {
	// ${var:-default} in double quotes, var unset
	list := mustParse(t, `echo "${X:-fallback}"`)
	Expand(list, testLookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "fallback")
}

// --- String replacement tests ---

func TestParamReplaceSingle(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hello world"
		}
		return ""
	}
	list := mustParse(t, `echo "${X/world/earth}"`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello earth")
}

func TestParamReplaceAll(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "aabaa"
		}
		return ""
	}
	list := mustParse(t, `echo ${X//a/x}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "xxbxx")
}

func TestParamDeleteFirst(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hello world"
		}
		return ""
	}
	list := mustParse(t, `echo "${X/o}"`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hell world")
}

func TestParamDeleteAll(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hello"
		}
		return ""
	}
	list := mustParse(t, `echo ${X//l}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "heo")
}

func TestParamReplaceGlob(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hello world"
		}
		return ""
	}
	list := mustParse(t, `echo "${X/h*/X}"`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "X")
}

// --- Substring extraction tests ---

func TestSubstringFromStart(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hello world"
		}
		return ""
	}
	list := mustParse(t, `echo ${X:0:5}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello")
}

func TestSubstringOffset(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hello world"
		}
		return ""
	}
	list := mustParse(t, `echo ${X:6}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "world")
}

func TestSubstringNegativeOffset(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hello world"
		}
		return ""
	}
	// Negative offset: space before - to distinguish from ${var:-default}
	list := mustParse(t, `echo "${X: -5}"`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "world")
}

func TestSubstringNegativeLength(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hello world"
		}
		return ""
	}
	list := mustParse(t, `echo "${X:0:-1}"`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo", "hello worl")
}

func TestSubstringBeyondLength(t *testing.T) {
	lookup := func(name string) string {
		if name == "X" {
			return "hi"
		}
		return ""
	}
	list := mustParse(t, `echo ${X:10}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo")
}

func TestSubstringEmpty(t *testing.T) {
	lookup := func(name string) string { return "" }
	list := mustParse(t, `echo ${X:0:3}`)
	Expand(list, lookup, nil, nil, nil)
	expectArgs(t, list, 0, "echo")
}

func simpleCmd(t *testing.T, cmd parser.Command) *parser.SimpleCmd {
	t.Helper()
	sc, ok := cmd.(*parser.SimpleCmd)
	if !ok {
		t.Fatalf("expected *SimpleCmd, got %T", cmd)
	}
	return sc
}

func expectArgs(t *testing.T, list *parser.List, entryIdx int, want ...string) {
	t.Helper()
	cmd := simpleCmd(t, list.Entries[entryIdx].Pipeline.Cmds[0])
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
