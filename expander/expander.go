// Package expander performs variable, command substitution, and glob
// expansion on the AST.
//
// Expansion phases (in order):
//
//  0. Brace expansion: {a,b,c}, {1..5}, {a..e} on args only
//  1. Tilde expansion: ~ → $HOME, ~user → user's home dir
//  2. Command substitution: $(cmd) and `cmd` → output of cmd
//  3. Variable expansion: $VAR, ${VAR}, $?, $$
//  3.5. Word splitting: split unquoted expansion results on IFS
//  4. Glob expansion: *, ?, [...] on unquoted args
//
// Variable expansion follows bash quoting rules:
//
//   - Unquoted text: $VAR is expanded
//   - Double-quoted text: $VAR is expanded
//   - Single-quoted text: no expansion (literal)
//   - Backslash-escaped $: no expansion (lexer marks it SingleQuoted)
//
// Command substitution: the inner command is executed via a caller-
// provided SubstFunc callback, which runs the full lex→parse→expand→
// execute pipeline recursively. The result replaces the substitution,
// with trailing newlines stripped (matching bash behavior).
//
// Glob expansion: expands unquoted *, ?, and [...] patterns using
// filepath.Glob. Quoted glob characters are literal. If a pattern
// matches no files, the word is kept as-is (bash default).
package expander

import (
	"fmt"
	"gosh/lexer"
	"gosh/parser"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// LookupFunc returns the value for a variable name. It should
// return "" for undefined variables (matching bash default behavior).
type LookupFunc func(name string) string

// SubstFunc executes a command string and returns its stdout output.
// It should strip trailing newlines from the result. May be nil if
// command substitution is not needed (e.g., in tests).
type SubstFunc func(cmd string) (string, error)

// LookupArrayFunc returns the elements of an array variable for "@"
// subscripts. Returns (elements, true) when the variable is an array
// with "@" subscript (each element becomes a separate word in double
// quotes). Returns (nil, false) for "*" subscripts and non-array vars.
type LookupArrayFunc func(name string) ([]string, bool)

// Expand walks the AST and performs all expansion phases.
// It modifies the AST in place. lookupArray may be nil.
func Expand(list *parser.List, lookup LookupFunc, subst SubstFunc, setVar SetFunc, lookupArray LookupArrayFunc) {
	for i := range list.Entries {
		expandPipeline(list.Entries[i].Pipeline, lookup, subst, setVar, lookupArray)
	}
}

func expandPipeline(pipe *parser.Pipeline, lookup LookupFunc, subst SubstFunc, setVar SetFunc, lookupArray LookupArrayFunc) {
	for _, cmd := range pipe.Cmds {
		switch c := cmd.(type) {
		case *parser.SimpleCmd:
			expandCommand(c, lookup, subst, setVar, lookupArray)
		case *parser.IfCmd:
			// IfCmd branches are expanded lazily by the executor,
			// so each branch is only expanded if it's actually taken.
		case *parser.WhileCmd:
			// WhileCmd condition and body are expanded lazily on each
			// iteration by the executor.
		case *parser.ForCmd:
			// ForCmd words and body are expanded lazily by the executor.
		case *parser.ArithForCmd:
			// ArithForCmd expressions and body are expanded lazily by the executor.
		case *parser.CaseCmd:
			// CaseCmd word, patterns, and body are expanded lazily by the executor.
		case *parser.FuncDef:
			// FuncDef body is stored and expanded when the function is called.
		case *parser.DblBracketCmd:
			// DblBracketCmd items are expanded at execution time.
		case *parser.SubshellCmd:
			// SubshellCmd body is expanded at execution time.
		case *parser.ArithCmd:
			// ArithCmd expression is expanded at execution time.
		default:
			_ = c
		}
	}
}

func expandCommand(cmd *parser.SimpleCmd, lookup LookupFunc, subst SubstFunc, setVar SetFunc, lookupArray LookupArrayFunc) {
	// Phase 0: brace expansion on args only.
	cmd.Args = expandBracesInArgs(cmd.Args)

	// Phase 1: tilde expansion on all words.
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandTilde(cmd.Assigns[i].Value, lookup)
		for j := range cmd.Assigns[i].Array {
			cmd.Assigns[i].Array[j] = expandTilde(cmd.Assigns[i].Array[j], lookup)
		}
	}
	for i := range cmd.Args {
		cmd.Args[i] = expandTilde(cmd.Args[i], lookup)
	}
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandTilde(cmd.Redirects[i].File, lookup)
	}

	// Phase 2a: arithmetic substitution on all words.
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandArithInWord(cmd.Assigns[i].Value, lookup, setVar)
		for j := range cmd.Assigns[i].Array {
			cmd.Assigns[i].Array[j] = expandArithInWord(cmd.Assigns[i].Array[j], lookup, setVar)
		}
	}
	for i := range cmd.Args {
		cmd.Args[i] = expandArithInWord(cmd.Args[i], lookup, setVar)
	}
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandArithInWord(cmd.Redirects[i].File, lookup, setVar)
	}

	// Phase 2b: command substitution on all words.
	if subst != nil {
		for i := range cmd.Assigns {
			cmd.Assigns[i].Value = expandCmdSubstInWord(cmd.Assigns[i].Value, subst)
			for j := range cmd.Assigns[i].Array {
				cmd.Assigns[i].Array[j] = expandCmdSubstInWord(cmd.Assigns[i].Array[j], subst)
			}
		}
		for i := range cmd.Args {
			cmd.Args[i] = expandCmdSubstInWord(cmd.Args[i], subst)
		}
		for i := range cmd.Redirects {
			cmd.Redirects[i].File = expandCmdSubstInWord(cmd.Redirects[i].File, subst)
		}
	}

	// Phase 3: variable expansion on all words.
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandVarsInWord(cmd.Assigns[i].Value, lookup)
		for j := range cmd.Assigns[i].Array {
			cmd.Assigns[i].Array[j] = expandVarsInWord(cmd.Assigns[i].Array[j], lookup)
		}
	}
	// For args, "${arr[@]}" and "$@" in double quotes may produce multiple words.
	var newArgs []lexer.Word
	for _, arg := range cmd.Args {
		newArgs = append(newArgs, expandVarsInWordMulti(arg, lookup, lookupArray)...)
	}
	cmd.Args = newArgs
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandVarsInWord(cmd.Redirects[i].File, lookup)
	}

	// Phase 3.5: word splitting on args only (not assignments or redirects).
	// Split unquoted expansion results on IFS characters.
	ifs := lookup("IFS")
	if ifs == "" {
		ifs = " \t\n" // default IFS when unset
	}
	cmd.Args = splitFieldsInArgs(cmd.Args, ifs)

	// Phase 4: glob expansion on args only.
	cmd.Args = expandGlobsInArgs(cmd.Args)
}

