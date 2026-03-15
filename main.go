package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gosh/editor"
	"gosh/lexer"
	"gosh/parser"
)

// version is set at build time via -ldflags "-X main.version=...".
// Defaults to "dev" for plain `go build` without flags.
var version = "dev"

// shellState holds the shell's mutable state: variables, export
// set, last exit status, and terminal control info.
type shellState struct {
	vars             map[string]string    // shell variables
	exported         map[string]bool      // which variables are exported to children
	funcs            map[string]*parser.List // user-defined functions
	lastStatus       int                  // $? — exit status of last command
	interactive      bool                 // true if stdin is a terminal
	shellPgid        int                  // the shell's own process group ID
	termFd           int                  // file descriptor of the controlling terminal
	exitFlag         bool                 // set by exit builtin to stop the REPL
	breakFlag        bool                 // set by break builtin to exit loop
	continueFlag     bool                 // set by continue builtin to skip to next iteration
	returnFlag       bool                 // set by return builtin to exit function
	loopDepth        int                  // nesting depth of for/while loops
	positionalParams []string             // $1, $2, ... for function arguments
	localScopes      []map[string]savedVar // stack of local variable scopes (one per function call)
	jobs             []*job               // job table for background/stopped jobs
	nextJobID        int                  // next job number to assign
	debugTokens      bool                 // print tokens before parsing
	debugAST         bool                 // print AST before expansion
	debugExpanded    bool                 // print AST after expansion
	substDepth       int                  // >0 when inside command substitution
	ed               *editor.Editor       // line editor (nil if non-interactive)
}

func newShellState() *shellState {
	s := &shellState{
		vars:     make(map[string]string),
		exported: make(map[string]bool),
		funcs:    make(map[string]*parser.List),
		termFd:   int(os.Stdin.Fd()),
	}

	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			s.vars[k] = v
			s.exported[k] = true
		}
	}

	// Set default PS1 and PS2 if not inherited from environment.
	if _, ok := s.vars["PS1"]; !ok {
		s.vars["PS1"] = `\u@\h:\w\$ `
	}
	if _, ok := s.vars["PS2"]; !ok {
		s.vars["PS2"] = "> "
	}

	s.interactive = isatty(s.termFd)

	if s.interactive {
		s.shellPgid = syscall.Getpgrp()

		// SIGTTOU must be SIG_IGN so the shell can call tcsetpgrp
		// from a background process group. SIG_IGN persists across
		// exec, but that's acceptable — children in the foreground
		// group won't receive SIGTTOU anyway.
		signal.Ignore(syscall.SIGTTOU)

		// SIGINT and SIGTSTP use signal.Notify (not signal.Ignore).
		// signal.Ignore sets SIG_IGN at the OS level, which persists
		// across exec (POSIX: only caught handlers are reset to
		// SIG_DFL by exec). That would make Ctrl-C and Ctrl-Z
		// ineffective in child processes.
		//
		// signal.Notify installs Go's own caught handler. After exec,
		// POSIX resets caught handlers to SIG_DFL, so children get
		// default signal behavior.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTSTP)
	}

	return s
}

func (s *shellState) lookup(name string) string {
	switch name {
	case "?":
		return strconv.Itoa(s.lastStatus)
	case "$":
		return strconv.Itoa(os.Getpid())
	case "#":
		return strconv.Itoa(len(s.positionalParams))
	case "@", "*":
		return strings.Join(s.positionalParams, " ")
	case "0":
		return "gosh"
	default:
		// Positional parameters: $1, $2, ..., ${10}, etc.
		if n, err := strconv.Atoi(name); err == nil && n >= 1 {
			if n <= len(s.positionalParams) {
				return s.positionalParams[n-1]
			}
			return ""
		}
		return s.vars[name]
	}
}

func (s *shellState) environ() []string {
	var env []string
	for k := range s.exported {
		env = append(env, k+"="+s.vars[k])
	}
	return env
}

func (s *shellState) setVar(name, value string) {
	s.vars[name] = value
}

func (s *shellState) exportVar(name string) {
	s.exported[name] = true
}

func (s *shellState) unsetVar(name string) {
	delete(s.vars, name)
	delete(s.exported, name)
}

