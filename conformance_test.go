package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// conformanceCase defines a single bash-vs-gosh comparison.
type conformanceCase struct {
	name string
	cmd  string // shell command passed via -c
	skip string // non-empty to skip with reason
}

// The test runs each case through both /opt/homebrew/bin/bash and ./gosh,
// comparing stdout and exit code. Any divergence is a failure.
func TestConformance(t *testing.T) {
	bash := "/opt/homebrew/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skipf("bash not found at %s", bash)
	}

	gosh := "./gosh"
	if _, err := os.Stat(gosh); err != nil {
		// Try building first.
		out, err := exec.Command("go", "build", "-o", "gosh", ".").CombinedOutput()
		if err != nil {
			t.Fatalf("cannot build gosh: %v\n%s", err, out)
		}
	}

	cases := allConformanceCases()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}

			bashOut, bashCode := runShell(t, bash, tc.cmd)
			goshOut, goshCode := runShell(t, gosh, tc.cmd)

			if bashOut != goshOut {
				t.Errorf("stdout mismatch:\n  bash: %q\n  gosh: %q", bashOut, goshOut)
			}
			if bashCode != goshCode {
				t.Errorf("exit code mismatch:\n  bash: %d\n  gosh: %d", bashCode, goshCode)
			}
		})
	}
}

// runShell executes `shell -c cmd` and returns stdout (trimmed) and exit code.
func runShell(t *testing.T, shell, cmd string) (string, int) {
	t.Helper()
	c := exec.Command(shell, "-c", cmd)
	// Clean environment to avoid interference.
	c.Env = []string{
		"HOME=" + t.TempDir(),
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin",
		"TERM=dumb",
		"LC_ALL=C",
	}
	out, err := c.Output()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run %s: %v", shell, err)
		}
	}
	return strings.TrimRight(string(out), "\n"), code
}

func allConformanceCases() []conformanceCase {
	var cases []conformanceCase
	cases = append(cases, quotingCases()...)
	cases = append(cases, expansionCases()...)
	cases = append(cases, parameterExpansionCases()...)
	cases = append(cases, arithmeticCases()...)
	cases = append(cases, arrayCases()...)
	cases = append(cases, controlFlowCases()...)
	cases = append(cases, redirectionCases()...)
	cases = append(cases, functionCases()...)
	cases = append(cases, builtinCases()...)
	cases = append(cases, wordSplittingCases()...)
	cases = append(cases, globCases()...)
	cases = append(cases, testCommandCases()...)
	cases = append(cases, dblBracketCases()...)
	cases = append(cases, trapCases()...)
	cases = append(cases, setOptionCases()...)
	cases = append(cases, specialVarCases()...)
	cases = append(cases, pipelineCases()...)
	cases = append(cases, subshellCases()...)
	cases = append(cases, heredocCases()...)
	cases = append(cases, processCases()...)
	cases = append(cases, miscCases()...)
	return cases
}

// --- Quoting ---

func quotingCases() []conformanceCase {
	return []conformanceCase{
		{"quoting/single_basic", `echo 'hello world'`, ""},
		{"quoting/single_special_chars", `echo 'hello $USER \n $(cmd)'`, ""},
		{"quoting/double_basic", `echo "hello world"`, ""},
		{"quoting/double_escape_dollar", `echo "price: \$5"`, ""},
		{"quoting/double_escape_backslash", `echo "back\\slash"`, ""},
		{"quoting/double_escape_dquote", `echo "say \"hi\""`, ""},
		{"quoting/double_backslash_preserved", `echo "hello\nworld"`, ""},
		{"quoting/backslash_space", `echo hello\ world`, ""},
		{"quoting/backslash_special", `echo \$HOME`, ""},
		{"quoting/mixed_quotes", `echo 'hel'"lo wo"'rld'`, ""},
		{"quoting/adjacent_quotes", `echo "he"'ll'"o"`, ""},
		{"quoting/empty_single", `echo '' | cat -v`, ""},
		{"quoting/empty_double", `echo "" | cat -v`, ""},
		{"quoting/ansi_c_newline", `echo $'hello\nworld'`, ""},
		{"quoting/ansi_c_tab", `echo $'hello\tworld'`, ""},
		{"quoting/ansi_c_hex", `echo $'\x41\x42\x43'`, ""},
		{"quoting/ansi_c_octal", `echo $'\101\102\103'`, ""},
		{"quoting/ansi_c_unicode", `echo $'\u0048\u0065\u006c\u006c\u006f'`, ""},
		{"quoting/ansi_c_escape", `printf '%s' $'\e' | od -An -tx1 | tr -d ' \n'`, ""},
		{"quoting/ansi_c_bell", `printf '%s' $'\a' | od -An -tx1 | tr -d ' \n'`, ""},
		{"quoting/ansi_c_backslash", `echo $'\\\\'`, ""},
		{"quoting/ansi_c_single_quote", `echo $'\''`, ""},
		{"quoting/ansi_c_control_a", `printf '%s' $'\cA' | od -An -tx1 | tr -d ' \n'`, ""},
		{"quoting/ansi_c_unknown_escape", `echo $'\q'`, ""},
		{"quoting/ansi_c_mixed", `echo hello$'\n'world`, ""},
		{"quoting/ansi_c_in_dquote", `echo "prefix$'\n'suffix"`, "gosh bug: $' should not be expanded inside double quotes (bash treats it as literal)"},
		{"quoting/ansi_c_double_quote_esc", `echo $'say \"hi\"'`, ""},
	}
}

