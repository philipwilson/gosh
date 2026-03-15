package expander

import (
	"fmt"
	"strconv"
	"unicode"
)

// evalArith evaluates an arithmetic expression string and returns the
// integer result. Variable references (bare names or $name) are resolved
// via the lookup function. Undefined variables default to 0 (bash behavior).
func evalArith(expr string, lookup LookupFunc) (int64, error) {
	tokens, err := tokenizeArith(expr)
	if err != nil {
		return 0, err
	}
	p := &arithParser{tokens: tokens, lookup: lookup}
	result, err := p.parseTernary()
	if err != nil {
		return 0, err
	}
	if p.pos < len(p.tokens) {
		return 0, fmt.Errorf("unexpected token in arithmetic: %q", p.tokens[p.pos].val)
	}
	return result, nil
}

// --- Arithmetic tokenizer ---

type arithTokenType int

const (
	aTokNum   arithTokenType = iota // integer literal
	aTokIdent                       // variable name (possibly with leading $)
	aTokOp                          // operator
	aTokLParen
	aTokRParen
)

type arithToken struct {
	typ arithTokenType
	val string
}

func tokenizeArith(expr string) ([]arithToken, error) {
	runes := []rune(expr)
	var tokens []arithToken
	i := 0

	for i < len(runes) {
		ch := runes[i]

		// Skip whitespace.
		if unicode.IsSpace(ch) {
			i++
			continue
		}

		// Number.
		if ch >= '0' && ch <= '9' {
			start := i
			for i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
				i++
			}
			tokens = append(tokens, arithToken{aTokNum, string(runes[start:i])})
			continue
		}

		// Variable: $name or ${name} or bare identifier.
		if ch == '$' {
			i++
			if i < len(runes) && runes[i] == '{' {
				i++ // skip {
				start := i
				for i < len(runes) && runes[i] != '}' {
					i++
				}
				if i >= len(runes) {
					return nil, fmt.Errorf("unterminated ${} in arithmetic")
				}
				tokens = append(tokens, arithToken{aTokIdent, string(runes[start:i])})
				i++ // skip }
			} else if i < len(runes) && isArithNameStart(runes[i]) {
				start := i
				for i < len(runes) && isArithNameCont(runes[i]) {
					i++
				}
				tokens = append(tokens, arithToken{aTokIdent, string(runes[start:i])})
			} else if i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
				// Positional parameter: $1, $2, etc.
				start := i
				for i < len(runes) && runes[i] >= '0' && runes[i] <= '9' {
					i++
				}
				tokens = append(tokens, arithToken{aTokIdent, string(runes[start:i])})
			} else if i < len(runes) && (runes[i] == '#' || runes[i] == '@' || runes[i] == '*') {
				// Special variables: $#, $@, $*
				tokens = append(tokens, arithToken{aTokIdent, string(runes[i])})
				i++
			} else {
				return nil, fmt.Errorf("invalid $ in arithmetic expression")
			}
			continue
		}

		// Bare identifier (variable name without $).
		if isArithNameStart(ch) {
			start := i
			for i < len(runes) && isArithNameCont(runes[i]) {
				i++
			}
			tokens = append(tokens, arithToken{aTokIdent, string(runes[start:i])})
			continue
		}

		// Parentheses.
		if ch == '(' {
			tokens = append(tokens, arithToken{aTokLParen, "("})
			i++
			continue
		}
		if ch == ')' {
			tokens = append(tokens, arithToken{aTokRParen, ")"})
			i++
			continue
		}

		// Two-character operators.
		if i+1 < len(runes) {
			two := string(runes[i : i+2])
			switch two {
			case "<=", ">=", "==", "!=", "&&", "||", "<<", ">>":
				tokens = append(tokens, arithToken{aTokOp, two})
				i += 2
				continue
			}
		}

		// Single-character operators.
		switch ch {
		case '+', '-', '*', '/', '%', '<', '>', '!', '~', '&', '|', '^', '?', ':':
			tokens = append(tokens, arithToken{aTokOp, string(ch)})
			i++
		default:
			return nil, fmt.Errorf("unexpected character in arithmetic: %c", ch)
		}
	}

	return tokens, nil
}

