// Package expander performs variable expansion on the AST.
//
// It walks each word in the AST and expands $VAR and ${VAR}
// references using a caller-provided lookup function. Expansion
// follows bash quoting rules:
//
//   - Unquoted text: $VAR is expanded
//   - Double-quoted text: $VAR is expanded
//   - Single-quoted text: no expansion (literal)
//   - Backslash-escaped $: no expansion (lexer marks it SingleQuoted)
//
// Special variables: $? (last exit status), $$ (shell PID).
package expander

import (
	"gosh/lexer"
	"gosh/parser"
	"strings"
)

// LookupFunc returns the value for a variable name. It should
// return "" for undefined variables (matching bash default behavior).
type LookupFunc func(name string) string

// Expand walks the AST and expands all variable references in words.
// It modifies the AST in place.
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
	for i := range cmd.Assigns {
		cmd.Assigns[i].Value = expandWord(cmd.Assigns[i].Value, lookup)
	}
	for i := range cmd.Args {
		cmd.Args[i] = expandWord(cmd.Args[i], lookup)
	}
	for i := range cmd.Redirects {
		cmd.Redirects[i].File = expandWord(cmd.Redirects[i].File, lookup)
	}
}

// expandWord expands $VAR references in a word, respecting quoting.
func expandWord(w lexer.Word, lookup LookupFunc) lexer.Word {
	var result lexer.Word

	for _, part := range w {
		if part.Quote == lexer.SingleQuoted {
			// Single-quoted: completely literal, no expansion.
			result = append(result, part)
			continue
		}
		// Unquoted or DoubleQuoted: expand $VAR references.
		expanded := expandDollar(part.Text, lookup)
		result = append(result, lexer.WordPart{
			Text:  expanded,
			Quote: part.Quote,
		})
	}

	return result
}

// ExpandWord is the exported version for use by the executor
// (e.g., to expand a single word for redirect filenames).
func ExpandWord(w lexer.Word, lookup LookupFunc) string {
	return expandWord(w, lookup).String()
}

// expandDollar scans text for $VAR, ${VAR}, $?, $$ and replaces
// them with values from the lookup function.
func expandDollar(text string, lookup LookupFunc) string {
	// Fast path: no $ means nothing to expand.
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
			// Bare $ at end of string.
			result.WriteRune('$')
			break
		}

		switch {
		case runes[i] == '{':
			// ${VAR} — braced variable reference.
			i++ // skip {
			start := i
			for i < len(runes) && runes[i] != '}' {
				i++
			}
			if i >= len(runes) {
				// Unterminated ${, emit literally.
				result.WriteString("${")
				result.WriteString(string(runes[start:]))
				break
			}
			name := string(runes[start:i])
			i++ // skip }
			result.WriteString(lookup(name))

		case runes[i] == '?':
			// $? — last exit status.
			result.WriteString(lookup("?"))
			i++

		case runes[i] == '$':
			// $$ — shell PID.
			result.WriteString(lookup("$"))
			i++

		case isNameStart(runes[i]):
			// $NAME — unbraced variable reference.
			start := i
			for i < len(runes) && isNameCont(runes[i]) {
				i++
			}
			name := string(runes[start:i])
			result.WriteString(lookup(name))

		default:
			// $ followed by something that's not a name — literal $.
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