// expandTilde performs tilde expansion on a word. Only an unquoted ~
// at the very start of the word is expanded:
//
//	~        → $HOME
//	~/path   → $HOME/path
//	~user    → user's home directory
//	"~"      → literal ~ (quoted, no expansion)
func expandTilde(w lexer.Word, lookup LookupFunc) lexer.Word {
	if len(w) == 0 {
		return w
	}

	first := w[0]
	if first.Quote != lexer.Unquoted || !strings.HasPrefix(first.Text, "~") {
		return w
	}

	// Extract the tilde prefix: everything up to the first / (or end).
	text := first.Text[1:] // skip the ~
	var prefix, rest string
	if idx := strings.IndexByte(text, '/'); idx >= 0 {
		prefix = text[:idx]
		rest = text[idx:] // includes the /
	} else {
		prefix = text
		rest = ""
	}

	// Resolve the home directory.
	var home string
	if prefix == "" {
		// ~ or ~/path → use $HOME
		home = lookup("HOME")
	} else {
		// ~user → look up that user's home directory
		if u, err := user.Lookup(prefix); err == nil {
			home = u.HomeDir
		} else {
			return w // unknown user, keep as-is
		}
	}

	if home == "" {
		return w
	}

	// Replace the first part with the expanded path.
	expanded := home + rest
	result := make(lexer.Word, 0, len(w))
	result = append(result, lexer.WordPart{Text: expanded, Quote: lexer.Unquoted})
	result = append(result, w[1:]...)
	return result
}

// expandArithInWord replaces ArithSubst and ArithSubstDQ parts with
// the result of evaluating the arithmetic expression.
func expandArithInWord(w lexer.Word, lookup LookupFunc, setVar SetFunc) lexer.Word {
	var result lexer.Word

	for _, part := range w {
		if part.Quote != lexer.ArithSubst && part.Quote != lexer.ArithSubstDQ {
			result = append(result, part)
			continue
		}

		val, err := EvalArith(part.Text, lookup, setVar)
		text := "0"
		if err == nil {
			text = strconv.FormatInt(val, 10)
		}

		// ArithSubst (unquoted) → result is Expanded (subject to splitting/globs).
		// ArithSubstDQ (double-quoted) → result is DoubleQuoted (no globs).
		quote := lexer.Expanded
		if part.Quote == lexer.ArithSubstDQ {
			quote = lexer.DoubleQuoted
		}
		result = append(result, lexer.WordPart{Text: text, Quote: quote})
	}

	return result
}

// expandCmdSubstInWord replaces CmdSubst and CmdSubstDQ parts with
// the output of running the command. CmdSubst results are Unquoted
// (subject to glob expansion), CmdSubstDQ results are DoubleQuoted
// (not subject to glob expansion).
func expandCmdSubstInWord(w lexer.Word, subst SubstFunc) lexer.Word {
	var result lexer.Word

	for _, part := range w {
		if part.Quote != lexer.CmdSubst && part.Quote != lexer.CmdSubstDQ {
			result = append(result, part)
			continue
		}

		output, err := subst(part.Text)
		if err != nil {
			output = ""
		}

		// CmdSubst (unquoted) → result is Expanded (subject to splitting/globs).
		// CmdSubstDQ (double-quoted) → result is DoubleQuoted (no globs).
		quote := lexer.Expanded
		if part.Quote == lexer.CmdSubstDQ {
			quote = lexer.DoubleQuoted
		}
		result = append(result, lexer.WordPart{Text: output, Quote: quote})
	}

	return result
}