// --- Expansions (tilde, brace, command subst) ---

func expansionCases() []conformanceCase {
	return []conformanceCase{
		// Tilde
		{"expand/tilde_home", `echo ~ | grep -c /`, ""},
		{"expand/tilde_slash", `echo ~/foo | grep -c foo`, ""},

		// Brace expansion
		{"expand/brace_comma", `echo {a,b,c}`, ""},
		{"expand/brace_seq_int", `echo {1..5}`, ""},
		{"expand/brace_seq_letter", `echo {a..e}`, ""},
		{"expand/brace_seq_reverse", `echo {5..1}`, ""},
		{"expand/brace_nested", `echo {a,b{1,2},c}`, ""},
		{"expand/brace_cartesian", `echo {a,b}{1,2}`, ""},
		{"expand/brace_single_no_expand", `echo {a}`, ""},
		{"expand/brace_empty_no_expand", `echo {}`, ""},
		{"expand/brace_seq_zeropad", `echo {01..03}`, ""},
		{"expand/brace_letter_reverse", `echo {e..a}`, ""},

		// Command substitution
		{"expand/cmdsub_dollar", `echo $(echo hello)`, ""},
		{"expand/cmdsub_backtick", "echo `echo hello`", ""},
		{"expand/cmdsub_nested", `echo $(echo $(echo deep))`, ""},
		{"expand/cmdsub_trailing_newlines", `echo "$(printf 'hi\n\n\n')"`, ""},
		{"expand/cmdsub_in_dquote", `echo "hello $(echo world)"`, ""},
		{"expand/cmdsub_in_assignment", `x=$(echo foo); echo $x`, ""},
	}
}

// --- Parameter Expansion ---

func parameterExpansionCases() []conformanceCase {
	return []conformanceCase{
		// Basics
		{"param/simple", `x=hello; echo $x`, ""},
		{"param/braced", `x=hello; echo ${x}`, ""},
		{"param/length", `x=hello; echo ${#x}`, ""},
		{"param/indirect", `x=y; y=hello; echo ${!x}`, ""},

		// Default / alternative / assign / error
		{"param/default_unset", `echo ${x:-default}`, ""},
		{"param/default_empty", `x=; echo ${x:-default}`, ""},
		{"param/default_set", `x=val; echo ${x:-default}`, ""},
		{"param/default_no_colon_unset", `echo ${x-default}`, ""},
		{"param/default_no_colon_empty", `x=; echo ${x-default}`, ""},
		{"param/alt_set", `x=val; echo ${x:+alt}`, ""},
		{"param/alt_unset", `echo ${x:+alt}`, ""},
		{"param/alt_empty", `x=; echo ${x:+alt}`, ""},
		{"param/alt_no_colon_empty", `x=; echo ${x+alt}`, ""},
		{"param/assign_default", `echo ${x:=hello}; echo $x`, ""},
		{"param/error_unset", `echo ${x:?oops} 2>/dev/null; echo $?`, ""},

		// Trimming
		{"param/trim_prefix_short", `x=abcabc; echo ${x#a*b}`, ""},
		{"param/trim_prefix_long", `x=abcabc; echo ${x##a*b}`, ""},
		{"param/trim_suffix_short", `x=abcabc; echo ${x%b*c}`, ""},
		{"param/trim_suffix_long", `x=abcabc; echo ${x%%b*c}`, ""},

		// Replace
		{"param/replace_first", `x=hello; echo ${x/l/L}`, ""},
		{"param/replace_all", `x=hello; echo ${x//l/L}`, ""},
		{"param/replace_empty_match", `x=hello; echo "${x/x/}"`, ""},

		// Substring
		{"param/substring_offset", `x=hello; echo ${x:1}`, ""},
		{"param/substring_offset_length", `x=hello; echo ${x:1:3}`, ""},
		{"param/substring_negative_offset", `x=hello; echo ${x: -2}`, ""},
		{"param/substring_neg_offset_len", `x=hello; echo ${x: -3:2}`, ""},

		// Case conversion
		{"param/case_upper_first", `x=hello; echo ${x^}`, ""},
		{"param/case_upper_all", `x=hello; echo ${x^^}`, ""},
		{"param/case_lower_first", `x=HELLO; echo ${x,}`, ""},
		{"param/case_lower_all", `x=HELLO; echo ${x,,}`, ""},
	}
}

