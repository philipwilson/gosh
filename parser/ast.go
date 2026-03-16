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
	REDIR_IN      RedirType = iota // <
	REDIR_OUT                      // >
	REDIR_APPEND                   // >>
	REDIR_DUP                     // >&N or <&N (fd duplication)
	REDIR_HEREDOC                  // << (here document)
	REDIR_HERESTRING               // <<< (here string)
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
	case REDIR_HEREDOC:
		return fmt.Sprintf("%s<<heredoc", fdStr)
	case REDIR_HERESTRING:
		return fmt.Sprintf("%s<<<%s", fdStr, r.File)
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
	case *CaseCmd:
		clauses := make([]CaseClause, len(c.Clauses))
		for i, cl := range c.Clauses {
			pats := make([]lexer.Word, len(cl.Patterns))
			for j, p := range cl.Patterns {
				pats[j] = CloneWord(p)
			}
			clauses[i] = CaseClause{
				Patterns: pats,
				Body:     CloneList(cl.Body),
			}
		}
		return &CaseCmd{
			Word:    CloneWord(c.Word),
			Clauses: clauses,
		}
	case *FuncDef:
		return &FuncDef{
			Name: c.Name,
			Body: CloneList(c.Body),
		}
	case *ArithForCmd:
		return &ArithForCmd{
			Init: c.Init,
			Cond: c.Cond,
			Step: c.Step,
			Body: CloneList(c.Body),
		}
	case *DblBracketCmd:
		items := make([]lexer.Word, len(c.Items))
		for i, w := range c.Items {
			items[i] = CloneWord(w)
		}
		return &DblBracketCmd{Items: items}
	case *SubshellCmd:
		return &SubshellCmd{Body: CloneList(c.Body)}
	case *ArithCmd:
		return &ArithCmd{Expr: c.Expr}
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

// --- ArithForCmd ---

// ArithForCmd represents: for (( init; cond; step )) do list done
type ArithForCmd struct {
	Init string // initialization expression (may be empty)
	Cond string // condition expression (may be empty — infinite loop)
	Step string // step/update expression (may be empty)
	Body *List
}

func (c *ArithForCmd) node()    {}
func (c *ArithForCmd) command() {}
func (c *ArithForCmd) String() string {
	return "ArithFor[init=" + c.Init + " cond=" + c.Cond + " step=" + c.Step + " body=" + c.Body.String() + "]"
}

// --- CaseCmd ---

// CaseClause is one pattern-list + body pair in a case statement.
type CaseClause struct {
	Patterns []lexer.Word // one or more patterns (separated by | in input)
	Body     *List
}

// CaseCmd represents: case word in (pattern) list ;; ... esac
type CaseCmd struct {
	Word    lexer.Word   // the word being matched
	Clauses []CaseClause
}

func (c *CaseCmd) node()    {}
func (c *CaseCmd) command() {}
func (c *CaseCmd) String() string {
	s := "Case[" + c.Word.String()
	for _, cl := range c.Clauses {
		var pats []string
		for _, p := range cl.Patterns {
			pats = append(pats, p.String())
		}
		s += " (" + strings.Join(pats, "|") + ") " + cl.Body.String()
	}
	return s + "]"
}

// --- FuncDef ---

// FuncDef represents a function definition: fname() { list; }
type FuncDef struct {
	Name string
	Body *List
}

func (c *FuncDef) node()    {}
func (c *FuncDef) command() {}
func (c *FuncDef) String() string {
	return "FuncDef[" + c.Name + " " + c.Body.String() + "]"
}

// --- DblBracketCmd ---

// DblBracketCmd represents: [[ expr ]] — extended conditional test.
// Items are the expression tokens between [[ and ]], preserving quoting
// for pattern matching on the RHS of == and !=.
type DblBracketCmd struct {
	Items []lexer.Word
}

func (c *DblBracketCmd) node()    {}
func (c *DblBracketCmd) command() {}
func (c *DblBracketCmd) String() string {
	var parts []string
	for _, w := range c.Items {
		parts = append(parts, w.String())
	}
	return "[[ " + strings.Join(parts, " ") + " ]]"
}

// --- SubshellCmd ---

// SubshellCmd represents: ( list ) — runs commands in a subshell.
// Variable changes inside the subshell do not affect the parent.
type SubshellCmd struct {
	Body *List
}

func (c *SubshellCmd) node()    {}
func (c *SubshellCmd) command() {}
func (c *SubshellCmd) String() string {
	return "Subshell[" + c.Body.String() + "]"
}

// --- ArithCmd ---

// ArithCmd represents: (( expr )) — an arithmetic command.
// Returns 0 (true) if expr evaluates to non-zero, 1 (false) if zero.
type ArithCmd struct {
	Expr string // the arithmetic expression text
}

func (c *ArithCmd) node()    {}
func (c *ArithCmd) command() {}
func (c *ArithCmd) String() string {
	return "((" + c.Expr + "))"
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