// expandVarsInWord expands $VAR references in a word, respecting quoting.
// For Unquoted parts, expansion results are marked Expanded (subject to
// word splitting). For DoubleQuoted parts, results keep DoubleQuoted context.
func expandVarsInWord(w lexer.Word, lookup LookupFunc) lexer.Word {
	var result lexer.Word

	for _, part := range w {
		if part.Quote == lexer.SingleQuoted {
			result = append(result, part)
			continue
		}
		if part.Quote == lexer.Unquoted {
			// Produce structured parts: literal text stays Unquoted,
			// expansion results are marked Expanded for word splitting.
			result = append(result, expandDollarParts(part.Text, lookup)...)
		} else {
			// DoubleQuoted (or Expanded from prior phase) — expand
			// variables but keep the quoting context.
			expanded := expandDollar(part.Text, lookup)
			result = append(result, lexer.WordPart{
				Text:  expanded,
				Quote: part.Quote,
			})
		}
	}

	return result
}

// expandVarsInWordMulti is like expandVarsInWord but can return multiple
// words when a DoubleQuoted part contains "${arr[@]}" or "$@". Each array
// element becomes a separate word, with surrounding text attached to the
// first and last elements.
func expandVarsInWordMulti(w lexer.Word, lookup LookupFunc, lookupArray LookupArrayFunc) []lexer.Word {
	if lookupArray == nil {
		return []lexer.Word{expandVarsInWord(w, lookup)}
	}

	// Check if any DoubleQuoted part contains an array-@ expansion.
	hasArrayAt := false
	for _, part := range w {
		if part.Quote == lexer.DoubleQuoted && containsArrayAt(part.Text) {
			hasArrayAt = true
			break
		}
	}
	if !hasArrayAt {
		return []lexer.Word{expandVarsInWord(w, lookup)}
	}

	// Process parts, splitting on array-@ expansions in DoubleQuoted context.
	// We build words by accumulating parts until we hit a "${arr[@]}" or "$@",
	// then split into multiple words.
	var result []lexer.Word
	var cur lexer.Word

	for _, part := range w {
		if part.Quote == lexer.SingleQuoted {
			cur = append(cur, part)
			continue
		}
		if part.Quote == lexer.Unquoted {
			cur = append(cur, expandDollarParts(part.Text, lookup)...)
			continue
		}
		if part.Quote != lexer.DoubleQuoted || !containsArrayAt(part.Text) {
			expanded := expandDollar(part.Text, lookup)
			cur = append(cur, lexer.WordPart{Text: expanded, Quote: part.Quote})
			continue
		}

		// DoubleQuoted part with ${arr[@]} or $@ — split into elements.
		elements := expandDollarMulti(part.Text, lookup, lookupArray)
		if len(elements) == 0 {
			// Empty array — the word should be removed entirely
			// (unless there are other non-empty parts).
			continue
		}

		// First element attaches to accumulated prefix.
		cur = append(cur, lexer.WordPart{Text: elements[0], Quote: lexer.DoubleQuoted})

		if len(elements) == 1 {
			continue
		}

		// Emit prefix + first element as a word.
		result = append(result, cur)

		// Middle elements each become their own word.
		for _, elem := range elements[1 : len(elements)-1] {
			result = append(result, lexer.Word{{Text: elem, Quote: lexer.DoubleQuoted}})
		}

		// Last element starts a new accumulator for suffix.
		cur = lexer.Word{{Text: elements[len(elements)-1], Quote: lexer.DoubleQuoted}}
	}

	if len(cur) > 0 {
		result = append(result, cur)
	}

	// If result is empty (empty array with no other text), return nothing.
	if len(result) == 0 {
		return nil
	}

	return result
}

// containsArrayAt returns true if text contains $@ or ${...[@]}.
func containsArrayAt(text string) bool {
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '$' {
			continue
		}
		i++
		if i >= len(runes) {
			break
		}
		if runes[i] == '@' {
			return true
		}
		if runes[i] == '{' {
			// Look for [@] before }.
			j := i + 1
			for j < len(runes) && runes[j] != '}' {
				j++
			}
			if j < len(runes) {
				content := string(runes[i+1 : j])
				if strings.HasSuffix(content, "[@]") {
					return true
				}
			}
		}
	}
	return false
}

