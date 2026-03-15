package parser

import (
	"fmt"
	"gosh/lexer"
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
		case lexer.TOKEN_EOF:
			// Last entry — no trailing operator.
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

// parseCommand parses: (word | redirect)+
func (p *parser) parseCommand() (*SimpleCmd, error) {
	cmd := &SimpleCmd{}

	for {
		tok := p.peek()

		switch tok.Type {
		case lexer.TOKEN_WORD:
			p.next()
			cmd.Args = append(cmd.Args, tok.Val)

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

		default:
			// End of this command (pipe, semicolon, EOF, etc.)
			if len(cmd.Args) == 0 && len(cmd.Redirects) == 0 {
				return nil, fmt.Errorf("expected command, got %s", tok)
			}
			return cmd, nil
		}
	}
}

func (p *parser) parseRedirect(typ RedirType) (Redirect, error) {
	p.next() // consume the redirect operator

	tok := p.peek()
	if tok.Type != lexer.TOKEN_WORD {
		return Redirect{}, fmt.Errorf("expected filename after redirect, got %s", tok)
	}
	p.next()

	return Redirect{Type: typ, File: tok.Val}, nil
}
