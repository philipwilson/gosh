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
// Command substitution: $(cmd) and `cmd` are recognized in unquoted
// and double-quoted contexts. The inner command text is stored as a
// CmdSubst or CmdSubstDQ word part for the expander to execute.
//
// Operator tokens (|, <, >, >>, ;, &&, ||) are recognized only in
// unquoted context. Whitespace separates tokens only when unquoted.
//
// Fd redirections: a single digit immediately before < or > is
// absorbed as the file descriptor number (e.g., 2>file, 0<input).
// The >&N and <&N syntax produces TOKEN_DUP for fd duplication.
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
	CmdSubst                         // $(cmd) or `cmd` in unquoted context
	CmdSubstDQ                       // $(cmd) or `cmd` inside double quotes
	ArithSubst                       // $(( expr )) in unquoted context
	ArithSubstDQ                     // $(( expr )) inside double quotes
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
	TOKEN_DUP                    // >&N or <&N (fd duplication)
	TOKEN_SEMI                   // ;
	TOKEN_AND                    // &&
	TOKEN_OR                     // ||
	TOKEN_AMP                    // & (background)
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
	case TOKEN_DUP:
		return "DUP"
	case TOKEN_SEMI:
		return "SEMI"
	case TOKEN_AND:
		return "AND"
	case TOKEN_OR:
		return "OR"
	case TOKEN_AMP:
		return "AMP"
	case TOKEN_EOF:
		return "EOF"
	default:
		return "UNKNOWN"
	}
}

// Token is a single lexical unit produced by the lexer.
type Token struct {
	Type  TokenType
	Val   string // the token's value (meaningful for WORD and DUP tokens)
	Parts Word   // quoting-aware parts (meaningful for WORD tokens)
	Fd    int    // file descriptor for redirect tokens (-1 = use default)
}

func (t Token) String() string {
	switch t.Type {
	case TOKEN_WORD:
		return fmt.Sprintf("%s(%q)", t.Type, t.Val)
	case TOKEN_DUP:
		return fmt.Sprintf("DUP(%d>&%s)", t.Fd, t.Val)
	case TOKEN_GT, TOKEN_APPEND, TOKEN_LT:
		if t.Fd >= 0 {
			return fmt.Sprintf("%s(fd=%d)", t.Type, t.Fd)
		}
		return t.Type.String()
	default:
		return t.Type.String()
	}
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
		case ch == '#':
			// Comment: skip the rest of the line.
			for {
				c, ok := l.peek()
				if !ok || c == '\n' {
					break
				}
				l.next()
			}
			continue

		case ch == '\n':
			l.next()
			// Newline acts as a command separator (like ;), but is
			// suppressed when redundant: after another separator,
			// after an operator that expects continuation (|, &&, ||),
			// or at the start of input.
			if len(tokens) == 0 {
				continue
			}
			last := tokens[len(tokens)-1].Type
			if last == TOKEN_SEMI || last == TOKEN_PIPE || last == TOKEN_AND ||
				last == TOKEN_OR || last == TOKEN_AMP {
				continue
			}
			tokens = append(tokens, Token{Type: TOKEN_SEMI, Fd: -1})

		case ch == '|':
			l.next()
			if c, ok := l.peek(); ok && c == '|' {
				l.next()
				tokens = append(tokens, Token{Type: TOKEN_OR, Fd: -1})
			} else {
				tokens = append(tokens, Token{Type: TOKEN_PIPE, Fd: -1})
			}

		case ch == '&':
			l.next()
			if c, ok := l.peek(); ok && c == '&' {
				l.next()
				tokens = append(tokens, Token{Type: TOKEN_AND, Fd: -1})
			} else {
				tokens = append(tokens, Token{Type: TOKEN_AMP, Fd: -1})
			}

		case ch == ';':
			l.next()
			tokens = append(tokens, Token{Type: TOKEN_SEMI, Fd: -1})

		case ch == '>':
			l.next()
			if c, ok := l.peek(); ok && c == '>' {
				l.next()
				tok := Token{Type: TOKEN_APPEND, Fd: -1}
				tokens = absorbFd(tokens, &tok)
				tokens = append(tokens, tok)
			} else if c, ok := l.peek(); ok && c == '&' {
				l.next() // consume &
				d, ok := l.peek()
				if !ok || d < '0' || d > '9' {
					return nil, fmt.Errorf("expected digit after >&")
				}
				l.next()
				tok := Token{Type: TOKEN_DUP, Val: string(d), Fd: 1}
				tokens = absorbFd(tokens, &tok)
				tokens = append(tokens, tok)
			} else {
				tok := Token{Type: TOKEN_GT, Fd: -1}
				tokens = absorbFd(tokens, &tok)
				tokens = append(tokens, tok)
			}

		case ch == '<':
			l.next()
			if c, ok := l.peek(); ok && c == '&' {
				l.next() // consume &
				d, ok := l.peek()
				if !ok || d < '0' || d > '9' {
					return nil, fmt.Errorf("expected digit after <&")
				}
				l.next()
				tok := Token{Type: TOKEN_DUP, Val: string(d), Fd: 0}
				tokens = absorbFd(tokens, &tok)
				tokens = append(tokens, tok)
			} else {
				tok := Token{Type: TOKEN_LT, Fd: -1}
				tokens = absorbFd(tokens, &tok)
				tokens = append(tokens, tok)
			}

		default:
			parts, err := l.readWord()
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, Token{
				Type:  TOKEN_WORD,
				Val:   parts.String(),
				Parts: parts,
				Fd:    -1,
			})
		}
	}

	tokens = append(tokens, Token{Type: TOKEN_EOF, Fd: -1})
	return tokens, nil
}