// expandDollarMulti expands a DoubleQuoted text that may contain $@ or
// ${arr[@]}, returning separate elements for array-@ expansions. Non-array
// parts are concatenated into adjacent elements.
func expandDollarMulti(text string, lookup LookupFunc, lookupArray LookupArrayFunc) []string {
	runes := []rune(text)
	var elements []string
	var buf strings.Builder
	hadEmptyArray := false
	i := 0

	for i < len(runes) {
		if runes[i] != '$' {
			buf.WriteRune(runes[i])
			i++
			continue
		}

		i++ // skip $
		if i >= len(runes) {
			buf.WriteRune('$')
			break
		}

		// Check for $@ (positional params array).
		if runes[i] == '@' {
			i++
			elems, ok := lookupArray("@")
			if ok && len(elems) == 0 {
				hadEmptyArray = true
				continue
			}
			if ok && len(elems) > 0 {
				// First element merges with prefix.
				buf.WriteString(elems[0])
				elements = appendBuf(&buf, elements)
				// Middle elements.
				for _, e := range elems[1 : len(elems)-1] {
					elements = append(elements, e)
				}
				// Last element starts next buffer.
				if len(elems) > 1 {
					buf.WriteString(elems[len(elems)-1])
				}
			} else if !ok {
				buf.WriteString(lookup("@"))
			}
			continue
		}

		// Check for ${...[@]}.
		if runes[i] == '{' {
			start := i + 1
			j := start
			for j < len(runes) && runes[j] != '}' {
				j++
			}
			if j < len(runes) {
				content := string(runes[start:j])
				i = j + 1

				// Check for array[@] pattern.
				if idx := strings.Index(content, "[@]"); idx >= 0 && idx+3 == len(content) {
					arrName := content[:idx]
					elems, ok := lookupArray(arrName + "[@]")
					if ok && len(elems) > 0 {
						buf.WriteString(elems[0])
						elements = appendBuf(&buf, elements)
						for _, e := range elems[1 : len(elems)-1] {
							elements = append(elements, e)
						}
						if len(elems) > 1 {
							buf.WriteString(elems[len(elems)-1])
						}
					} else if !ok {
						buf.WriteString(expandParam(content, lookup))
					} else {
						// ok && len(elems)==0: empty array
						hadEmptyArray = true
					}
					continue
				}

				buf.WriteString(expandParam(content, lookup))
				continue
			}
			// Unclosed brace.
			buf.WriteString("${")
			buf.WriteString(string(runes[start:]))
			break
		}

		// Regular $VAR expansion.
		switch {
		case runes[i] == '?':
			buf.WriteString(lookup("?"))
			i++
		case runes[i] == '$':
			buf.WriteString(lookup("$"))
			i++
		case runes[i] == '#':
			buf.WriteString(lookup("#"))
			i++
		case runes[i] == '*':
			buf.WriteString(lookup("*"))
			i++
		case runes[i] >= '0' && runes[i] <= '9':
			buf.WriteString(lookup(string(runes[i])))
			i++
		case isNameStart(runes[i]):
			start := i
			for i < len(runes) && isNameCont(runes[i]) {
				i++
			}
			buf.WriteString(lookup(string(runes[start:i])))
		default:
			buf.WriteRune('$')
		}
	}

	// Flush remaining buffer.
	if buf.Len() > 0 {
		elements = append(elements, buf.String())
	} else if len(elements) == 0 && !hadEmptyArray {
		elements = append(elements, "")
	}
	return elements
}

// appendBuf flushes a string builder as an element and returns the updated slice.
func appendBuf(buf *strings.Builder, elements []string) []string {
	elements = append(elements, buf.String())
	buf.Reset()
	return elements
}

// --- Word splitting (IFS field splitting) ---

// splitFieldsInArgs performs IFS word splitting on each arg word.
// Only parts marked Expanded (from unquoted variable/command/arithmetic
// expansion) are split. Literal and quoted parts are not split.
// A word that consists entirely of empty Expanded parts (with no
// literal or quoted text) is removed entirely.
func splitFieldsInArgs(args []lexer.Word, ifs string) []lexer.Word {
	var result []lexer.Word
	for _, w := range args {
		if !hasExpandedPart(w) {
			result = append(result, w)
			continue
		}
		result = append(result, splitWord(w, ifs)...)
	}
	return result
}

// hasExpandedPart returns true if a word contains any Expanded parts.
func hasExpandedPart(w lexer.Word) bool {
	for _, p := range w {
		if p.Quote == lexer.Expanded {
			return true
		}
	}
	return false
}

