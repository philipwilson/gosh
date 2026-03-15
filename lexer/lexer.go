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
package lexer

import "fmt"

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
	Type TokenType
	Val  string // the token's value (meaningful for WORD tokens)
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
				// We'll handle it properly in a later milestone.
				tokens = append(tokens, Token{Type: TOKEN_WORD, Val: "&"})
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
			word, err := l.readWord()
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, Token{Type: TOKEN_WORD, Val: word})
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

// readWord reads a word token. A word is a sequence of:
//   - unquoted characters (terminated by whitespace or operator chars)
//   - single-quoted strings
//   - double-quoted strings
//   - backslash-escaped characters
//
// These can be freely mixed: he"ll"o → hello
func (l *lexer) readWord() (string, error) {
	var buf []rune

	for {
		ch, ok := l.peek()
		if !ok {
			break
		}

		switch {
		case ch == '\'':
			s, err := l.readSingleQuote()
			if err != nil {
				return "", err
			}
			buf = append(buf, s...)

		case ch == '"':
			s, err := l.readDoubleQuote()
			if err != nil {
				return "", err
			}
			buf = append(buf, s...)

		case ch == '\\':
			l.next() // consume the backslash
			esc, ok := l.next()
			if !ok {
				// Trailing backslash — bash waits for continuation.
				// We treat it as an error for now.
				return "", fmt.Errorf("unexpected end of input after \\")
			}
			buf = append(buf, esc)

		case isOperator(ch) || ch == ' ' || ch == '\t':
			// End of this word — don't consume the delimiter.
			goto done

		default:
			l.next()
			buf = append(buf, ch)
		}
	}

done:
	return string(buf), nil
}

// readSingleQuote reads a single-quoted string. The opening quote
// has NOT been consumed yet. Everything between the quotes is literal.
func (l *lexer) readSingleQuote() ([]rune, error) {
	l.next() // consume opening '
	var buf []rune

	for {
		ch, ok := l.next()
		if !ok {
			return nil, fmt.Errorf("unterminated single quote")
		}
		if ch == '\'' {
			return buf, nil
		}
		buf = append(buf, ch)
	}
}

// readDoubleQuote reads a double-quoted string. The opening quote
// has NOT been consumed yet. Backslash escapes work for \, ", $, `.
func (l *lexer) readDoubleQuote() ([]rune, error) {
	l.next() // consume opening "
	var buf []rune

	for {
		ch, ok := l.next()
		if !ok {
			return nil, fmt.Errorf("unterminated double quote")
		}

		switch ch {
		case '"':
			return buf, nil

		case '\\':
			esc, ok := l.next()
			if !ok {
				return nil, fmt.Errorf("unterminated double quote")
			}
			// In bash, within double quotes only these characters
			// are actually escaped by backslash: $ ` " \ newline
			// For anything else, the backslash is preserved.
			switch esc {
			case '\\', '"', '$', '`':
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