func isArithNameStart(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isArithNameCont(ch rune) bool {
	return isArithNameStart(ch) || (ch >= '0' && ch <= '9')
}

// --- Arithmetic parser (recursive descent with precedence) ---

type arithParser struct {
	tokens []arithToken
	pos    int
	lookup LookupFunc
}

func (p *arithParser) peek() arithToken {
	if p.pos >= len(p.tokens) {
		return arithToken{}
	}
	return p.tokens[p.pos]
}

func (p *arithParser) next() arithToken {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

// parseTernary: expr ? expr : expr
func (p *arithParser) parseTernary() (int64, error) {
	cond, err := p.parseLogicalOr()
	if err != nil {
		return 0, err
	}
	if p.peek().val == "?" {
		p.next() // consume ?
		thenVal, err := p.parseTernary()
		if err != nil {
			return 0, err
		}
		if p.peek().val != ":" {
			return 0, fmt.Errorf("expected ':' in ternary expression")
		}
		p.next() // consume :
		elseVal, err := p.parseTernary()
		if err != nil {
			return 0, err
		}
		if cond != 0 {
			return thenVal, nil
		}
		return elseVal, nil
	}
	return cond, nil
}

// parseLogicalOr: expr || expr
func (p *arithParser) parseLogicalOr() (int64, error) {
	left, err := p.parseLogicalAnd()
	if err != nil {
		return 0, err
	}
	for p.peek().val == "||" {
		p.next()
		right, err := p.parseLogicalAnd()
		if err != nil {
			return 0, err
		}
		if left != 0 || right != 0 {
			left = 1
		} else {
			left = 0
		}
	}
	return left, nil
}

// parseLogicalAnd: expr && expr
func (p *arithParser) parseLogicalAnd() (int64, error) {
	left, err := p.parseBitwiseOr()
	if err != nil {
		return 0, err
	}
	for p.peek().val == "&&" {
		p.next()
		right, err := p.parseBitwiseOr()
		if err != nil {
			return 0, err
		}
		if left != 0 && right != 0 {
			left = 1
		} else {
			left = 0
		}
	}
	return left, nil
}

// parseBitwiseOr: expr | expr
func (p *arithParser) parseBitwiseOr() (int64, error) {
	left, err := p.parseBitwiseXor()
	if err != nil {
		return 0, err
	}
	for p.peek().val == "|" {
		p.next()
		right, err := p.parseBitwiseXor()
		if err != nil {
			return 0, err
		}
		left = left | right
	}
	return left, nil
}

// parseBitwiseXor: expr ^ expr
func (p *arithParser) parseBitwiseXor() (int64, error) {
	left, err := p.parseBitwiseAnd()
	if err != nil {
		return 0, err
	}
	for p.peek().val == "^" {
		p.next()
		right, err := p.parseBitwiseAnd()
		if err != nil {
			return 0, err
		}
		left = left ^ right
	}
	return left, nil
}

// parseBitwiseAnd: expr & expr
func (p *arithParser) parseBitwiseAnd() (int64, error) {
	left, err := p.parseEquality()
	if err != nil {
		return 0, err
	}
	for p.peek().val == "&" {
		p.next()
		right, err := p.parseEquality()
		if err != nil {
			return 0, err
		}
		left = left & right
	}
	return left, nil
}

// parseEquality: expr (== | !=) expr
func (p *arithParser) parseEquality() (int64, error) {
	left, err := p.parseRelational()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek().val
		if op != "==" && op != "!=" {
			break
		}
		p.next()
		right, err := p.parseRelational()
		if err != nil {
			return 0, err
		}
		switch op {
		case "==":
			left = boolToInt(left == right)
		case "!=":
			left = boolToInt(left != right)
		}
	}
	return left, nil
}

// parseRelational: expr (< | <= | > | >=) expr
func (p *arithParser) parseRelational() (int64, error) {
	left, err := p.parseShift()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek().val
		if op != "<" && op != "<=" && op != ">" && op != ">=" {
			break
		}
		p.next()
		right, err := p.parseShift()
		if err != nil {
			return 0, err
		}
		switch op {
		case "<":
			left = boolToInt(left < right)
		case "<=":
			left = boolToInt(left <= right)
		case ">":
			left = boolToInt(left > right)
		case ">=":
			left = boolToInt(left >= right)
		}
	}
	return left, nil
}

// parseShift: expr (<< | >>) expr
func (p *arithParser) parseShift() (int64, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek().val
		if op != "<<" && op != ">>" {
			break
		}
		p.next()
		right, err := p.parseAdditive()
		if err != nil {
			return 0, err
		}
		switch op {
		case "<<":
			left = left << uint(right)
		case ">>":
			left = left >> uint(right)
		}
	}
	return left, nil
}

// parseAdditive: expr (+ | -) expr
func (p *arithParser) parseAdditive() (int64, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek().val
		if op != "+" && op != "-" {
			break
		}
		p.next()
		right, err := p.parseMultiplicative()
		if err != nil {
			return 0, err
		}
		switch op {
		case "+":
			left = left + right
		case "-":
			left = left - right
		}
	}
	return left, nil
}

// parseMultiplicative: expr (* | / | %) expr
func (p *arithParser) parseMultiplicative() (int64, error) {
	left, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek().val
		if op != "*" && op != "/" && op != "%" {
			break
		}
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		switch op {
		case "*":
			left = left * right
		case "/":
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left = left / right
		case "%":
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left = left % right
		}
	}
	return left, nil
}

// parseUnary: (+ | - | ! | ~) unary | primary
func (p *arithParser) parseUnary() (int64, error) {
	op := p.peek().val
	switch op {
	case "+":
		p.next()
		return p.parseUnary()
	case "-":
		p.next()
		val, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return -val, nil
	case "!":
		p.next()
		val, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return boolToInt(val == 0), nil
	case "~":
		p.next()
		val, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return ^val, nil
	}
	return p.parsePrimary()
}

// parsePrimary: number | variable | ( expr )
func (p *arithParser) parsePrimary() (int64, error) {
	tok := p.peek()

	switch tok.typ {
	case aTokNum:
		p.next()
		n, err := strconv.ParseInt(tok.val, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", tok.val)
		}
		return n, nil

	case aTokIdent:
		p.next()
		val := p.lookup(tok.val)
		if val == "" {
			return 0, nil // undefined variables default to 0
		}
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("non-integer variable: %s=%q", tok.val, val)
		}
		return n, nil

	case aTokLParen:
		p.next() // consume (
		result, err := p.parseTernary()
		if err != nil {
			return 0, err
		}
		if p.peek().typ != aTokRParen {
			return 0, fmt.Errorf("missing ')' in arithmetic expression")
		}
		p.next() // consume )
		return result, nil
	}

	return 0, fmt.Errorf("expected number or variable in arithmetic, got %q", tok.val)
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