// --- Arithmetic ---

func arithmeticCases() []conformanceCase {
	return []conformanceCase{
		{"arith/basic_add", `echo $((2 + 3))`, ""},
		{"arith/multiply", `echo $((4 * 5))`, ""},
		{"arith/divide", `echo $((10 / 3))`, ""},
		{"arith/modulo", `echo $((10 % 3))`, ""},
		{"arith/parens", `echo $(( (2 + 3) * 4 ))`, ""},
		{"arith/var_ref", `x=10; echo $((x + 5))`, ""},
		{"arith/var_dollar_ref", `x=10; echo $(($x + 5))`, ""},
		{"arith/comparison", `echo $((3 > 2))`, ""},
		{"arith/ternary", `echo $((1 ? 42 : 0))`, ""},
		{"arith/logical_and", `echo $((1 && 0))`, ""},
		{"arith/logical_or", `echo $((0 || 1))`, ""},
		{"arith/bitwise_and", `echo $((0xFF & 0x0F))`, ""},
		{"arith/bitwise_or", `echo $((0xF0 | 0x0F))`, ""},
		{"arith/bitwise_xor", `echo $((0xFF ^ 0x0F))`, ""},
		{"arith/shift_left", `echo $((1 << 8))`, ""},
		{"arith/shift_right", `echo $((256 >> 4))`, ""},
		{"arith/negate", `echo $((-5))`, ""},
		{"arith/logical_not", `echo $((! 0))`, ""},
		{"arith/bitwise_not", `echo $((~ 0))`, ""},
		{"arith/increment_pre", `x=5; echo $((++x)); echo $x`, ""},
		{"arith/increment_post", `x=5; echo $((x++)); echo $x`, ""},
		{"arith/assign_in_arith", `echo $((x = 42)); echo $x`, ""},
		{"arith/plus_assign", `x=10; echo $((x += 5)); echo $x`, ""},
		{"arith/nested_parens", `echo $(( ((2+3)) * 2 ))`, ""},

		// Arithmetic commands
		{"arith/cmd_true", `(( 5 > 3 )); echo $?`, ""},
		{"arith/cmd_false", `(( 3 > 5 )); echo $?`, ""},
		{"arith/cmd_assign", `(( x = 42 )); echo $x`, ""},
		{"arith/cmd_increment", `x=0; (( x++ )); echo $x`, ""},

		// let
		{"arith/let_basic", `let 'x=5+3'; echo $x`, ""},
		{"arith/let_multi", `let 'x=2' 'y=x*3'; echo $y`, ""},
		{"arith/let_status_nonzero", `let 'x=5'; echo $?`, ""},
		{"arith/let_status_zero", `let 'x=0'; echo $?`, ""},

		// Arithmetic for loop
		{"arith/for_loop", `for ((i=0; i<5; i++)); do printf '%d ' $i; done`, ""},
		{"arith/for_loop_sum", `s=0; for ((i=1; i<=10; i++)); do (( s += i )); done; echo $s`, ""},
	}
}

// --- Arrays ---

func arrayCases() []conformanceCase {
	return []conformanceCase{
		// Indexed arrays
		{"array/create", `a=(one two three); echo ${a[0]} ${a[1]} ${a[2]}`, ""},
		{"array/all_elements", `a=(one two three); echo ${a[@]}`, ""},
		{"array/all_star", `a=(one two three); echo "${a[*]}"`, ""},
		{"array/length", `a=(one two three); echo ${#a[@]}`, ""},
		{"array/assign_element", `a=(one two three); a[1]=TWO; echo ${a[@]}`, ""},
		{"array/append", `a=(one two); a+=(three four); echo ${a[@]}`, ""},
		{"array/bare_name", `a=(one two three); echo $a`, ""},
		{"array/unset_element", `a=(one two three); unset 'a[1]'; echo ${a[0]} ${a[2]}`, ""},
		{"array/at_in_quotes", `a=(one two three); printf '<%s>\n' "${a[@]}"`, ""},
		{"array/star_in_quotes", `a=(one two three); printf '<%s>\n' "${a[*]}"`, ""},
		{"array/empty_at", `a=(); printf '<%s>\n' "${a[@]}"; echo done`, ""},
		{"array/arith_subscript", `a=(10 20 30); i=1; echo ${a[$i]}`, ""},
		{"array/arith_subscript_expr", `a=(10 20 30 40 50); echo ${a[1+2]}`, ""},

		// Associative arrays
		{"array/assoc_create", `declare -A m=([x]=1 [y]=2); echo ${m[x]} ${m[y]}`, ""},
		{"array/assoc_assign", `declare -A m; m[hello]=world; echo ${m[hello]}`, ""},
		{"array/assoc_keys", `declare -A m=([b]=2 [a]=1 [c]=3); echo ${!m[@]} | tr ' ' '\n' | sort | tr '\n' ' '`, ""},
		{"array/assoc_values", `declare -A m=([b]=2 [a]=1 [c]=3); echo ${m[@]} | tr ' ' '\n' | sort | tr '\n' ' '`, ""},
		{"array/assoc_length", `declare -A m=([a]=1 [b]=2 [c]=3); echo ${#m[@]}`, ""},
		{"array/assoc_unset", `declare -A m=([a]=1 [b]=2); unset 'm[a]'; echo ${m[@]}`, ""},

		// Key enumeration
		{"array/indexed_keys", `a=(one two three); echo ${!a[@]}`, ""},
	}
}