// splitWord splits a single word on IFS boundaries within Expanded parts.
// Non-Expanded parts (literal text, quoted text) are never split.
// Returns zero or more words.
func splitWord(w lexer.Word, ifs string) []lexer.Word {
	var words []lexer.Word
	var cur lexer.Word // parts accumulating for the current output word

	for _, part := range w {
		if part.Quote != lexer.Expanded {
			cur = append(cur, part)
			continue
		}

		fields, startsIFS, endsIFS := ifsSplit(part.Text, ifs)

		if len(fields) == 0 {
			// Empty expansion. If it starts/ends with IFS (e.g., all
			// whitespace), emit the current accumulator as a word.
			if (startsIFS || endsIFS) && len(cur) > 0 {
				words = append(words, cur)
				cur = nil
			}
			continue
		}

		// First field: if the expansion starts with IFS, split from
		// any preceding literal text before joining the first field.
		if startsIFS && len(cur) > 0 {
			words = append(words, cur)
			cur = nil
		}
		if fields[0] != "" {
			cur = append(cur, lexer.WordPart{Text: fields[0], Quote: lexer.Unquoted})
		}

		// Middle fields (index 1..n-2): each becomes a separate word.
		for j := 1; j < len(fields)-1; j++ {
			if len(cur) > 0 {
				words = append(words, cur)
			}
			cur = lexer.Word{{Text: fields[j], Quote: lexer.Unquoted}}
		}

		// Last field (if more than one): emit current, start fresh.
		if len(fields) > 1 {
			if len(cur) > 0 {
				words = append(words, cur)
			}
			last := fields[len(fields)-1]
			if last != "" {
				cur = lexer.Word{{Text: last, Quote: lexer.Unquoted}}
			} else {
				cur = nil
			}
		}

		// If the expansion ends with IFS and there's following text,
		// emit the current accumulator so following parts start fresh.
		if endsIFS && len(cur) > 0 {
			words = append(words, cur)
			cur = nil
		}
	}

	if len(cur) > 0 {
		words = append(words, cur)
	}

	return words
}

// ifsSplit splits a string on IFS characters following POSIX rules.
// IFS whitespace (space, tab, newline present in ifs) is trimmed from
// the start/end and collapsed between fields. Non-whitespace IFS
// characters each act as an individual delimiter (producing empty
// fields between consecutive occurrences). Returns the fields and
// whether the string started/ended with IFS characters.
func ifsSplit(s, ifs string) (fields []string, startsIFS, endsIFS bool) {
	if s == "" {
		return nil, false, false
	}
	if ifs == "" {
		return []string{s}, false, false
	}

	runes := []rune(s)
	n := len(runes)

	isIFS := func(r rune) bool { return strings.ContainsRune(ifs, r) }
	isWhiteIFS := func(r rune) bool {
		return (r == ' ' || r == '\t' || r == '\n') && strings.ContainsRune(ifs, r)
	}

	startsIFS = isIFS(runes[0])
	endsIFS = isIFS(runes[n-1])

	i := 0

	// Skip leading IFS whitespace.
	for i < n && isWhiteIFS(runes[i]) {
		i++
	}

	// Leading non-whitespace IFS char → empty first field.
	if i < n && isIFS(runes[i]) && !isWhiteIFS(runes[i]) {
		fields = append(fields, "")
	}

	for i < n {
		// Collect non-IFS characters into a field.
		start := i
		for i < n && !isIFS(runes[i]) {
			i++
		}
		fields = append(fields, string(runes[start:i]))

		if i >= n {
			break
		}

		// Consume delimiter: optional whitespace, optional non-whitespace, optional whitespace.
		for i < n && isWhiteIFS(runes[i]) {
			i++
		}
		if i < n && isIFS(runes[i]) && !isWhiteIFS(runes[i]) {
			i++
			for i < n && isWhiteIFS(runes[i]) {
				i++
			}
			// Trailing non-whitespace delimiter at end → empty trailing field.
			if i >= n {
				fields = append(fields, "")
			}
		}
	}

	return fields, startsIFS, endsIFS
}

// expandGlobsInArgs processes a list of arg words, expanding any
// that contain unquoted glob metacharacters into multiple words
// (one per matching file). Words without globs, or globs that
// match nothing, are kept as-is.
func expandGlobsInArgs(args []lexer.Word) []lexer.Word {
	var result []lexer.Word

	for _, w := range args {
		if !hasUnquotedGlob(w) {
			result = append(result, w)
			continue
		}

		pattern := buildGlobPattern(w)
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			// No matches or bad pattern: keep original word.
			result = append(result, w)
			continue
		}

		sort.Strings(matches)
		for _, m := range matches {
			result = append(result, lexer.Word{
				{Text: m, Quote: lexer.Unquoted},
			})
		}
	}

	return result
}

// hasUnquotedGlob returns true if the word contains any unquoted
// glob metacharacters (*, ?, [).
func hasUnquotedGlob(w lexer.Word) bool {
	for _, part := range w {
		if part.Quote != lexer.Unquoted {
			continue
		}
		if strings.ContainsAny(part.Text, "*?[") {
			return true
		}
	}
	return false
}

// buildGlobPattern constructs a filepath.Glob pattern from a word.
// Unquoted parts are used as-is (their metacharacters are live).
// Quoted parts have their metacharacters escaped with \ so they
// match literally.
func buildGlobPattern(w lexer.Word) string {
	var sb strings.Builder

	for _, part := range w {
		if part.Quote == lexer.Unquoted {
			sb.WriteString(part.Text)
		} else {
			// Escape glob metacharacters in quoted text.
			for _, ch := range part.Text {
				if ch == '*' || ch == '?' || ch == '[' || ch == ']' || ch == '\\' {
					sb.WriteRune('\\')
				}
				sb.WriteRune(ch)
			}
		}
	}

	return sb.String()
}

