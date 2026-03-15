// Package parser builds an abstract syntax tree from a token stream.
//
// The grammar (informal):
//
//	list       = pipeline ((';' | '&&' | '||') pipeline)*
//	pipeline   = command ('|' command)*
//	command    = (word | redirect)+
//	redirect   = ('<' | '>' | '>>') word
//
// The AST is intentionally simple: three node types cover everything
// we need through M5 (pipes and redirections). Control-flow operators
// (;, &&, ||) are captured so we can execute them properly in M10,
// but for now the executor can walk a List and run each pipeline.
package parser

import "fmt"

// Node is the interface satisfied by all AST nodes.
type Node interface {
	node() // marker method — keeps the interface closed
	String() string
}

// --- Redirect ---

// RedirType identifies the kind of redirection.
type RedirType int

const (
	REDIR_IN     RedirType = iota // <
	REDIR_OUT                     // >
	REDIR_APPEND                  // >>
)

// Redirect represents a single I/O redirection on a command.
type Redirect struct {
	Type RedirType
	File string // the target filename
}

func (r Redirect) String() string {
	switch r.Type {
	case REDIR_IN:
		return fmt.Sprintf("<%s", r.File)
	case REDIR_OUT:
		return fmt.Sprintf(">%s", r.File)
	case REDIR_APPEND:
		return fmt.Sprintf(">>%s", r.File)
	}
	return "?redir"
}

// --- SimpleCmd ---

// SimpleCmd is a single command: a list of argument words and
// zero or more I/O redirections.
//
//	echo hello world > out.txt
//	→ Args: ["echo", "hello", "world"], Redirects: [>out.txt]
type SimpleCmd struct {
	Args      []string
	Redirects []Redirect
}

func (c *SimpleCmd) node() {}
func (c *SimpleCmd) String() string {
	s := fmt.Sprintf("Cmd%v", c.Args)
	for _, r := range c.Redirects {
		s += " " + r.String()
	}
	return s
}

// --- Pipeline ---

// Pipeline is one or more commands connected by pipes.
//
//	ls -l | grep foo | wc -l
//	→ Cmds: [Cmd[ls -l], Cmd[grep foo], Cmd[wc -l]]
type Pipeline struct {
	Cmds []*SimpleCmd
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
//
//	make && make test ; echo done
//	→ [{make, "&&"}, {make test, ";"}, {echo done, ""}]
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