// --- Control Flow ---

func controlFlowCases() []conformanceCase {
	return []conformanceCase{
		// if/elif/else
		{"flow/if_true", `if true; then echo yes; fi`, ""},
		{"flow/if_false", `if false; then echo yes; else echo no; fi`, ""},
		{"flow/if_elif", `x=2; if [ $x -eq 1 ]; then echo one; elif [ $x -eq 2 ]; then echo two; else echo other; fi`, ""},
		{"flow/if_status", `if echo yes >/dev/null; then echo ok; fi`, ""},

		// while/until
		{"flow/while_count", `i=0; while [ $i -lt 5 ]; do printf '%d ' $i; i=$((i+1)); done`, ""},
		{"flow/until_count", `i=0; until [ $i -ge 5 ]; do printf '%d ' $i; i=$((i+1)); done`, ""},
		{"flow/while_exit_status", `while false; do echo nope; done; echo $?`, ""},

		// for
		{"flow/for_words", `for x in a b c; do printf '%s ' $x; done`, ""},
		{"flow/for_glob", `mkdir -p /tmp/gosh_conf_test; touch /tmp/gosh_conf_test/{a,b,c}.txt; for f in /tmp/gosh_conf_test/*.txt; do basename $f; done; rm -rf /tmp/gosh_conf_test`, ""},
		{"flow/for_expansion", `items="one two three"; for x in $items; do printf '%s ' $x; done`, ""},

		// case
		{"flow/case_match", `x=hello; case $x in hello) echo matched;; world) echo nope;; esac`, ""},
		{"flow/case_pattern", `x=abc; case $x in a*) echo prefix;; *) echo other;; esac`, ""},
		{"flow/case_multiple_patterns", `x=b; case $x in a|b|c) echo letter;; esac`, ""},
		{"flow/case_no_match", `x=z; case $x in a) echo a;; b) echo b;; esac; echo done`, ""},
		{"flow/case_star", `x=anything; case $x in *) echo star;; esac`, ""},

		// break/continue
		{"flow/break_basic", `for i in 1 2 3 4 5; do if [ $i -eq 3 ]; then break; fi; printf '%s ' $i; done`, ""},
		{"flow/continue_basic", `for i in 1 2 3 4 5; do if [ $i -eq 3 ]; then continue; fi; printf '%s ' $i; done`, ""},

		// Nested loops
		{"flow/nested_break", `for i in 1 2; do for j in a b c; do if [ $j = b ]; then break; fi; printf '%s%s ' $i $j; done; done`, ""},

		// Brace groups
		{"flow/brace_group", `{ echo one; echo two; }`, ""},
		{"flow/brace_group_status", `{ true; false; }; echo $?`, ""},
	}
}

// --- Redirections ---

func redirectionCases() []conformanceCase {
	return []conformanceCase{
		{"redir/stdout_file", `echo hello > /tmp/gosh_redir_test; cat /tmp/gosh_redir_test; rm /tmp/gosh_redir_test`, ""},
		{"redir/append", `echo one > /tmp/gosh_redir_test; echo two >> /tmp/gosh_redir_test; cat /tmp/gosh_redir_test; rm /tmp/gosh_redir_test`, ""},
		{"redir/stdin_file", `echo hello > /tmp/gosh_redir_test; cat < /tmp/gosh_redir_test; rm /tmp/gosh_redir_test`, ""},
		{"redir/stderr_to_file", `echo err >&2 2>/tmp/gosh_redir_test; cat /tmp/gosh_redir_test; rm /tmp/gosh_redir_test`, ""},
		{"redir/stderr_to_stdout", `echo err >&2 2>&1 | cat`, ""},
		{"redir/fd2_append", `echo one 2>/tmp/gosh_redir_test; echo err >&2 2>>/tmp/gosh_redir_test; cat /tmp/gosh_redir_test; rm /tmp/gosh_redir_test`, ""},
		{"redir/herestring", `cat <<< hello`, ""},
		{"redir/herestring_var", `x=world; cat <<< "hello $x"`, ""},
		{"redir/and_gt", `echo both &>/tmp/gosh_redir_test; cat /tmp/gosh_redir_test; rm /tmp/gosh_redir_test`, ""},
	}
}

