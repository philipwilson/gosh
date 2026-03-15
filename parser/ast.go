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

// --- Clone support ---
// CloneList deep-copies a List so that in-place expansion (which
// modifies WordPart.Text) doesn't corrupt the original AST. This
// is needed for loops where the body is expanded on each iteration.

func CloneList(l *List) *List {
	if l == nil {
		return nil
	}
	entries := make([]ListEntry, len(l.Entries))
	for i, e := range l.Entries {
		entries[i] = ListEntry{
			Pipeline: clonePipeline(e.Pipeline),
			Op:       e.Op,
		}
	}
	return &List{Entries: entries}
}

func clonePipeline(p *Pipeline) *Pipeline {
	cmds := make([]Command, len(p.Cmds))
	for i, c := range p.Cmds {
		cmds[i] = cloneCommand(c)
	}
	return &Pipeline{Cmds: cmds}
}

func cloneCommand(c Command) Command {
	switch c := c.(type) {
	case *SimpleCmd:
		return cloneSimpleCmd(c)
	case *IfCmd:
		clauses := make([]IfClause, len(c.Clauses))
		for i, cl := range c.Clauses {
			clauses[i] = IfClause{
				Condition: CloneList(cl.Condition),
				Body:      CloneList(cl.Body),
			}
		}
		return &IfCmd{Clauses: clauses, ElseBody: CloneList(c.ElseBody)}
	case *WhileCmd:
		return &WhileCmd{
			Condition: CloneList(c.Condition),
			Body:      CloneList(c.Body),
		}
	case *ForCmd:
		words := make([]lexer.Word, len(c.Words))
		for i, w := range c.Words {
			words[i] = CloneWord(w)
		}
		return &ForCmd{
			VarName: c.VarName,
			Words:   words,
			Body:    CloneList(c.Body),
		}
	default:
		return c
	}
}

func cloneSimpleCmd(c *SimpleCmd) *SimpleCmd {
	sc := &SimpleCmd{}
	for _, a := range c.Assigns {
		sc.Assigns = append(sc.Assigns, Assignment{
			Name:  a.Name,
			Value: CloneWord(a.Value),
		})
	}
	for _, w := range c.Args {
		sc.Args = append(sc.Args, CloneWord(w))
	}
	for _, r := range c.Redirects {
		sc.Redirects = append(sc.Redirects, Redirect{
			Fd:   r.Fd,
			Type: r.Type,
			File: CloneWord(r.File),
		})
	}
	return sc
}

// CloneWord deep-copies a Word so in-place expansion doesn't corrupt
// the original.
func CloneWord(w lexer.Word) lexer.Word {
	if w == nil {
		return nil
	}
	cw := make(lexer.Word, len(w))
	copy(cw, w)
	return cw
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

// --- WhileCmd ---

// WhileCmd represents: while list; do list; done
type WhileCmd struct {
	Condition *List
	Body      *List
}

func (c *WhileCmd) node()    {}
func (c *WhileCmd) command() {}
func (c *WhileCmd) String() string {
	return "While[cond=" + c.Condition.String() + " body=" + c.Body.String() + "]"
}

// --- ForCmd ---

// ForCmd represents: for NAME in word...; do list; done
type ForCmd struct {
	VarName string       // loop variable name
	Words   []lexer.Word // words to iterate over (expanded before iteration)
	Body    *List
}

func (c *ForCmd) node()    {}
func (c *ForCmd) command() {}
func (c *ForCmd) String() string {
	var words []string
	for _, w := range c.Words {
		words = append(words, w.String())
	}
	return "For[" + c.VarName + " in " + strings.Join(words, " ") + " body=" + c.Body.String() + "]"
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
