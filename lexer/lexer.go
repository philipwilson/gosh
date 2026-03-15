// Package lexer tokenizes shell input into a stream of tokens.
//
// It handles three quoting mechanisms following bash semantics:
//
//   - Single quotes: preserve everything literally. No escape sequences.
//     The only character that cannot appear inside single quotes is a
//     single quote.
//
//   - Double quotes: preserve most characters literally, but allow
//     backslash escapes for \, ", $, and `. A backslash followed by
//     any other character is kept as-is (both the \ and the character).
//
//   - Backslash (outside quotes): escapes the immediately following
//     character, making it literal.
//
// Operator tokens (|, <, >, >>, ;, &&, ||) are recognized only in
// unquoted context. Whitespace separates tokens only when unquoted.
//
// Each WORD token carries both a plain string value (Val) and a
// slice of WordParts that preserves quoting context. The expander
// uses Parts to know where $VAR expansion should occur (not inside
// single quotes, not on backslash-escaped $).
package lexer

import (
	"fmt"
	"strings"
)

// QuoteContext indicates how a piece of text was quoted in the input.
// The expander uses this to decide whether to perform variable
// expansion and (later) glob expansion.
type QuoteContext int

const (
	Unquoted     QuoteContext = iota // bare text — expand $VAR and globs
	SingleQuoted                     // inside '...' or after \ — fully literal
	DoubleQuoted                     // inside "..." — expand $VAR but not globs
)

// WordPart is a fragment of a word with a uniform quoting context.
// A single shell word like  he"$USER"'!' produces three parts:
//
//	[{Unquoted, "he"}, {DoubleQuoted, "$USER"}, {SingleQuoted, "!"}]
type WordPart struct {
	Text  string
	Quote QuoteContext
}

// Word is a complete shell word made up of one or more parts.
type Word []WordPart

// String joins all parts' text into a single string, discarding
// quoting context. Use this to get the resolved value after expansion.
func (w Word) String() string {
	var sb strings.Builder
	for _, p := range w {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// TokenType identifies the kind of token.
type TokenType int

const (
	TOKEN_WORD   TokenType = iota // a plain word (after quote removal)
	TOKEN_PIPE                    // |
	TOKEN_LT                     // <
	TOKEN_GT                     // >
	TOKEN_APPEND                 // >>
	TOKEN_SEMI                   // ;
	TOKEN_AND                    // &&
	TOKEN_OR                     // ||
	TOKEN_EOF                    // end of input
)

func (t TokenType) String() string {
	switch t {
	case TOKEN_WORD:
		return "WORD"
	case TOKEN_PIPE:
		return "PIPE"
	case TOKEN_LT:
		return "LT"
	case TOKEN_GT:
		return "GT"
	case TOKEN_APPEND:
		return "APPEND"
	case TOKEN_SEMI:
		return "SEMI"
	case TOKEN_AND:
		return "AND"
	case TOKEN_OR:
		return "OR"
	case TOKEN_EOF:
		return "EOF"
	default:
		return "UNKNOWN"
	}
}

// Token is a single lexical unit produced by the lexer.
type Token struct {
	Type  TokenType
	Val   string // the token's value (meaningful for WORD tokens)
	Parts Word   // quoting-aware parts (meaningful for WORD tokens)
}

func (t Token) String() string {
	if t.Type == TOKEN_WORD {
		return fmt.Sprintf("%s(%q)", t.Type, t.Val)
	}
	return t.Type.String()
}

// Lex tokenizes the input string and returns the token list.
// Returns an error if the input contains unmatched quotes.
func Lex(input string) ([]Token, error) {
	l := &lexer{input: []rune(input)}
	return l.lex()
}

type lexer struct {
	input []rune
	pos   int
}

func (l *lexer) peek() (rune, bool) {
	if l.pos >= len(l.input) {
		return 0, false
	}
	return l.input[l.pos], true
}

func (l *lexer) next() (rune, bool) {
	ch, ok := l.peek()
	if ok {
		l.pos++
	}
	return ch, ok
}

func (l *lexer) lex() ([]Token, error) {
	var tokens []Token

	for {
		l.skipSpaces()

		ch, ok := l.peek()
		if !ok {
			break
		}

		switch {
		case ch == '|':
			l.next()
			if c, ok := l.peek(); ok && c == '|' {
				l.next()
				tokens = append(tokens, Token{Type: TOKEN_OR})
			} else {
				tokens = append(tokens, Token{Type: TOKEN_PIPE})
			}

		case ch == '&':
			l.next()
			if c, ok := l.peek(); ok && c == '&' {
				l.next()
				tokens = append(tokens, Token{Type: TOKEN_AND})
			} else {
				// Bare & (background) — treat as a word for now.
				tokens = append(tokens, Token{
					Type:  TOKEN_WORD,
					Val:   "&",
					Parts: Word{{Text: "&", Quote: Unquoted}},
				})
			}

		case ch == ';':
			l.next()
			tokens = append(tokens, Token{Type: TOKEN_SEMI})

		case ch == '>':
			l.next()
			if c, ok := l.peek(); ok && c == '>' {
				l.next()
				tokens = append(tokens, Token{Type: TOKEN_APPEND})
			} else {
				tokens = append(tokens, Token{Type: TOKEN_GT})
			}

		case ch == '<':
			l.next()
			tokens = append(tokens, Token{Type: TOKEN_LT})

		default:
			parts, err := l.readWord()
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, Token{
				Type:  TOKEN_WORD,
				Val:   parts.String(),
				Parts: parts,
			})
		}
	}

	tokens = append(tokens, Token{Type: TOKEN_EOF})
	return tokens, nil
}

func (l *lexer) skipSpaces() {
	for {
		ch, ok := l.peek()
		if !ok || (ch != ' ' && ch != '\t') {
			break
		}
		l.next()
	}
}

// readWord reads a word token, returning a slice of WordParts that
// preserves the quoting context of each fragment.
//
// A word is a sequence of:
//   - unquoted characters (terminated by whitespace or operator chars)
//   - single-quoted strings
//   - double-quoted strings
//   - backslash-escaped characters
//
// These can be freely mixed: he"ll"o → hello
func (l *lexer) readWord() (Word, error) {
	var parts Word
	var buf []rune

	// flushUnquoted saves any accumulated unquoted characters as a part.
	flushUnquoted := func() {
		if len(buf) > 0 {
			parts = append(parts, WordPart{Text: string(buf), Quote: Unquoted})
			buf = nil
		}
	}

	for {
		ch, ok := l.peek()
		if !ok {
			break
		}

		switch {
		case ch == '\'':
			flushUnquoted()
			part, err := l.readSingleQuote()
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)

		case ch == '"':
			flushUnquoted()
			dqParts, err := l.readDoubleQuote()
			if err != nil {
				return nil, err
			}
			parts = append(parts, dqParts...)

		case ch == '\\':
			flushUnquoted()
			l.next() // consume the backslash
			esc, ok := l.next()
			if !ok {
				return nil, fmt.Errorf("unexpected end of input after \\")
			}
			// Backslash-escaped characters are fully literal,
			// marked as SingleQuoted to suppress expansion.
			parts = append(parts, WordPart{Text: string(esc), Quote: SingleQuoted})

		case isOperator(ch) || ch == ' ' || ch == '\t':
			goto done

		default:
			l.next()
			buf = append(buf, ch)
		}
	}

done:
	flushUnquoted()
	return parts, nil
}