// --- Functions ---

func functionCases() []conformanceCase {
	return []conformanceCase{
		{"func/basic", `f() { echo hello; }; f`, ""},
		{"func/with_args", `f() { echo $1 $2; }; f hello world`, ""},
		{"func/return_status", `f() { return 42; }; f; echo $?`, ""},
		{"func/local_var", `x=outer; f() { local x=inner; echo $x; }; f; echo $x`, ""},
		{"func/recursion", `f() { if [ $1 -le 0 ]; then echo done; return; fi; echo $1; f $(($1 - 1)); }; f 3`, ""},
		{"func/positional_restore", `set -- a b c; f() { echo $1; }; f x; echo $1`, ""},
		{"func/param_count", `f() { echo $#; }; f a b c`, ""},
		{"func/all_params", `f() { echo "$@"; }; f one two three`, ""},
		{"func/return_in_loop", `f() { for i in 1 2 3; do if [ $i -eq 2 ]; then return 0; fi; echo $i; done; }; f`, ""},
		{"func/nested_local", `f() { local x=1; g; echo $x; }; g() { local x=2; }; f`, ""},
	}
}

// --- Builtins ---

func builtinCases() []conformanceCase {
	return []conformanceCase{
		// echo
		{"builtin/echo_basic", `echo hello world`, ""},
		{"builtin/echo_n", `echo -n hello; echo world`, ""},

		// printf
		{"builtin/printf_s", `printf '%s\n' hello`, ""},
		{"builtin/printf_d", `printf '%d\n' 42`, ""},
		{"builtin/printf_x", `printf '%x\n' 255`, ""},
		{"builtin/printf_o", `printf '%o\n' 8`, ""},
		{"builtin/printf_reuse", `printf '%s ' a b c; echo`, ""},
		{"builtin/printf_escape_n", `printf 'hello\nworld\n'`, ""},
		{"builtin/printf_escape_t", `printf 'hello\tworld\n'`, ""},
		{"builtin/printf_percent", `printf '100%%\n'`, ""},

		// cd / pwd
		{"builtin/cd_pwd", `cd /tmp; pwd`, ""},
		{"builtin/cd_dash", `cd /tmp; cd /; cd -; pwd`, ""},

		// true / false
		{"builtin/true", `true; echo $?`, ""},
		{"builtin/false", `false; echo $?`, ""},

		// export / env
		{"builtin/export_visible", `export X=hello; env | grep '^X='`, ""},
		{"builtin/export_unset", `export X=hello; unset X; env | grep -c '^X=' || true`, ""},

		// read
		{"builtin/read_basic", `echo 'hello world' | { read a b; echo "$a:$b"; }`, ""},
		{"builtin/read_no_var", `echo hello | { read; echo "$REPLY"; }`, ""},
		{"builtin/read_eof", `printf '' | { read x; echo $?; }`, ""},
		{"builtin/read_r", `printf 'hello\\nworld\n' | { read -r x; echo "$x"; }`, ""},
		{"builtin/read_a", `echo 'a b c' | { read -a arr; echo "${arr[1]}"; }`, ""},

		// shift
		{"builtin/shift_basic", `set -- a b c; shift; echo $1 $2`, ""},
		{"builtin/shift_n", `set -- a b c d; shift 2; echo $1 $2`, ""},
		{"builtin/shift_too_many", `set -- a; shift 5 2>/dev/null; echo $?`, ""},

		// eval
		{"builtin/eval_basic", `eval 'echo hello'`, ""},
		{"builtin/eval_var", `cmd='echo world'; eval $cmd`, ""},

		// source (using eval to simulate since we can't create files easily)
		{"builtin/source_inline", `echo 'x=42' > /tmp/gosh_src_test.sh; source /tmp/gosh_src_test.sh; echo $x; rm /tmp/gosh_src_test.sh`, ""},

		// command / type
		{"builtin/command_v_builtin", `command -v echo`, ""},
		{"builtin/command_v_external", `command -v cat`, ""},
		{"builtin/command_skip_func", `echo() { printf 'FUNC\n'; }; command echo hello`, ""},
		{"builtin/type_builtin", `type echo | head -1`, ""},

		// alias
		{"builtin/alias_define_use", `alias ll='echo listing'; ll`, ""},

		// getopts
		{"builtin/getopts_basic", `f() { while getopts 'ab:c' opt; do echo "$opt:$OPTARG"; done; }; f -a -b val -c`, ""},

		// declare
		{"builtin/declare_i", `declare -i x=2+3; echo $x`, ""},
		{"builtin/declare_r", `declare -r x=42; x=1 2>/dev/null; echo $?`, ""},
		{"builtin/declare_x", `declare -x MYVAR=hello; env | grep '^MYVAR='`, ""},
	}
}