// formatPrompt expands bash-style backslash escapes in a prompt string
// (PS1/PS2). Supported sequences:
//
//	\u  — username ($USER)
//	\h  — hostname up to first '.'
//	\H  — full hostname
//	\w  — current working directory, with $HOME replaced by ~
//	\W  — basename of current working directory (~ if $HOME)
//	\$  — '#' if uid 0, '$' otherwise
//	\n  — newline
//	\t  — time in 24-hour HH:MM:SS
//	\e  — escape character (ASCII 27, for ANSI color codes)
//	\\  — literal backslash
//	\[  — begin non-printing sequence (ignored — terminal handles it)
//	\]  — end non-printing sequence (ignored — terminal handles it)
func (s *shellState) formatPrompt(raw string) string {
	var sb strings.Builder
	sb.Grow(len(raw))

	i := 0
	for i < len(raw) {
		if raw[i] != '\\' || i+1 >= len(raw) {
			sb.WriteByte(raw[i])
			i++
			continue
		}

		i++ // skip backslash
		switch raw[i] {
		case 'u':
			sb.WriteString(s.vars["USER"])
		case 'h':
			host, _ := os.Hostname()
			if idx := strings.IndexByte(host, '.'); idx >= 0 {
				host = host[:idx]
			}
			sb.WriteString(host)
		case 'H':
			host, _ := os.Hostname()
			sb.WriteString(host)
		case 'w':
			cwd := s.vars["PWD"]
			if cwd == "" {
				cwd, _ = os.Getwd()
			}
			home := s.vars["HOME"]
			if home != "" && (cwd == home || strings.HasPrefix(cwd, home+"/")) {
				cwd = "~" + cwd[len(home):]
			}
			sb.WriteString(cwd)
		case 'W':
			cwd := s.vars["PWD"]
			if cwd == "" {
				cwd, _ = os.Getwd()
			}
			home := s.vars["HOME"]
			if cwd == home {
				sb.WriteByte('~')
			} else {
				sb.WriteString(filepath.Base(cwd))
			}
		case '$':
			if os.Getuid() == 0 {
				sb.WriteByte('#')
			} else {
				sb.WriteByte('$')
			}
		case 'n':
			sb.WriteByte('\n')
		case 't':
			now := time.Now()
			fmt.Fprintf(&sb, "%02d:%02d:%02d", now.Hour(), now.Minute(), now.Second())
		case 'e':
			sb.WriteByte(0x1b)
		case '[', ']':
			// Non-printing markers — ignored since our editor handles
			// ANSI escapes correctly via relative cursor positioning.
		case '\\':
			sb.WriteByte('\\')
		default:
			// Unknown escape — keep as-is.
			sb.WriteByte('\\')
			sb.WriteByte(raw[i])
		}
		i++
	}

	return sb.String()
}

// cmdSubst executes a command string and returns its stdout output
// with trailing newlines stripped. Used for $(cmd) and `cmd` expansion.
func (s *shellState) cmdSubst(cmd string) (string, error) {
	tokens, err := lexer.Lex(cmd)
	if err != nil {
		return "", err
	}
	if len(tokens) == 1 && tokens[0].Type == lexer.TOKEN_EOF {
		return "", nil
	}

	list, err := parser.Parse(tokens)
	if err != nil {
		return "", err
	}

	// Create a pipe to capture stdout.
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}

	// Execute with stdout directed to the pipe. execList handles
	// per-entry expansion, so no need to expand here.
	s.substDepth++
	execList(s, list, os.Stdin, w)
	s.substDepth--
	w.Close()

	// Read all output from the pipe.
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		return "", err
	}

	// Strip trailing newlines (bash behavior).
	return strings.TrimRight(string(out), "\n"), nil
}

// --- Main loop ---

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("gosh %s\n", version)
		return
	}

	state := newShellState()

	// If a script file is given as an argument, run it.
	if len(os.Args) >= 2 {
		os.Exit(runScript(state, os.Args[1]))
	}

	if state.interactive {
		histPath := filepath.Join(state.vars["HOME"], ".gosh_history")
		ed, err := editor.New(state.termFd, histPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: editor init failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "gosh: falling back to simple input")
		} else {
			state.ed = ed
			state.ed.Complete = state.complete
			defer ed.Close()
		}

		// Source ~/.goshrc if it exists.
		rcPath := filepath.Join(state.vars["HOME"], ".goshrc")
		if _, err := os.Stat(rcPath); err == nil {
			runScript(state, rcPath)
		}
	}

	if state.ed != nil {
		runInteractive(state)
	} else {
		runNonInteractive(state)
	}

	os.Exit(state.lastStatus)
}

// runScript executes a script file and returns the exit status.
func runScript(state *shellState, path string) int {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
		return 127
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for {
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Collect continuation lines for incomplete input.
		for needsMore(line) {
			if !scanner.Scan() {
				break
			}
			more := scanner.Text()
			if strings.HasSuffix(line, "\\") {
				line = line[:len(line)-1] + more
			} else {
				line = line + "\n" + more
			}
		}

		tokens, err := lexer.Lex(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			continue
		}

		if lexer.HasHeredocs(tokens) {
			hdErr := lexer.ResolveHeredocs(tokens, func() (string, bool) {
				if !scanner.Scan() {
					return "", false
				}
				return scanner.Text(), true
			})
			if hdErr != nil {
				fmt.Fprintf(os.Stderr, "gosh: %v\n", hdErr)
				continue
			}
		}

		if runTokens(state, tokens) {
			break
		}
	}

	return state.lastStatus
}

