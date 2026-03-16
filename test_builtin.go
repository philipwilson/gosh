package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// builtinTest implements the 'test' builtin (no closing bracket required).
func builtinTest(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	result, err := evalTest(args)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: test: %v\n", err)
		return 2
	}
	if result {
		return 0
	}
	return 1
}

// builtinBracket implements the '[' builtin (requires closing ']').
func builtinBracket(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) == 0 || args[len(args)-1] != "]" {
		fmt.Fprintln(stderr, "gosh: [: missing ']'")
		return 2
	}
	return builtinTest(state, args[:len(args)-1], stdin, stdout, stderr)
}

// evalTest evaluates a test expression and returns true/false.
func evalTest(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}

	p := &testParser{args: args}
	result, err := p.parseOr()
	if err != nil {
		return false, err
	}
	if p.pos < len(p.args) {
		return false, fmt.Errorf("extra argument: %s", p.args[p.pos])
	}
	return result, nil
}

// testParser is a simple recursive descent parser for test expressions.
type testParser struct {
	args []string
	pos  int
}

func (p *testParser) peek() string {
	if p.pos >= len(p.args) {
		return ""
	}
	return p.args[p.pos]
}

func (p *testParser) next() string {
	s := p.peek()
	if p.pos < len(p.args) {
		p.pos++
	}
	return s
}

// parseOr: expr -o expr
func (p *testParser) parseOr() (bool, error) {
	left, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for p.peek() == "-o" {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		left = left || right
	}
	return left, nil
}

// parseAnd: expr -a expr
func (p *testParser) parseAnd() (bool, error) {
	left, err := p.parseNot()
	if err != nil {
		return false, err
	}
	for p.peek() == "-a" {
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
func (p *testParser) parseNot() (bool, error) {
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
func (p *testParser) parsePrimary() (bool, error) {
	tok := p.peek()

	if tok == "" {
		return false, fmt.Errorf("expected expression")
	}

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
		if p.pos > len(p.args) {
			return false, fmt.Errorf("expected argument after %s", tok)
		}
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

	// At this point we have a value. Check if the next token is a
	// binary operator.
	left := p.next()

	op := p.peek()
	switch op {
	case "=":
		p.next()
		right := p.next()
		return left == right, nil
	case "!=":
		p.next()
		right := p.next()
		return left != right, nil
	case "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
		p.next()
		right := p.next()
		return evalIntCmp(op, left, right)
	}

	// Bare string: true if non-empty.
	return left != "", nil
}

func evalFileTest(op, path string) (bool, error) {
	info, err := os.Stat(path)
	switch op {
	case "-e":
		return err == nil, nil
	case "-f":
		return err == nil && info.Mode().IsRegular(), nil
	case "-d":
		return err == nil && info.IsDir(), nil
	case "-s":
		return err == nil && info.Size() > 0, nil
	case "-r":
		return checkAccess(path, 0x04), nil // R_OK
	case "-w":
		return checkAccess(path, 0x02), nil // W_OK
	case "-x":
		return checkAccess(path, 0x01), nil // X_OK
	}
	return false, nil
}

// checkAccess uses syscall.Access to test file permissions.
func checkAccess(path string, mode uint32) bool {
	return syscall.Access(path, mode) == nil
}

func evalIntCmp(op, left, right string) (bool, error) {
	a, err := strconv.Atoi(left)
	if err != nil {
		return false, fmt.Errorf("integer expression expected: %s", left)
	}
	b, err := strconv.Atoi(right)
	if err != nil {
		return false, fmt.Errorf("integer expression expected: %s", right)
	}
	switch op {
	case "-eq":
		return a == b, nil
	case "-ne":
		return a != b, nil
	case "-lt":
		return a < b, nil
	case "-le":
		return a <= b, nil
	case "-gt":
		return a > b, nil
	case "-ge":
		return a >= b, nil
	}
	return false, nil
}
