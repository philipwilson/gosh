// Package parser builds an abstract syntax tree from a token stream.
//
// The grammar (informal):
//
//	list       = pipeline ((';' | '&&' | '||') pipeline)*
//	pipeline   = command ('|' command)*
//	command    = (assign)* (word | redirect)*
//	redirect   = [N] ('<' | '>' | '>>') word
//	           | [N] ('>&' | '<&') digit
//	assign     = NAME '=' word   (recognized only before the first non-assignment word)
//
// The AST is intentionally simple: three node types cover everything
// we need through M6. Control-flow operators (;, &&, ||) are captured
// so we can execute them properly in M10.
package parser

import (
	"fmt"
	"gosh/lexer"
	"strings"
)

// Node is the interface satisfied by all AST nodes.
type Node interface {
	node() // marker method — keeps the interface closed
	String() string
}

// Command is the interface for nodes that can appear in a pipeline.
// SimpleCmd and compound commands (IfCmd, etc.) implement this.
type Command interface {
	Node
	command() // marker method
}

// --- Redirect ---

// RedirType identifies the kind of redirection.
type RedirType int

const (
	REDIR_IN     RedirType = iota // <
	REDIR_OUT                     // >
	REDIR_APPEND                  // >>
	REDIR_DUP                    // >&N or <&N (fd duplication)
)

// Redirect represents a single I/O redirection on a command.
type Redirect struct {
	Fd   int       // source fd (-1 = default: 0 for input, 1 for output)
	Type RedirType
	File lexer.Word // target filename, or fd number string for REDIR_DUP
}

func (r Redirect) String() string {
	fdStr := ""
	if r.Fd >= 0 {
		fdStr = fmt.Sprintf("%d", r.Fd)
	}
	switch r.Type {
	case REDIR_IN:
		return fmt.Sprintf("%s<%s", fdStr, r.File)
	case REDIR_OUT:
		return fmt.Sprintf("%s>%s", fdStr, r.File)
	case REDIR_APPEND:
		return fmt.Sprintf("%s>>%s", fdStr, r.File)
	case REDIR_DUP:
		return fmt.Sprintf("%s>&%s", fdStr, r.File)
	}
	return "?redir"
}

// --- Assignment ---

// Assignment represents a variable assignment like FOO=bar.
type Assignment struct {
	Name  string
	Value lexer.Word // the value (may contain $VAR for expansion)
}

func (a Assignment) String() string {
	return fmt.Sprintf("%s=%s", a.Name, a.Value)
}

// --- SimpleCmd ---

// SimpleCmd is a single command: optional assignments, argument words,
// and zero or more I/O redirections.
//
//	FOO=bar echo hello world > out.txt
//	→ Assigns: [{FOO, bar}], Args: [echo, hello, world], Redirects: [>out.txt]
type SimpleCmd struct {
	Assigns   []Assignment
	Args      []lexer.Word
	Redirects []Redirect
}

func (c *SimpleCmd) node()    {}
func (c *SimpleCmd) command() {}
func (c *SimpleCmd) String() string {
	var parts []string
	for _, a := range c.Assigns {
		parts = append(parts, a.String())
	}
	for _, w := range c.Args {
		parts = append(parts, w.String())
	}
	s := "Cmd[" + strings.Join(parts, " ") + "]"
	for _, r := range c.Redirects {
		s += " " + r.String()
	}
	return s
}

// ArgStrings returns the args as plain strings (joining word parts).
func (c *SimpleCmd) ArgStrings() []string {
	out := make([]string, len(c.Args))
	for i, w := range c.Args {
		out[i] = w.String()
	}
	return out
}

// --- Pipeline ---

// Pipeline is one or more commands connected by pipes.
type Pipeline struct {
	Cmds []Command
}

func (p *Pipeline) node() {}
func (p *Pipeline) String() string {
	s := "Pipeline["
	for i, c := range p.Cmds {
		if i > 0 {
			s += " | "
		}
		s += c.String()
	}
	return s + "]"
}

// --- List ---

// ListEntry is a pipeline together with the operator that follows it.
// The last entry in a list has Op set to "".
type ListEntry struct {
	Pipeline *Pipeline
	Op       string // ";", "&&", "||", or "" for the last entry
}

// List is a sequence of pipelines separated by ;, &&, or ||.
type List struct {
	Entries []ListEntry
}

func (l *List) node() {}
func (l *List) String() string {
	s := "List["
	for i, e := range l.Entries {
		if i > 0 {
			s += " "
		}
		s += e.Pipeline.String()
		if e.Op != "" {
			s += " " + e.Op
		}
	}
	return s + "]"
}

// --- IfCmd ---

// IfClause is one condition+body pair (the "if" or an "elif").
type IfClause struct {
	Condition *List // commands between if/elif and then
	Body      *List // commands between then and the next elif/else/fi
}

// IfCmd represents: if list; then list; [elif list; then list;]... [else list;] fi
type IfCmd struct {
	Clauses  []IfClause // if + zero or more elif
	ElseBody *List      // nil if no else branch
}

func (c *IfCmd) node()    {}
func (c *IfCmd) command() {}
func (c *IfCmd) String() string {
	s := "If["
	for i, cl := range c.Clauses {
		if i > 0 {
			s += " Elif["
		}
		s += "cond=" + cl.Condition.String() + " body=" + cl.Body.String()
		if i > 0 {
			s += "]"
		}
	}
	if c.ElseBody != nil {
		s += " Else[" + c.ElseBody.String() + "]"
	}
	return s + "]"
}