// absorbFd checks if the previous token is a single-digit word. If so,
// it removes that token and sets the redirect token's Fd to that digit.
// This handles patterns like 2>file and 2>&1.
func absorbFd(tokens []Token, tok *Token) []Token {
	n := len(tokens)
	if n > 0 {
		prev := tokens[n-1]
		if prev.Type == TOKEN_WORD && len(prev.Val) == 1 && prev.Val[0] >= '0' && prev.Val[0] <= '9' {
			tok.Fd = int(prev.Val[0] - '0')
			return tokens[:n-1]
		}
	}
	return tokens
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
//   - command substitutions: $(cmd) or `cmd`
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

		case ch == '`':
			flushUnquoted()
			l.next() // consume opening `
			cmd, err := l.readBacktick()
			if err != nil {
				return nil, err
			}
			parts = append(parts, WordPart{Text: cmd, Quote: CmdSubst})

		case ch == '$' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '(':
			flushUnquoted()
			if l.pos+2 < len(l.input) && l.input[l.pos+2] == '(' {
				// $(( — arithmetic substitution
				l.next() // consume $
				l.next() // consume first (
				l.next() // consume second (
				expr, err := l.readArithSubst()
				if err != nil {
					return nil, err
				}
				parts = append(parts, WordPart{Text: expr, Quote: ArithSubst})
			} else {
				l.next() // consume $
				l.next() // consume (
				cmd, err := l.readCmdSubst()
				if err != nil {
					return nil, err
				}
				parts = append(parts, WordPart{Text: cmd, Quote: CmdSubst})
			}

		case isOperator(ch) || ch == ' ' || ch == '\t' || ch == '\n':
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

		case '`':
			flush()
			cmd, err := l.readBacktick()
			if err != nil {
				return nil, err
			}
			parts = append(parts, WordPart{Text: cmd, Quote: CmdSubstDQ})

		default:
			// Check for $(( arithmetic or $( command substitution.
			if ch == '$' {
				if next, ok := l.peek(); ok && next == '(' {
					if l.pos+1 < len(l.input) && l.input[l.pos+1] == '(' {
						// $(( — arithmetic substitution
						flush()
						l.next() // consume first (
						l.next() // consume second (
						expr, err := l.readArithSubst()
						if err != nil {
							return nil, err
						}
						parts = append(parts, WordPart{Text: expr, Quote: ArithSubstDQ})
						continue
					}
					flush()
					l.next() // consume (
					cmd, err := l.readCmdSubst()
					if err != nil {
						return nil, err
					}
					parts = append(parts, WordPart{Text: cmd, Quote: CmdSubstDQ})
					continue
				}
			}
			buf = append(buf, ch)
		}
	}
}