// ExpandWord is the exported version for use by the executor
// (e.g., to expand a single word for redirect filenames).
func ExpandWord(w lexer.Word, lookup LookupFunc) string {
	return expandVarsInWord(w, lookup).String()
}

// ExpandDollar is the exported version of expandDollar for use by the
// executor (e.g., to expand $VAR references in arithmetic command expressions).
func ExpandDollar(text string, lookup LookupFunc) string {
	return expandDollar(text, lookup)
}

// expandDollarParts is like expandDollar but returns structured parts:
// literal text is marked Unquoted, expansion results are marked Expanded.
// This preserves the boundary between literal and expanded text so that
// word splitting can act only on expanded portions.
func expandDollarParts(text string, lookup LookupFunc) []lexer.WordPart {
	if !strings.ContainsRune(text, '$') {
		return []lexer.WordPart{{Text: text, Quote: lexer.Unquoted}}
	}

	runes := []rune(text)
	var parts []lexer.WordPart
	var literal strings.Builder

	flushLiteral := func() {
		if literal.Len() > 0 {
			parts = append(parts, lexer.WordPart{Text: literal.String(), Quote: lexer.Unquoted})
			literal.Reset()
		}
	}

	i := 0
	for i < len(runes) {
		if runes[i] != '$' {
			literal.WriteRune(runes[i])
			i++
			continue
		}

		i++ // skip $

		if i >= len(runes) {
			literal.WriteRune('$')
			break
		}

		var varName string
		consumed := true
		switch {
		case runes[i] == '{':
			i++ // skip {
			start := i
			for i < len(runes) && runes[i] != '}' {
				i++
			}
			if i >= len(runes) {
				literal.WriteString("${")
				literal.WriteString(string(runes[start:]))
				consumed = false
			} else {
				content := string(runes[start:i])
				i++ // skip }
				flushLiteral()
				parts = append(parts, lexer.WordPart{Text: expandParam(content, lookup), Quote: lexer.Expanded})
				consumed = false // already handled
			}
		case runes[i] == '?':
			varName = "?"
			i++
		case runes[i] == '$':
			varName = "$"
			i++
		case runes[i] == '#':
			varName = "#"
			i++
		case runes[i] == '@' || runes[i] == '*':
			varName = string(runes[i])
			i++
		case runes[i] >= '0' && runes[i] <= '9':
			varName = string(runes[i])
			i++
		case isNameStart(runes[i]):
			start := i
			for i < len(runes) && isNameCont(runes[i]) {
				i++
			}
			varName = string(runes[start:i])
		default:
			literal.WriteRune('$')
			consumed = false
		}

		if consumed {
			flushLiteral()
			parts = append(parts, lexer.WordPart{Text: lookup(varName), Quote: lexer.Expanded})
		}
	}

	flushLiteral()
	return parts
}

// --- Parameter expansion ---

// expandParam handles ${...} parameter expansion. It supports:
//
//	${var}           simple lookup
//	${#var}          string length
//	${var:-word}     default value (if unset or empty)
//	${var-word}      default value (if unset)
//	${var:+word}     alternative value (if set and non-empty)
//	${var+word}      alternative value (if set)
//	${var:=word}     assign default (if unset or empty) — limited: no SetFunc
//	${var=word}      assign default (if unset) — limited: no SetFunc
//	${var:?word}     error if unset or empty
//	${var?word}      error if unset
//	${var#pattern}   remove shortest prefix match
//	${var##pattern}  remove longest prefix match
//	${var%pattern}   remove shortest suffix match
//	${var%%pattern}  remove longest suffix match
func expandParam(content string, lookup LookupFunc) string {
	// ${#var} — string length / array length.
	if len(content) > 1 && content[0] == '#' {
		rest := content[1:]
		if isValidVarRef(rest) {
			// For ${#arr[@]} / ${#arr[*]}, use #arr[@] convention
			// to get element count from lookup.
			if idx := strings.IndexByte(rest, '['); idx >= 0 {
				sub := rest[idx+1:]
				if strings.HasSuffix(sub, "]") {
					sub = sub[:len(sub)-1]
				}
				if sub == "@" || sub == "*" {
					return lookup("#" + rest)
				}
			}
			return strconv.Itoa(len([]rune(lookup(rest))))
		}
	}

	name, op, word := parseParamOp(content)

	// Evaluate arithmetic subscripts in array references.
	name = evalArraySubscript(name, lookup)

	if op == "" {
		return lookup(name)
	}

	value := lookup(name)
	// Expand variables in the word part.
	word = expandDollar(word, lookup)

	switch op {
	case ":-", "-":
		// Use default if empty/unset.
		if value == "" {
			return word
		}
		return value
	case ":+", "+":
		// Use alternative if set and non-empty.
		if value != "" {
			return word
		}
		return ""
	case ":=", "=":
		// Assign default — we can't modify variables from the expander,
		// so behave like :- (return default without assigning).
		if value == "" {
			return word
		}
		return value
	case ":?", "?":
		// Error if unset/empty.
		if value == "" {
			msg := word
			if msg == "" {
				msg = "parameter null or not set"
			}
			fmt.Fprintf(os.Stderr, "gosh: %s: %s\n", name, msg)
			return ""
		}
		return value
	case "#":
		return removePrefix(value, word, false)
	case "##":
		return removePrefix(value, word, true)
	case "%":
		return removeSuffix(value, word, false)
	case "%%":
		return removeSuffix(value, word, true)
	case "/":
		pat, rep := splitSlashPattern(word)
		return substitutePattern(value, pat, rep, false)
	case "//":
		pat, rep := splitSlashPattern(word)
		return substitutePattern(value, pat, rep, true)
	case ":":
		return substringExtract(value, word)
	}

	return value
}