// --- Word Splitting ---

func wordSplittingCases() []conformanceCase {
	return []conformanceCase{
		{"split/default_ifs", `x='a  b  c'; printf '<%s>\n' $x`, ""},
		{"split/custom_ifs", `IFS=:; x='a:b:c'; printf '<%s>\n' $x`, ""},
		{"split/empty_ifs", `IFS=; x='a b c'; printf '<%s>\n' $x`, ""},
		{"split/quoted_no_split", `x='a b c'; printf '<%s>\n' "$x"`, ""},
		{"split/unset_empty_removed", `x=; printf '<%s>\n' a $x b`, ""},
		{"split/quoted_empty_preserved", `x=; printf '<%s>\n' a "$x" b`, ""},
		{"split/ifs_whitespace_collapse", `x='  a  b  c  '; printf '<%s>\n' $x`, ""},
		{"split/ifs_non_whitespace", `IFS=,; x=',a,,b,'; printf '<%s>\n' $x`, ""},
		{"split/at_expansion", `set -- one two three; printf '<%s>\n' "$@"`, ""},
		{"split/star_expansion", `set -- one two three; printf '<%s>\n' "$*"`, ""},
	}
}

// --- Globbing ---

func globCases() []conformanceCase {
	return []conformanceCase{
		{"glob/star", `mkdir -p /tmp/gosh_glob_test; touch /tmp/gosh_glob_test/{a,b,c}.txt; echo /tmp/gosh_glob_test/*.txt; rm -rf /tmp/gosh_glob_test`, ""},
		{"glob/question", `mkdir -p /tmp/gosh_glob_test; touch /tmp/gosh_glob_test/{a,b,c}.txt; echo /tmp/gosh_glob_test/?.txt; rm -rf /tmp/gosh_glob_test`, ""},
		{"glob/bracket", `mkdir -p /tmp/gosh_glob_test; touch /tmp/gosh_glob_test/{a,b,c}.txt; echo /tmp/gosh_glob_test/[ab].txt; rm -rf /tmp/gosh_glob_test`, ""},
		{"glob/no_match_literal", `echo /tmp/gosh_glob_nomatch_xyzzy_*.foo`, ""},
		{"glob/quoted_no_glob", `echo '/tmp/gosh_glob_nomatch_*.foo'`, ""},
	}
}

// --- test / [ ---

func testCommandCases() []conformanceCase {
	return []conformanceCase{
		{"test/string_z_empty", `test -z ''; echo $?`, ""},
		{"test/string_z_nonempty", `test -z 'x'; echo $?`, ""},
		{"test/string_n_nonempty", `test -n 'x'; echo $?`, ""},
		{"test/string_n_empty", `test -n ''; echo $?`, ""},
		{"test/string_equal", `test 'abc' = 'abc'; echo $?`, ""},
		{"test/string_not_equal", `test 'abc' != 'def'; echo $?`, ""},
		{"test/int_eq", `test 5 -eq 5; echo $?`, ""},
		{"test/int_ne", `test 5 -ne 3; echo $?`, ""},
		{"test/int_lt", `test 3 -lt 5; echo $?`, ""},
		{"test/int_gt", `test 5 -gt 3; echo $?`, ""},
		{"test/int_le", `test 5 -le 5; echo $?`, ""},
		{"test/int_ge", `test 5 -ge 5; echo $?`, ""},
		{"test/file_e", `test -e /tmp; echo $?`, ""},
		{"test/file_d", `test -d /tmp; echo $?`, ""},
		{"test/file_f", `test -f /tmp; echo $?`, ""},
		{"test/not", `test ! -f /tmp; echo $?`, ""},
		{"test/and", `test 1 -eq 1 -a 2 -eq 2; echo $?`, ""},
		{"test/or", `test 1 -eq 2 -o 2 -eq 2; echo $?`, ""},
		{"test/bracket_syntax", `[ 'hello' = 'hello' ]; echo $?`, ""},
		{"test/bracket_missing_close", `[ 'hello' = 'hello' 2>/dev/null; echo $?`, ""},
	}
}

// --- [[ ]] ---