func runInteractive(state *shellState) {
	for {
		state.reapJobs()
		prompt := state.formatPrompt(state.vars["PS1"])
		line, err := state.ed.ReadLine(prompt)
		if err == io.EOF {
			fmt.Fprintln(os.Stderr)
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: read: %v\n", err)
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Collect continuation lines for incomplete input.
		for needsMore(line) {
			ps2 := state.formatPrompt(state.vars["PS2"])
			more, err := state.ed.ReadLine(ps2)
			if err == io.EOF {
				fmt.Fprintln(os.Stderr)
				break
			}
			if err != nil {
				break
			}
			// Trailing backslash: strip it and join directly.
			if strings.HasSuffix(line, "\\") {
				line = line[:len(line)-1] + more
			} else {
				line = line + "\n" + more
			}
		}

		tokens, err := lexer.Lex(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			continue
		}

		// Resolve heredoc bodies by reading continuation lines.
		if lexer.HasHeredocs(tokens) {
			hdErr := lexer.ResolveHeredocs(tokens, func() (string, bool) {
				ps2 := state.formatPrompt(state.vars["PS2"])
				more, err := state.ed.ReadLine(ps2)
				if err != nil {
					return "", false
				}
				return more, true
			})
			if hdErr != nil {
				fmt.Fprintf(os.Stderr, "gosh: %v\n", hdErr)
				continue
			}
		}

		if runTokens(state, tokens) {
			break
		}

		state.ed.History.Add(line)
	}
}

func runNonInteractive(state *shellState) {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		if state.interactive {
			fmt.Fprintf(os.Stderr, "%s", state.formatPrompt(state.vars["PS1"]))
		}

		if !scanner.Scan() {
			if state.interactive {
				fmt.Fprintln(os.Stderr)
			}
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Collect continuation lines for incomplete input.
		for needsMore(line) {
			if !scanner.Scan() {
				break
			}
			more := scanner.Text()
			if strings.HasSuffix(line, "\\") {
				line = line[:len(line)-1] + more
			} else {
				line = line + "\n" + more
			}
		}

		tokens, err := lexer.Lex(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
			continue
		}

		if lexer.HasHeredocs(tokens) {
			hdErr := lexer.ResolveHeredocs(tokens, func() (string, bool) {
				if !scanner.Scan() {
					return "", false
				}
				return scanner.Text(), true
			})
			if hdErr != nil {
				fmt.Fprintf(os.Stderr, "gosh: %v\n", hdErr)
				continue
			}
		}

		if runTokens(state, tokens) {
			break
		}
	}
}

// needsMore returns true if the input is incomplete and should be
// continued on the next line. Checks for trailing backslash, unclosed
// quotes, trailing operators, and unclosed compound commands.
func needsMore(line string) bool {
	// Trailing backslash: explicit line continuation.
	if strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") {
		return true
	}

	// Try to lex. Unterminated quotes need continuation.
	tokens, err := lexer.Lex(line)
	if err != nil {
		msg := err.Error()
		return strings.Contains(msg, "unterminated")
	}

	// Check for trailing operators that expect more input.
	if len(tokens) >= 2 {
		// Last token is EOF; check the one before it.
		prev := tokens[len(tokens)-2]
		switch prev.Type {
		case lexer.TOKEN_PIPE, lexer.TOKEN_AND, lexer.TOKEN_OR:
			return true
		}
	}

	// Try to parse. Certain errors indicate incomplete input.
	_, err = parser.Parse(tokens)
	if err != nil {
		msg := err.Error()
		// "expected 'then'" etc. at EOF means the compound command
		// isn't closed yet.
		if strings.Contains(msg, "got EOF") {
			return true
		}
	}

	return false
}

// runLine lexes, parses, expands, and executes a single input line.
// Returns true if the shell should exit.
func runLine(state *shellState, line string) bool {
	tokens, err := lexer.Lex(line)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
		return false
	}
	return runTokens(state, tokens)
}

// runTokens parses and executes a pre-lexed token stream.
// Returns true if the shell should exit.
func runTokens(state *shellState, tokens []lexer.Token) bool {
	if len(tokens) == 1 && tokens[0].Type == lexer.TOKEN_EOF {
		return false
	}

	if state.debugTokens {
		for _, tok := range tokens {
			fmt.Fprintf(os.Stderr, "  %s\n", tok)
		}
	}

	list, err := parser.Parse(tokens)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosh: %v\n", err)
		return false
	}

	if state.debugAST {
		fmt.Fprintf(os.Stderr, "  %s\n", list)
	}

	// execList handles per-entry expansion (lazy), so no
	// expander.Expand call here. debugExpanded is also in execList.
	execList(state, list, os.Stdin, os.Stdout)

	return state.exitFlag
}