// parseParamOp extracts the variable name, operator, and word from
// the content between ${ and }. Returns (name, op, word) where op
// is "" for a simple ${var} lookup. Array subscripts like arr[0] or
// arr[@] are included in the name.
func parseParamOp(content string) (name, op, word string) {
	runes := []rune(content)
	i := 0

	// Special single-character variable names.
	if len(runes) > 0 {
		switch runes[0] {
		case '?', '$', '@', '*':
			i = 1
		default:
			if runes[0] >= '0' && runes[0] <= '9' {
				i = 1
			} else if isNameStart(runes[0]) {
				for i < len(runes) && isNameCont(runes[i]) {
					i++
				}
			} else {
				return content, "", ""
			}
		}
	}

	// Consume array subscript [expr] if present.
	if i < len(runes) && runes[i] == '[' {
		depth := 1
		i++ // skip [
		for i < len(runes) && depth > 0 {
			if runes[i] == '[' {
				depth++
			} else if runes[i] == ']' {
				depth--
			}
			i++
		}
	}

	name = string(runes[:i])
	rest := string(runes[i:])

	// Check for two-character operators first, then single-character.
	for _, candidate := range []string{"%%", "##", "//", ":-", ":+", ":=", ":?"} {
		if strings.HasPrefix(rest, candidate) {
			return name, candidate, rest[len(candidate):]
		}
	}
	for _, candidate := range []string{"%", "#", "/", "-", "+", "=", "?"} {
		if strings.HasPrefix(rest, candidate) {
			return name, candidate, rest[len(candidate):]
		}
	}

	// Substring extraction: ${var:offset} or ${var:offset:length}
	if strings.HasPrefix(rest, ":") {
		return name, ":", rest[1:]
	}

	// No operator — could be a simple name or unrecognized content.
	if rest == "" {
		return name, "", ""
	}
	return content, "", ""
}

// isValidVarName returns true if s is a valid variable name
// (used to distinguish ${#var} from ${#} with an operator).
func isValidVarName(s string) bool {
	if s == "" {
		return false
	}
	runes := []rune(s)
	if len(runes) == 1 {
		switch runes[0] {
		case '?', '$', '#', '@', '*':
			return true
		}
		if runes[0] >= '0' && runes[0] <= '9' {
			return true
		}
	}
	if !isNameStart(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if !isNameCont(r) {
			return false
		}
	}
	return true
}

// isValidVarRef returns true if s is a valid variable name or an
// array reference like arr[0] or arr[@].
func isValidVarRef(s string) bool {
	if isValidVarName(s) {
		return true
	}
	// Check for arr[subscript] pattern.
	idx := strings.IndexByte(s, '[')
	if idx <= 0 || !strings.HasSuffix(s, "]") {
		return false
	}
	return isValidVarName(s[:idx])
}

// evalArraySubscript evaluates arithmetic in array subscripts.
// For "arr[expr]", it expands $vars in expr and evaluates it as arithmetic.
// For "arr[@]" and "arr[*]", the subscript is left as-is.
// For non-array names, returns the name unchanged.
func evalArraySubscript(name string, lookup LookupFunc) string {
	idx := strings.IndexByte(name, '[')
	if idx < 0 || !strings.HasSuffix(name, "]") {
		return name
	}
	arrName := name[:idx]
	subscript := name[idx+1 : len(name)-1]
	if subscript == "@" || subscript == "*" {
		return name
	}
	// Expand $vars in the subscript.
	subscript = expandDollar(subscript, lookup)
	// Evaluate as arithmetic.
	val, err := EvalArith(subscript, lookup, nil)
	if err != nil {
		return name
	}
	return arrName + "[" + strconv.FormatInt(val, 10) + "]"
}

// removePrefix removes the shortest (or longest) prefix matching
// the glob pattern from value.
func removePrefix(value, pattern string, longest bool) string {
	runes := []rune(value)
	if longest {
		for i := len(runes); i >= 0; i-- {
			if patternMatch(pattern, string(runes[:i])) {
				return string(runes[i:])
			}
		}
	} else {
		for i := 0; i <= len(runes); i++ {
			if patternMatch(pattern, string(runes[:i])) {
				return string(runes[i:])
			}
		}
	}
	return value
}