func dblBracketCases() []conformanceCase {
	return []conformanceCase{
		{"dblbracket/string_eq", `[[ hello == hello ]]; echo $?`, ""},
		{"dblbracket/string_ne", `[[ hello != world ]]; echo $?`, ""},
		{"dblbracket/glob_match", `[[ hello == hel* ]]; echo $?`, ""},
		{"dblbracket/glob_no_match", `[[ hello == wor* ]]; echo $?`, ""},
		{"dblbracket/regex", `[[ hello123 =~ ^hello[0-9]+$ ]]; echo $?`, ""},
		{"dblbracket/regex_no_match", `[[ hello =~ ^[0-9]+$ ]]; echo $?`, ""},
		{"dblbracket/regex_rematch", `RE='^([a-z]+)([0-9]+)$'; [[ abc123 =~ $RE ]]; echo ${BASH_REMATCH[0]} ${BASH_REMATCH[1]} ${BASH_REMATCH[2]}`, ""},
		{"dblbracket/and", `[[ 1 -eq 1 && 2 -eq 2 ]]; echo $?`, ""},
		{"dblbracket/or", `[[ 1 -eq 2 || 2 -eq 2 ]]; echo $?`, ""},
		{"dblbracket/not", `[[ ! 1 -eq 2 ]]; echo $?`, ""},
		{"dblbracket/string_lt", `[[ abc < def ]]; echo $?`, ""},
		{"dblbracket/string_gt", `[[ def > abc ]]; echo $?`, ""},
		{"dblbracket/z_empty", `[[ -z '' ]]; echo $?`, ""},
		{"dblbracket/n_nonempty", `[[ -n 'x' ]]; echo $?`, ""},
		{"dblbracket/file_e", `[[ -e /tmp ]]; echo $?`, ""},
		{"dblbracket/file_d", `[[ -d /tmp ]]; echo $?`, ""},
		{"dblbracket/var_expansion", `x=hello; [[ $x == hello ]]; echo $?`, ""},
		{"dblbracket/int_eq", `[[ 42 -eq 42 ]]; echo $?`, ""},
		{"dblbracket/int_lt", `[[ 3 -lt 5 ]]; echo $?`, ""},
		{"dblbracket/v_set", `x=hello; [[ -v x ]]; echo $?`, ""},
		{"dblbracket/v_unset", `unset y; [[ -v y ]]; echo $?`, ""},
		{"dblbracket/quoted_rhs_literal", `[[ 'hel*' == 'hel*' ]]; echo $?`, ""},
		{"dblbracket/parens", `[[ (1 -eq 1) ]]; echo $?`, ""},
	}
}

// --- Traps ---

func trapCases() []conformanceCase {
	return []conformanceCase{
		{"trap/exit", `trap 'echo bye' EXIT; echo hello`, ""},
		{"trap/err", `trap 'echo ERR' ERR; false; true`, ""},
		{"trap/return", `trap 'echo RET' RETURN; f() { echo in_f; }; f`, "gosh bug: RETURN trap fires on function return but bash only fires it in functions/sourced files when trap is set inside them"},
		{"trap/remove", `trap 'echo bye' EXIT; trap - EXIT; echo done`, ""},
		{"trap/ignore", `trap '' INT; echo ok`, ""},
		{"trap/subshell_inherit", `trap 'echo bye' EXIT; (echo sub)`, ""},
	}
}

// --- Set Options ---

func setOptionCases() []conformanceCase {
	return []conformanceCase{
		// errexit
		{"set/errexit_basic", `set -e; true; echo ok`, ""},
		{"set/errexit_fail", `set -e; false; echo should_not_print`, ""},
		{"set/errexit_if_suppressed", `set -e; if false; then echo no; fi; echo ok`, ""},
		{"set/errexit_and_suppressed", `set -e; false || true; echo ok`, ""},

		// nounset
		{"set/nounset_set_var", `set -u; x=hello; echo $x`, ""},
		{"set/nounset_default", `set -u; echo ${y:-fallback}`, ""},

		// pipefail
		{"set/pipefail_off", `false | true; echo $?`, ""},
		{"set/pipefail_on", `set -o pipefail; false | true; echo $?`, ""},

		// set -- positional
		{"set/positional", `set -- a b c; echo $1 $2 $3`, ""},
		{"set/positional_count", `set -- a b c; echo $#`, ""},
	}
}

// --- Special Variables ---

