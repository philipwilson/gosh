package parser

import (
	"fmt"
	"gosh/lexer"
	"strings"
)

// reservedWords are words that terminate a list when they appear
// in command position. They are only special to the parser, not
// the lexer.
var reservedWords = map[string]bool{
	"then": true, "elif": true, "else": true, "fi": true,
	"do": true, "done": true, "in": true,
	"esac": true,
}

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

// skipSemis consumes consecutive TOKEN_SEMI tokens. These arise from
// newlines in multi-line input and are not meaningful between commands.
func (p *parser) skipSemis() {
	for p.peek().Type == lexer.TOKEN_SEMI {
		p.next()
	}
}

// isStopWord returns true if the current token is a reserved word
// that should terminate list parsing.
func (p *parser) isStopWord(stops ...string) bool {
	tok := p.peek()
	if tok.Type != lexer.TOKEN_WORD {
		return false
	}
	for _, s := range stops {
		if tok.Val == s {
			return true
		}
	}
	return false
}

// parseList parses: pipeline ((';' | '&&' | '||') pipeline)*
// Stops at EOF or when a token matching one of the stop words
// appears in command position.
func (p *parser) parseList(stops ...string) (*List, error) {
	list := &List{}

	// Skip leading semicolons (from newlines in multi-line input).
	p.skipSemis()

	// A list may be empty if we immediately hit a stop word
	// (e.g., "else" right after "then" with no commands — that's
	// actually an error in bash, but we handle it for robustness).
	if len(stops) > 0 && p.isStopWord(stops...) {
		return list, nil
	}
	if p.peek().Type == lexer.TOKEN_EOF || p.peek().Type == lexer.TOKEN_DSEMI {
		return list, nil
	}

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
		case lexer.TOKEN_EOF, lexer.TOKEN_DSEMI:
			list.Entries = append(list.Entries, ListEntry{Pipeline: pipeline})
			return list, nil
		default:
			// Check if this is a stop word (e.g., "then", "fi").
			if len(stops) > 0 && p.isStopWord(stops...) {
				list.Entries = append(list.Entries, ListEntry{Pipeline: pipeline})
				return list, nil
			}
			return nil, fmt.Errorf("unexpected token %s", tok)
		}

		p.next() // consume the operator

		list.Entries = append(list.Entries, ListEntry{Pipeline: pipeline, Op: op})

		// Skip extra semicolons (from newlines) and check for end.
		p.skipSemis()
		if p.peek().Type == lexer.TOKEN_EOF || p.peek().Type == lexer.TOKEN_DSEMI {
			return list, nil
		}
		if len(stops) > 0 && p.isStopWord(stops...) {
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

// parseCommand dispatches to compound commands (if, while, for) or
// falls through to parseSimpleCommand for regular commands.
func (p *parser) parseCommand() (Command, error) {
	tok := p.peek()
	if tok.Type == lexer.TOKEN_WORD {
		switch tok.Val {
		case "if":
			return p.parseIf()
		case "while":
			return p.parseWhile()
		case "for":
			return p.parseFor()
		case "case":
			return p.parseCase()
		}
	}
	return p.parseSimpleCommand()
}

// parseIf parses: 'if' list 'then' list ('elif' list 'then' list)* ('else' list)? 'fi'
func (p *parser) parseIf() (*IfCmd, error) {
	p.next() // consume "if"

	cmd := &IfCmd{}

	// Parse the first if clause.
	cond, err := p.parseList("then")
	if err != nil {
		return nil, err
	}
	if !p.expectWord("then") {
		return nil, fmt.Errorf("expected 'then', got %s", p.peek())
	}

	body, err := p.parseList("elif", "else", "fi")
	if err != nil {
		return nil, err
	}
	cmd.Clauses = append(cmd.Clauses, IfClause{Condition: cond, Body: body})

	// Parse zero or more elif clauses.
	for p.peekWord("elif") {
		p.next() // consume "elif"

		cond, err = p.parseList("then")
		if err != nil {
			return nil, err
		}
		if !p.expectWord("then") {
			return nil, fmt.Errorf("expected 'then' after 'elif', got %s", p.peek())
		}

		body, err = p.parseList("elif", "else", "fi")
		if err != nil {
			return nil, err
		}
		cmd.Clauses = append(cmd.Clauses, IfClause{Condition: cond, Body: body})
	}

	// Parse optional else.
	if p.peekWord("else") {
		p.next() // consume "else"

		elseBody, err := p.parseList("fi")
		if err != nil {
			return nil, err
		}
		cmd.ElseBody = elseBody
	}

	if !p.expectWord("fi") {
		return nil, fmt.Errorf("expected 'fi', got %s", p.peek())
	}

	return cmd, nil
}

// parseWhile parses: 'while' list 'do' list 'done'
func (p *parser) parseWhile() (*WhileCmd, error) {
	p.next() // consume "while"

	cond, err := p.parseList("do")
	if err != nil {
		return nil, err
	}
	if !p.expectWord("do") {
		return nil, fmt.Errorf("expected 'do', got %s", p.peek())
	}

	body, err := p.parseList("done")
	if err != nil {
		return nil, err
	}
	if !p.expectWord("done") {
		return nil, fmt.Errorf("expected 'done', got %s", p.peek())
	}

	return &WhileCmd{Condition: cond, Body: body}, nil
}

// parseFor parses: 'for' NAME 'in' word... (';' | EOF-before-do) 'do' list 'done'
func (p *parser) parseFor() (*ForCmd, error) {
	p.next() // consume "for"

	// Expect variable name.
	tok := p.peek()
	if tok.Type != lexer.TOKEN_WORD {
		return nil, fmt.Errorf("expected variable name after 'for', got %s", tok)
	}
	varName := tok.Val
	p.next()

	// Expect "in".
	if !p.expectWord("in") {
		return nil, fmt.Errorf("expected 'in' after 'for %s', got %s", varName, p.peek())
	}

	// Collect words until we hit ';' or 'do'.
	var words []lexer.Word
	for {
		tok = p.peek()
		if tok.Type == lexer.TOKEN_SEMI {
			p.next() // consume ;
			break
		}
		if tok.Type == lexer.TOKEN_WORD && tok.Val == "do" {
			break
		}
		if tok.Type == lexer.TOKEN_EOF {
			return nil, fmt.Errorf("expected 'do', got EOF")
		}
		if tok.Type != lexer.TOKEN_WORD {
			return nil, fmt.Errorf("expected word in 'for' list, got %s", tok)
		}
		p.next()
		words = append(words, tok.Parts)
	}

	if !p.expectWord("do") {
		return nil, fmt.Errorf("expected 'do', got %s", p.peek())
	}

	body, err := p.parseList("done")
	if err != nil {
		return nil, err
	}
	if !p.expectWord("done") {
		return nil, fmt.Errorf("expected 'done', got %s", p.peek())
	}

	return &ForCmd{VarName: varName, Words: words, Body: body}, nil
}

// parseCase parses: 'case' word 'in' (pattern ('|' pattern)* ')' list ';;')* 'esac'
func (p *parser) parseCase() (*CaseCmd, error) {
	p.next() // consume "case"

	// Expect the word to match against.
	tok := p.peek()
	if tok.Type != lexer.TOKEN_WORD {
		return nil, fmt.Errorf("expected word after 'case', got %s", tok)
	}
	word := tok.Parts
	p.next()

	// Expect "in".
	if !p.expectWord("in") {
		return nil, fmt.Errorf("expected 'in' after 'case %s', got %s", word, p.peek())
	}

	cmd := &CaseCmd{Word: word}

	// Parse clauses until "esac".
	for {
		p.skipSemis()

		if p.peekWord("esac") {
			break
		}
		if p.peek().Type == lexer.TOKEN_EOF {
			return nil, fmt.Errorf("expected 'esac', got EOF")
		}

		// Parse one or more patterns separated by |, terminated by ).
		var patterns []lexer.Word
		for {
			tok := p.peek()
			if tok.Type != lexer.TOKEN_WORD {
				return nil, fmt.Errorf("expected pattern in 'case', got %s", tok)
			}
			patterns = append(patterns, tok.Parts)
			p.next()

			if p.peek().Type == lexer.TOKEN_RPAREN {
				p.next() // consume )
				break
			}
			if p.peek().Type == lexer.TOKEN_PIPE {
				p.next() // consume |
				continue
			}
			return nil, fmt.Errorf("expected '|' or ')' in case pattern, got %s", p.peek())
		}

		// Parse the body. It ends at ;; or esac.
		// parseList will stop at "esac" (stop word) or at TOKEN_DSEMI
		// (not a valid list token, so parseList returns).
		body, err := p.parseList("esac")
		if err != nil {
			return nil, err
		}

		cmd.Clauses = append(cmd.Clauses, CaseClause{
			Patterns: patterns,
			Body:     body,
		})

		// Consume ;; if present.
		if p.peek().Type == lexer.TOKEN_DSEMI {
			p.next()
		}
	}

	if !p.expectWord("esac") {
		return nil, fmt.Errorf("expected 'esac', got %s", p.peek())
	}

	return cmd, nil
}

// peekWord returns true if the next token is a WORD with the given value.
func (p *parser) peekWord(word string) bool {
	tok := p.peek()
	return tok.Type == lexer.TOKEN_WORD && tok.Val == word
}

// expectWord consumes the next token if it's a WORD with the given value.
// Returns true if consumed, false otherwise.
func (p *parser) expectWord(word string) bool {
	if p.peekWord(word) {
		p.next()
		return true
	}
	return false
}

// parseSimpleCommand parses: (assign)* (word | redirect)+
//
// Assignments (NAME=VALUE) are only recognized before the first
// non-assignment word, following bash semantics.
func (p *parser) parseSimpleCommand() (*SimpleCmd, error) {
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
