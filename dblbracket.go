package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gosh/expander"
	"gosh/lexer"
	"gosh/parser"
)

// execDblBracket evaluates a [[ expr ]] conditional expression.
// Variables are expanded but no word splitting or glob expansion is performed.
func execDblBracket(state *shellState, cmd *parser.DblBracketCmd, stderr *os.File) int {
	lookup := func(name string) string { return state.lookup(name) }

	// Expand variables in each item (no splitting/globbing).
	type bracketItem struct {
		str  string     // expanded string value
		word lexer.Word // original word (for quoting info in pattern matching)
	}
	items := make([]bracketItem, len(cmd.Items))
	for i, w := range cmd.Items {
		expanded := expander.ExpandWord(w, lookup)
		items[i] = bracketItem{str: expanded, word: w}
	}

	// Convert to the evaluator format.
	strs := make([]string, len(items))
	words := make([]lexer.Word, len(items))
	for i, item := range items {
		strs[i] = item.str
		words[i] = item.word
	}

	p := &bracketParser{strs: strs, words: words, state: state}
	result, err := p.parseOr()
	if err != nil {
		fmt.Fprintf(stderr, "gosh: [[: %v\n", err)
		return 2
	}
	if p.pos < len(p.strs) {
		fmt.Fprintf(stderr, "gosh: [[: unexpected argument: %s\n", p.strs[p.pos])
		return 2
	}
	if result {
		return 0
	}
	return 1
}

// bracketParser is a recursive descent parser/evaluator for [[ ]] expressions.
type bracketParser struct {
	strs  []string     // expanded string values
	words []lexer.Word // original words (for quoting info)
	pos   int
	state *shellState  // for setting BASH_REMATCH
}

func (p *bracketParser) peek() string {
	if p.pos >= len(p.strs) {
		return ""
	}
	return p.strs[p.pos]
}

func (p *bracketParser) next() string {
	s := p.peek()
	if p.pos < len(p.strs) {
		p.pos++
	}
	return s
}

// peekWord returns the original Word for the current position.
func (p *bracketParser) peekWord() lexer.Word {
	if p.pos >= len(p.words) {
		return nil
	}
	return p.words[p.pos]
}

// nextWord returns the original Word and advances.
func (p *bracketParser) nextWord() lexer.Word {
	w := p.peekWord()
	if p.pos < len(p.words) {
		p.pos++
	}
	return w
}

// parseOr: expr || expr
func (p *bracketParser) parseOr() (bool, error) {
	left, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for p.peek() == "||" {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		left = left || right
	}
	return left, nil
}

// parseAnd: expr && expr
func (p *bracketParser) parseAnd() (bool, error) {
	left, err := p.parseNot()
	if err != nil {
		return false, err
	}
	for p.peek() == "&&" {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return false, err
		}
		left = left && right
	}
	return left, nil
}

// parseNot: ! expr | primary
func (p *bracketParser) parseNot() (bool, error) {
	if p.peek() == "!" {
		p.next()
		result, err := p.parseNot()
		if err != nil {
			return false, err
		}
		return !result, nil
	}
	return p.parsePrimary()
}

// parsePrimary handles unary tests, binary tests, and parentheses.
func (p *bracketParser) parsePrimary() (bool, error) {
	if p.pos >= len(p.strs) {
		return false, fmt.Errorf("expected expression")
	}
	tok := p.peek()

	// Parenthesized expression.
	if tok == "(" {
		p.next()
		result, err := p.parseOr()
		if err != nil {
			return false, err
		}
		if p.peek() != ")" {
			return false, fmt.Errorf("missing ')'")
		}
		p.next()
		return result, nil
	}

	// Unary file tests.
	switch tok {
	case "-e", "-f", "-d", "-r", "-w", "-x", "-s":
		p.next()
		arg := p.next()
		return evalFileTest(tok, arg)
	case "-z":
		p.next()
		arg := p.next()
		return arg == "", nil
	case "-n":
		p.next()
		arg := p.next()
		return arg != "", nil
	}

	// Read the left operand.
	left := p.next()

	op := p.peek()
	switch op {
	case "==", "=":
		p.next()
		rightWord := p.peekWord()
		right := p.next()
		return bracketPatternMatch(left, right, rightWord), nil
	case "!=":
		p.next()
		rightWord := p.peekWord()
		right := p.next()
		return !bracketPatternMatch(left, right, rightWord), nil
	case "<":
		p.next()
		right := p.next()
		return left < right, nil
	case ">":
		p.next()
		right := p.next()
		return left > right, nil
	case "=~":
		p.next()
		right := p.next()
		return bracketRegexMatch(p.state, left, right)
	case "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
		p.next()
		right := p.next()
		return evalIntCmp(op, left, right)
	}

	// Bare string: true if non-empty.
	return left != "", nil
}

// bracketPatternMatch performs pattern matching for [[ == ]] and [[ != ]].
// If the right-hand word has any quoted parts, the comparison is literal.
// If entirely unquoted, glob-style pattern matching is used.
func bracketPatternMatch(left, right string, rightWord lexer.Word) bool {
	if isFullyUnquoted(rightWord) {
		matched, _ := filepath.Match(right, left)
		return matched
	}
	// Quoted RHS: literal string comparison.
	return left == right
}

// bracketRegexMatch performs regex matching for [[ =~ ]].
// Sets BASH_REMATCH array with capture groups on match.
// Returns (matched, nil) on success, or (false, error) if the regex is invalid.
func bracketRegexMatch(state *shellState, left, right string) (bool, error) {
	re, err := regexp.Compile(right)
	if err != nil {
		return false, fmt.Errorf("invalid regex: %s", right)
	}
	matches := re.FindStringSubmatch(left)
	if matches == nil {
		if state != nil {
			delete(state.arrays, "BASH_REMATCH")
		}
		return false, nil
	}
	if state != nil {
		state.arrays["BASH_REMATCH"] = matches
	}
	return true, nil
}

// isFullyUnquoted returns true if all parts of a word are unquoted.
func isFullyUnquoted(w lexer.Word) bool {
	for _, part := range w {
		if part.Quote != lexer.Unquoted {
			return false
		}
	}
	return true
}

