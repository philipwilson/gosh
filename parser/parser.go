package parser

import (
	"fmt"
	"gosh/lexer"
	"strings"
)

// Parse takes a token stream (from lexer.Lex) and returns an AST.
func Parse(tokens []lexer.Token) (*List, error) {
	p := &parser{tokens: tokens}
	return p.parseList()
}

type parser struct {
	tokens []lexer.Token
	pos    int
}

func (p *parser) peek() lexer.Token {
	if p.pos >= len(p.tokens) {
		return lexer.Token{Type: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) next() lexer.Token {
	tok := p.peek()
	if tok.Type != lexer.TOKEN_EOF {
		p.pos++
	}
	return tok
}

// parseList parses: pipeline ((';' | '&&' | '||') pipeline)*
func (p *parser) parseList() (*List, error) {
	list := &List{}

	pipeline, err := p.parsePipeline()
	if err != nil {
		return nil, err
	}

	for {
		tok := p.peek()
		var op string
		switch tok.Type {
		case lexer.TOKEN_SEMI:
			op = ";"
		case lexer.TOKEN_AND:
			op = "&&"
		case lexer.TOKEN_OR:
			op = "||"
		case lexer.TOKEN_AMP:
			op = "&"
		case lexer.TOKEN_EOF:
			list.Entries = append(list.Entries, ListEntry{Pipeline: pipeline})
			return list, nil
		default:
			return nil, fmt.Errorf("unexpected token %s", tok)
		}

		p.next() // consume the operator

		list.Entries = append(list.Entries, ListEntry{Pipeline: pipeline, Op: op})

		// A trailing semicolon with nothing after it is valid: "echo hi ;"
		if p.peek().Type == lexer.TOKEN_EOF {
			return list, nil
		}

		pipeline, err = p.parsePipeline()
		if err != nil {
			return nil, err
		}
	}
}

// parsePipeline parses: command ('|' command)*
func (p *parser) parsePipeline() (*Pipeline, error) {
	pipe := &Pipeline{}

	cmd, err := p.parseCommand()
	if err != nil {
		return nil, err
	}
	pipe.Cmds = append(pipe.Cmds, cmd)

	for p.peek().Type == lexer.TOKEN_PIPE {
		p.next() // consume |
		cmd, err = p.parseCommand()
		if err != nil {
			return nil, err
		}
		pipe.Cmds = append(pipe.Cmds, cmd)
	}

	return pipe, nil
}

// parseCommand parses: (assign)* (word | redirect)+
//
// Assignments (NAME=VALUE) are only recognized before the first
// non-assignment word, following bash semantics.
func (p *parser) parseCommand() (*SimpleCmd, error) {
	cmd := &SimpleCmd{}
	seenArg := false // once true, no more assignments

	for {
		tok := p.peek()

		switch tok.Type {
		case lexer.TOKEN_WORD:
			// Check for assignment before any command word.
			if !seenArg {
				if name, value, ok := splitAssignment(tok.Parts); ok {
					p.next()
					cmd.Assigns = append(cmd.Assigns, Assignment{
						Name:  name,
						Value: value,
					})
					continue
				}
			}
			p.next()
			cmd.Args = append(cmd.Args, tok.Parts)
			seenArg = true

		case lexer.TOKEN_LT:
			r, err := p.parseRedirect(REDIR_IN)
			if err != nil {
				return nil, err
			}
			cmd.Redirects = append(cmd.Redirects, r)

		case lexer.TOKEN_GT:
			r, err := p.parseRedirect(REDIR_OUT)
			if err != nil {
				return nil, err
			}
			cmd.Redirects = append(cmd.Redirects, r)

		case lexer.TOKEN_APPEND:
			r, err := p.parseRedirect(REDIR_APPEND)
			if err != nil {
				return nil, err
			}
			cmd.Redirects = append(cmd.Redirects, r)

		case lexer.TOKEN_DUP:
			rTok := p.next()
			target := lexer.Word{{Text: rTok.Val, Quote: lexer.Unquoted}}
			cmd.Redirects = append(cmd.Redirects, Redirect{
				Fd:   rTok.Fd,
				Type: REDIR_DUP,
				File: target,
			})

		default:
			if len(cmd.Args) == 0 && len(cmd.Redirects) == 0 && len(cmd.Assigns) == 0 {
				return nil, fmt.Errorf("expected command, got %s", tok)
			}
			return cmd, nil
		}
	}
}

func (p *parser) parseRedirect(typ RedirType) (Redirect, error) {
	rTok := p.next() // consume the redirect operator

	tok := p.peek()
	if tok.Type != lexer.TOKEN_WORD {
		return Redirect{}, fmt.Errorf("expected filename after redirect, got %s", tok)
	}
	p.next()

	return Redirect{Fd: rTok.Fd, Type: typ, File: tok.Parts}, nil
}

// splitAssignment checks if a word is a variable assignment (NAME=VALUE).
// The name must start with a letter or underscore and contain only
// alphanumerics and underscores. The = must be in an unquoted part.
func splitAssignment(w lexer.Word) (name string, value lexer.Word, ok bool) {
	if len(w) == 0 {
		return
	}

	// The assignment syntax requires the NAME= to be in the first
	// unquoted part of the word.
	first := w[0]
	if first.Quote != lexer.Unquoted {
		return
	}

	idx := strings.IndexByte(first.Text, '=')
	if idx <= 0 {
		return // no = found, or starts with = (not a valid name)
	}

	name = first.Text[:idx]
	if !isValidName(name) {
		return "", nil, false
	}

	// Build the value from the rest of the text after =.
	rest := first.Text[idx+1:]
	if rest != "" {
		value = append(value, lexer.WordPart{Text: rest, Quote: lexer.Unquoted})
	}
	value = append(value, w[1:]...)

	return name, value, true
}

func isValidName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, ch := range s {
		if ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			continue
		}
		if i > 0 && ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
}