// removeSuffix removes the shortest (or longest) suffix matching
// the glob pattern from value.
func removeSuffix(value, pattern string, longest bool) string {
	runes := []rune(value)
	if longest {
		for i := 0; i <= len(runes); i++ {
			if patternMatch(pattern, string(runes[i:])) {
				return string(runes[:i])
			}
		}
	} else {
		for i := len(runes); i >= 0; i-- {
			if patternMatch(pattern, string(runes[i:])) {
				return string(runes[:i])
			}
		}
	}
	return value
}

// splitSlashPattern splits word on the first unescaped "/" into
// pattern and replacement. If there is no "/", replacement is "".
func splitSlashPattern(word string) (pat, rep string) {
	runes := []rune(word)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			i++ // skip escaped char
			continue
		}
		if runes[i] == '/' {
			return string(runes[:i]), string(runes[i+1:])
		}
	}
	return word, ""
}

// substitutePattern replaces the first (or all) occurrences of a glob
// pattern in value with replacement. Matches are found by testing
// substrings at each position for the shortest match.
func substitutePattern(value, pattern, replacement string, all bool) string {
	if pattern == "" {
		return value
	}
	runes := []rune(value)
	var result strings.Builder
	i := 0
	for i < len(runes) {
		// Try longest match first (bash behavior).
		matchEnd := -1
		for end := len(runes); end > i; end-- {
			if patternMatch(pattern, string(runes[i:end])) {
				matchEnd = end
				break
			}
		}
		if matchEnd >= 0 {
			result.WriteString(replacement)
			i = matchEnd
			if !all {
				result.WriteString(string(runes[i:]))
				return result.String()
			}
		} else {
			result.WriteRune(runes[i])
			i++
		}
	}
	return result.String()
}

// substringExtract implements ${var:offset} and ${var:offset:length}.
// Supports negative offset (from end, requires leading space in shell)
// and negative length (stop before end).
func substringExtract(value, spec string) string {
	runes := []rune(value)
	n := len(runes)

	// Split spec on ":" to get offset and optional length.
	parts := strings.SplitN(spec, ":", 2)
	offsetStr := strings.TrimSpace(parts[0])

	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		return value
	}

	// Negative offset counts from end.
	if offset < 0 {
		offset = n + offset
		if offset < 0 {
			offset = 0
		}
	}
	if offset > n {
		return ""
	}

	if len(parts) == 1 {
		return string(runes[offset:])
	}

	lengthStr := strings.TrimSpace(parts[1])
	length, err := strconv.Atoi(lengthStr)
	if err != nil {
		return value
	}

	if length < 0 {
		// Negative length means stop before end.
		end := n + length
		if end <= offset {
			return ""
		}
		return string(runes[offset:end])
	}

	end := offset + length
	if end > n {
		end = n
	}
	return string(runes[offset:end])
}

// patternMatch is like filepath.Match but allows * to match /
// (which filepath.Match treats as a path separator). Parameter
// expansion patterns are string patterns, not path patterns.
func patternMatch(pattern, s string) bool {
	// Replace / with a placeholder that * will match.
	const ph = "\x00"
	p := strings.ReplaceAll(pattern, "/", ph)
	s = strings.ReplaceAll(s, "/", ph)
	matched, _ := filepath.Match(p, s)
	return matched
}

// expandDollar scans text for $VAR, ${VAR}, $?, $$ and replaces
// them with values from the lookup function.
func expandDollar(text string, lookup LookupFunc) string {
	if !strings.ContainsRune(text, '$') {
		return text
	}

	runes := []rune(text)
	var result strings.Builder
	result.Grow(len(text))

	i := 0
	for i < len(runes) {
		if runes[i] != '$' {
			result.WriteRune(runes[i])
			i++
			continue
		}

		i++ // skip $

		if i >= len(runes) {
			result.WriteRune('$')
			break
		}

		switch {
		case runes[i] == '{':
			i++ // skip {
			start := i
			for i < len(runes) && runes[i] != '}' {
				i++
			}
			if i >= len(runes) {
				result.WriteString("${")
				result.WriteString(string(runes[start:]))
				break
			}
			content := string(runes[start:i])
			i++ // skip }
			result.WriteString(expandParam(content, lookup))

		case runes[i] == '?':
			result.WriteString(lookup("?"))
			i++

		case runes[i] == '$':
			result.WriteString(lookup("$"))
			i++

		case runes[i] == '#':
			result.WriteString(lookup("#"))
			i++

		case runes[i] == '@' || runes[i] == '*':
			result.WriteString(lookup(string(runes[i])))
			i++

		case runes[i] >= '0' && runes[i] <= '9':
			result.WriteString(lookup(string(runes[i])))
			i++

		case isNameStart(runes[i]):
			start := i
			for i < len(runes) && isNameCont(runes[i]) {
				i++
			}
			name := string(runes[start:i])
			result.WriteString(lookup(name))

		default:
			result.WriteRune('$')
		}
	}

	return result.String()
}

func isNameStart(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isNameCont(ch rune) bool {
	return isNameStart(ch) || (ch >= '0' && ch <= '9')
}