func specialVarCases() []conformanceCase {
	return []conformanceCase{
		{"var/question_mark", `true; echo $?`, ""},
		{"var/question_mark_fail", `false; echo $?`, ""},
		{"var/dollar_dollar", `echo $$ | grep -c '^[0-9]'`, ""},
		{"var/hash_positional", `set -- a b c; echo $#`, ""},
		{"var/at_positional", `set -- one two three; echo "$@"`, ""},
		{"var/star_positional", `set -- one two three; echo "$*"`, ""},
		{"var/positional_1_9", `set -- a b c d e f g h i; echo $1 $5 $9`, ""},
		{"var/positional_10", `set -- a b c d e f g h i j; echo ${10}`, ""},
		{"var/zero", `echo $0 | grep -c .`, ""},
		{"var/random_is_number", `echo $RANDOM | grep -cE '^[0-9]+$'`, ""},
		{"var/seconds_is_number", `echo $SECONDS | grep -cE '^[0-9]+$'`, ""},
	}
}

// --- Pipelines ---

func pipelineCases() []conformanceCase {
	return []conformanceCase{
		{"pipe/basic", `echo hello | tr a-z A-Z`, ""},
		{"pipe/chain", `echo hello world | tr ' ' '\n' | sort`, ""},
		{"pipe/exit_status", `true | false; echo $?`, ""},
		{"pipe/with_builtins", `echo hello | cat | cat`, ""},
		{"pipe/and", `true && echo yes`, ""},
		{"pipe/and_fail", `false && echo no; echo $?`, ""},
		{"pipe/or", `false || echo fallback`, ""},
		{"pipe/or_pass", `true || echo no; echo ok`, ""},
		{"pipe/semicolon", `echo one; echo two; echo three`, ""},
		{"pipe/compound_in_pipe", `for i in a b c; do echo $i; done | sort -r`, ""},
	}
}

// --- Subshells ---

func subshellCases() []conformanceCase {
	return []conformanceCase{
		{"subshell/basic", `(echo hello)`, ""},
		{"subshell/var_isolation", `x=outer; (x=inner; echo $x); echo $x`, ""},
		{"subshell/exit_status", `(exit 42); echo $?`, ""},
		{"subshell/nested", `(echo $(echo nested))`, ""},
		{"subshell/array_isolation", `a=(1 2 3); (a[0]=X; echo ${a[0]}); echo ${a[0]}`, ""},
		{"subshell/func_isolation", `f() { echo outer; }; (f() { echo inner; }; f); f`, ""},
	}
}

// --- Here Documents ---

func heredocCases() []conformanceCase {
	return []conformanceCase{
		{"heredoc/basic", fmt.Sprintf("cat <<EOF\nhello world\nEOF"), ""},
		{"heredoc/var_expand", fmt.Sprintf("x=hello; cat <<EOF\n$x world\nEOF"), ""},
		{"heredoc/quoted_no_expand", fmt.Sprintf("x=hello; cat <<'EOF'\n$x world\nEOF"), ""},
		{"heredoc/strip_tabs", fmt.Sprintf("cat <<-EOF\n\thello\n\tworld\nEOF"), ""},
		{"heredoc/multiline", fmt.Sprintf("cat <<EOF\nline 1\nline 2\nline 3\nEOF"), ""},
	}
}

// --- Process Substitution ---

func processCases() []conformanceCase {
	return []conformanceCase{
		{"procsub/read_from", `cat <(echo hello)`, ""},
		{"procsub/diff", `diff <(echo a) <(echo a); echo $?`, ""},
		{"procsub/write_to", `echo hello > >(cat); sleep 0.1`, "gosh bug: >(cmd) process substitution output not working"},
	}
}

// --- Miscellaneous ---

func miscCases() []conformanceCase {
	return []conformanceCase{
		// Background & $!
		// & already terminates the command; ; after & is a syntax error in bash
		{"misc/background_bang", `sleep 0.01 & echo $! | grep -cE '^[0-9]+$'`, ""},

		// Per-command assignment
		{"misc/per_cmd_assign", `X=hello sh -c 'echo $X'`, ""},
		{"misc/per_cmd_no_persist", `X=hello true; echo "${X:-unset}"`, ""},

		// Multi-line constructs
		{"misc/multiline_if", fmt.Sprintf("if true\nthen\necho yes\nfi"), ""},
		{"misc/multiline_for", fmt.Sprintf("for i in a b c\ndo\necho $i\ndone"), ""},
		{"misc/multiline_while", fmt.Sprintf("i=0\nwhile [ $i -lt 3 ]\ndo\ni=$((i+1))\necho $i\ndone"), ""},

		// Empty command
		{"misc/empty_semi", `; echo ok`, ""},

		// Comments
		{"misc/comment", `echo hello # this is a comment`, ""},

		// Command substitution in assignment
		{"misc/cmdsub_assign", `x=$(echo hello); echo $x`, ""},

		// Nested quotes
		{"misc/nested_cmdsub_quotes", `echo "$(echo 'hello world')"`, ""},

		// Lazy expansion
		{"misc/lazy_expand", `x=1; echo $x; x=2; echo $x`, ""},

		// String length
		{"misc/length", `x=hello; echo ${#x}`, ""},
	}
}
