// Package expander performs variable and glob expansion on the AST.
//
// Variable expansion: walks each word and expands $VAR and ${VAR}
// references using a caller-provided lookup function. Expansion
// follows bash quoting rules:
//
//   - Unquoted text: $VAR is expanded
//   - Double-quoted text: $VAR is expanded
//   - Single-quoted text: no expansion (literal)
//   - Backslash-escaped $: no expansion (lexer marks it SingleQuoted)
//
// Glob expansion: expands unquoted *, ?, and [...] patterns using
// filepath.Glob. Quoted glob characters are literal. If a pattern
// matches no files, the word is kept as-is (bash default).
//
// Special variables: $? (last exit status), $$ (shell PID).
package expander

import (
	"gosh/lexer"
	"gosh/parser"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// LookupFunc returns the value for a variable name. It should
// return "" for undefined variables (matching bash default behavior).
type LookupFunc func(name string) string

// Expand walks the AST and expands variable references, then
// expands glob patterns. It modifies the AST in place.
func Expand(list *parser.List, lookup LookupFunc) {
	for i := range list.Entries {
		expandPipeline(list.Entries[i].Pipeline, lookup)
	}
}

func expandPipeline(pipe *parser.Pipeline, lookup LookupFunc) {
	for _, cmd := range pipe.Cmds {
		expandCommand(cmd, lookup)
	}
}

func expandCommand(cmd *parser.SimpleCmd, lookup LookupFunc) {
	// Phase 1: tilde expansion on all words.
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandTilde(cmd.Assigns[i].Value, lookup)
	}
	for i := range cmd.Args {
		cmd.Args[i] = expandTilde(cmd.Args[i], lookup)
	}
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandTilde(cmd.Redirects[i].File, lookup)
	}

	// Phase 2: variable expansion on all words.
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandVarsInWord(cmd.Assigns[i].Value, lookup)
	}
	for i := range cmd.Args {
		cmd.Args[i] = expandVarsInWord(cmd.Args[i], lookup)
	}
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandVarsInWord(cmd.Redirects[i].File, lookup)
	}

	// Phase 3: glob expansion on args only.
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

// expandVarsInWord expands $VAR references in a word, respecting quoting.
func expandVarsInWord(w lexer.Word, lookup LookupFunc) lexer.Word {
	var result lexer.Word

	for _, part := range w {
		if part.Quote == lexer.SingleQuoted {
			result = append(result, part)
			continue
		}
		expanded := expandDollar(part.Text, lookup)
		result = append(result, lexer.WordPart{
			Text:  expanded,
			Quote: part.Quote,
		})
	}

	return result
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
			name := string(runes[start:i])
			i++ // skip }
			result.WriteString(lookup(name))

		case runes[i] == '?':
			result.WriteString(lookup("?"))
			i++

		case runes[i] == '$':
			result.WriteString(lookup("$"))
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