// readCmdSubst reads a $(...) command substitution. The $( has already
// been consumed. Reads until the matching ), respecting nested parens
// and quoting inside the substitution.
func (l *lexer) readCmdSubst() (string, error) {
	depth := 1
	var buf []rune

	for depth > 0 {
		ch, ok := l.next()
		if !ok {
			return "", fmt.Errorf("unterminated command substitution")
		}

		switch ch {
		case '(':
			depth++
			buf = append(buf, ch)
		case ')':
			depth--
			if depth > 0 {
				buf = append(buf, ch)
			}
		case '\'':
			// Single-quoted string inside substitution.
			buf = append(buf, ch)
			for {
				c, ok := l.next()
				if !ok {
					return "", fmt.Errorf("unterminated single quote in command substitution")
				}
				buf = append(buf, c)
				if c == '\'' {
					break
				}
			}
		case '"':
			// Double-quoted string inside substitution.
			buf = append(buf, ch)
			for {
				c, ok := l.next()
				if !ok {
					return "", fmt.Errorf("unterminated double quote in command substitution")
				}
				buf = append(buf, c)
				if c == '"' {
					break
				}
				if c == '\\' {
					esc, ok := l.next()
					if !ok {
						return "", fmt.Errorf("unterminated double quote in command substitution")
					}
					buf = append(buf, esc)
				}
			}
		case '\\':
			esc, ok := l.next()
			if !ok {
				return "", fmt.Errorf("unexpected end of input in command substitution")
			}
			buf = append(buf, ch, esc)
		default:
			buf = append(buf, ch)
		}
	}

	return string(buf), nil
}

// readBacktick reads a `...` command substitution. The opening
// backtick has already been consumed. Inside backticks, only \` \\
// and \$ are special escape sequences.
func (l *lexer) readBacktick() (string, error) {
	var buf []rune

	for {
		ch, ok := l.next()
		if !ok {
			return "", fmt.Errorf("unterminated backtick")
		}
		if ch == '`' {
			return string(buf), nil
		}
		if ch == '\\' {
			esc, ok := l.next()
			if !ok {
				return "", fmt.Errorf("unexpected end of input in backtick")
			}
			if esc == '`' || esc == '\\' || esc == '$' {
				buf = append(buf, esc)
			} else {
				buf = append(buf, '\\', esc)
			}
			continue
		}
		buf = append(buf, ch)
	}
}

// readArithSubst reads a $((...)) arithmetic substitution. The $((
// has already been consumed. Reads until the matching )), counting
// nested parentheses.
func (l *lexer) readArithSubst() (string, error) {
	depth := 1 // we've consumed one (( so we need one ))
	var buf []rune

	for {
		ch, ok := l.next()
		if !ok {
			return "", fmt.Errorf("unterminated arithmetic substitution")
		}

		if ch == '(' {
			// Check for ((
			if next, ok := l.peek(); ok && next == '(' {
				l.next()
				depth++
				buf = append(buf, '(', '(')
				continue
			}
			buf = append(buf, ch)
		} else if ch == ')' {
			if next, ok := l.peek(); ok && next == ')' {
				depth--
				if depth == 0 {
					l.next() // consume the second )
					return string(buf), nil
				}
				l.next()
				buf = append(buf, ')', ')')
			} else {
				buf = append(buf, ch)
			}
		} else {
			buf = append(buf, ch)
		}
	}
}

func isOperator(ch rune) bool {
	return ch == '|' || ch == '&' || ch == ';' || ch == '>' || ch == '<'
}