// readSingleQuote reads a single-quoted string. The opening quote
// has NOT been consumed yet. Everything between the quotes is literal.
func (l *lexer) readSingleQuote() (WordPart, error) {
	l.next() // consume opening '
	var buf []rune

	for {
		ch, ok := l.next()
		if !ok {
			return WordPart{}, fmt.Errorf("unterminated single quote")
		}
		if ch == '\'' {
			return WordPart{Text: string(buf), Quote: SingleQuoted}, nil
		}
		buf = append(buf, ch)
	}
}

// readDoubleQuote reads a double-quoted string. The opening quote
// has NOT been consumed yet. Returns multiple parts because \$
// inside double quotes must be marked as literal (SingleQuoted)
// to suppress expansion.
func (l *lexer) readDoubleQuote() ([]WordPart, error) {
	l.next() // consume opening "
	var parts []WordPart
	var buf []rune

	// flush saves any accumulated double-quoted characters as a part.
	flush := func() {
		if len(buf) > 0 {
			parts = append(parts, WordPart{Text: string(buf), Quote: DoubleQuoted})
			buf = nil
		}
	}

	for {
		ch, ok := l.next()
		if !ok {
			return nil, fmt.Errorf("unterminated double quote")
		}

		switch ch {
		case '"':
			flush()
			return parts, nil

		case '\\':
			esc, ok := l.next()
			if !ok {
				return nil, fmt.Errorf("unterminated double quote")
			}
			// In bash, within double quotes only these characters
			// are actually escaped by backslash: $ ` " \ newline
			// For anything else, the backslash is preserved.
			switch esc {
			case '$':
				// \$ in double quotes → literal $, must not expand.
				flush()
				parts = append(parts, WordPart{Text: "$", Quote: SingleQuoted})
			case '\\', '"', '`':
				buf = append(buf, esc)
			default:
				buf = append(buf, '\\', esc)
			}

		default:
			buf = append(buf, ch)
		}
	}
}

func isOperator(ch rune) bool {
	return ch == '|' || ch == '&' || ch == ';' || ch == '>' || ch == '<'
}
