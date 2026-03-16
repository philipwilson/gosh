package main

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	vars             map[string]string       // shell variables
	arrays           map[string][]string     // indexed arrays
	exported         map[string]bool         // which variables are exported to children
	aliases          map[string]string       // alias name → replacement text
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
	traps            map[string]string    // signal name → command string
	pendingSignals   map[string]bool      // signals received, not yet handled
	pendingMu        sync.Mutex           // guards pendingSignals
	trapsMu          sync.RWMutex         // guards traps map
	trapRunning      bool                 // prevent recursive trap execution
	sigCh            chan os.Signal       // signal notification channel
	optErrexit       bool                 // set -e: exit on error
	optNounset       bool                 // set -u: error on unset variables
	optXtrace        bool                 // set -x: print commands before execution
	optPipefail      bool                 // set -o pipefail: pipeline fails if any command fails
	noErrexit        int                  // >0 suppresses errexit (condition contexts, &&/|| LHS)
	nounsetError     bool                 // set when a nounset violation occurs during expansion
	lastBgPid        int                  // $! — PID of last background command
	startTime        time.Time            // for $SECONDS
}

func newShellState() *shellState {
	s := &shellState{
		vars:     make(map[string]string),
		arrays:   make(map[string][]string),
		exported: make(map[string]bool),
		aliases:  make(map[string]string),
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
	if _, ok := s.vars["PS3"]; !ok {
		s.vars["PS3"] = "#? "
	}

	s.startTime = time.Now()
	s.traps = make(map[string]string)
	s.pendingSignals = make(map[string]bool)
	s.sigCh = make(chan os.Signal, 8)

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
		signal.Notify(s.sigCh, syscall.SIGINT, syscall.SIGTSTP)
	}

	// Goroutine to receive signals and set pending flags.
	go func() {
		for sig := range s.sigCh {
			if name := signalName(sig); name != "" {
				s.pendingMu.Lock()
				s.pendingSignals[name] = true
				s.pendingMu.Unlock()
			}
		}
	}()

	return s
}

func (s *shellState) lookup(name string) string {
	switch name {
	case "?":
		return strconv.Itoa(s.lastStatus)
	case "!":
		return strconv.Itoa(s.lastBgPid)
	case "$":
		return strconv.Itoa(os.Getpid())
	case "#":
		return strconv.Itoa(len(s.positionalParams))
	case "@", "*":
		return strings.Join(s.positionalParams, " ")
	case "0":
		if v, ok := s.vars["0"]; ok {
			return v
		}
		return "gosh"
	case "RANDOM":
		return strconv.Itoa(rand.Intn(32768))
	case "SECONDS":
		return strconv.Itoa(int(time.Since(s.startTime).Seconds()))
	default:
		// Positional parameters: $1, $2, ..., ${10}, etc.
		if n, err := strconv.Atoi(name); err == nil && n >= 1 {
			if n <= len(s.positionalParams) {
				return s.positionalParams[n-1]
			}
			return ""
		}

		// Array element count: #arr[@] or #arr[*]
		// Used by expandParam for ${#arr[@]}.
		if strings.HasPrefix(name, "#") {
			rest := name[1:]
			if arrName, subscript, ok := parseArrayRef(rest); ok {
				if subscript == "@" || subscript == "*" {
					count := 0
					for _, v := range s.arrays[arrName] {
						if v != "" {
							count++
						}
					}
					return strconv.Itoa(count)
				}
			}
		}

		// Array subscripts: arr[N], arr[@], arr[*]
		if arrName, subscript, ok := parseArrayRef(name); ok {
			arr := s.arrays[arrName]
			switch subscript {
			case "@", "*":
				// Filter out unset (empty) elements for sparse arrays.
				var live []string
				for _, v := range arr {
					if v != "" {
						live = append(live, v)
					}
				}
				return strings.Join(live, " ")
			default:
				idx, err := strconv.Atoi(subscript)
				if err != nil || idx < 0 || idx >= len(arr) {
					return ""
				}
				return arr[idx]
			}
		}

		// Bare array name returns element 0.
		if arr, ok := s.arrays[name]; ok {
			if len(arr) > 0 {
				return arr[0]
			}
			return ""
		}

		if val, ok := s.vars[name]; ok {
			return val
		}
		return ""
	}
}

// lookupNounset wraps lookup with nounset checking. Used by the expander
// for bare variable references ($VAR, ${VAR}) where no default operator
// is present, so that `set -u` fires for unset variables.
func (s *shellState) lookupNounset(name string) string {
	val := s.lookup(name)
	if val == "" && s.optNounset && !s.isVarSet(name) {
		fmt.Fprintf(os.Stderr, "gosh: %s: unbound variable\n", name)
		s.nounsetError = true
	}
	return val
}

// isVarSet returns true if the named variable exists in the shell state.
// Used by [[ -v var ]] to test whether a variable is set.
func (s *shellState) isVarSet(name string) bool {
	switch name {
	case "?", "$", "!", "#", "@", "*", "0", "RANDOM", "SECONDS":
		return true
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 1 {
		return n <= len(s.positionalParams)
	}
	if arrName, _, ok := parseArrayRef(name); ok {
		_, exists := s.arrays[arrName]
		return exists
	}
	if _, ok := s.arrays[name]; ok {
		return true
	}
	_, ok := s.vars[name]
	return ok
}

// parseArrayRef extracts the array name and subscript from strings
// like "arr[0]", "arr[@]", "arr[expr]". Returns ("", "", false) if
// the string is not an array reference.
func parseArrayRef(s string) (name, subscript string, ok bool) {
	idx := strings.IndexByte(s, '[')
	if idx < 0 {
		return "", "", false
	}
	if !strings.HasSuffix(s, "]") {
		return "", "", false
	}
	name = s[:idx]
	subscript = s[idx+1 : len(s)-1]
	return name, subscript, true
}

// lookupArray returns array elements for "${arr[@]}" and "$@" expansion.
// Returns (elements, true) for @ subscripts where each element should
// become a separate word. Returns (nil, false) for * subscripts and
// non-array variables.
func (s *shellState) lookupArray(name string) ([]string, bool) {
	// $@ — positional parameters as separate words.
	if name == "@" {
		return s.positionalParams, true
	}
	// ${arr[@]}
	if arrName, subscript, ok := parseArrayRef(name); ok {
		if subscript == "@" {
			arr := s.arrays[arrName]
			// Filter empty elements (sparse arrays).
			var live []string
			for _, v := range arr {
				if v != "" {
					live = append(live, v)
				}
			}
			return live, true
		}
	}
	return nil, false
}

func (s *shellState) environ() []string {
	var env []string
	for k := range s.exported {
		// Arrays are not exported to children (bash behavior).
		if _, isArray := s.arrays[k]; isArray {
			continue
		}
		env = append(env, k+"="+s.vars[k])
	}
	return env
}

func (s *shellState) setVar(name, value string) {
	// Special variable: SECONDS resets the timer.
	if name == "SECONDS" {
		n, err := strconv.Atoi(value)
		if err != nil {
			n = 0
		}
		s.startTime = time.Now().Add(-time.Duration(n) * time.Second)
		return
	}
	// Array element assignment: arr[N]=value
	if arrName, subscript, ok := parseArrayRef(name); ok {
		idx, err := strconv.Atoi(subscript)
		if err != nil || idx < 0 {
			return
		}
		arr := s.arrays[arrName]
		// Grow the array if needed.
		for len(arr) <= idx {
			arr = append(arr, "")
		}
		arr[idx] = value
		s.arrays[arrName] = arr
		return
	}
	s.vars[name] = value
}

// setArray sets an array variable, replacing any existing value.
func (s *shellState) setArray(name string, vals []string) {
	s.arrays[name] = vals
}

// appendArray appends values to an existing array (or creates one).
func (s *shellState) appendArray(name string, vals []string) {
	s.arrays[name] = append(s.arrays[name], vals...)
}

func (s *shellState) exportVar(name string) {
	s.exported[name] = true
}

func (s *shellState) unsetVar(name string) {
	// Array element: unset arr[N] sets element to ""
	if arrName, subscript, ok := parseArrayRef(name); ok {
		arr := s.arrays[arrName]
		if subscript == "@" || subscript == "*" {
			delete(s.arrays, arrName)
			return
		}
		idx, err := strconv.Atoi(subscript)
		if err != nil || idx < 0 || idx >= len(arr) {
			return
		}
		arr[idx] = ""
		s.arrays[arrName] = arr
		return
	}
	delete(s.vars, name)
	delete(s.arrays, name)
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
	execList(s, list, os.Stdin, w, os.Stderr)
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

// --- Trap support ---

// signalName maps an os.Signal to its canonical trap name.
func signalName(sig os.Signal) string {
	if s, ok := sig.(syscall.Signal); ok {
		switch s {
		case syscall.SIGINT:
			return "INT"
		case syscall.SIGTERM:
			return "TERM"
		case syscall.SIGHUP:
			return "HUP"
		case syscall.SIGQUIT:
			return "QUIT"
		case syscall.SIGUSR1:
			return "USR1"
		case syscall.SIGUSR2:
			return "USR2"
		}
	}
	return ""
}

// parseSignalSpec normalizes a signal specification (e.g. "INT", "SIGINT",
// "int", "2") to a canonical name and syscall.Signal.
func parseSignalSpec(spec string) (string, syscall.Signal, error) {
	upper := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(spec), "SIG"))

	nameToSig := map[string]syscall.Signal{
		"INT":  syscall.SIGINT,
		"TERM": syscall.SIGTERM,
		"HUP":  syscall.SIGHUP,
		"QUIT": syscall.SIGQUIT,
		"USR1": syscall.SIGUSR1,
		"USR2": syscall.SIGUSR2,
	}

	if sig, ok := nameToSig[upper]; ok {
		return upper, sig, nil
	}

	// Pseudo-signals (no syscall.Signal).
	switch upper {
	case "EXIT", "ERR", "RETURN":
		return upper, 0, nil
	}

	// Try numeric.
	if n, err := strconv.Atoi(spec); err == nil {
		numToName := map[int]string{
			1:  "HUP",
			2:  "INT",
			3:  "QUIT",
			15: "TERM",
			30: "USR1",
			31: "USR2",
		}
		if name, ok := numToName[n]; ok {
			return name, nameToSig[name], nil
		}
	}

	return "", 0, fmt.Errorf("invalid signal specification: %s", spec)
}

// runPendingTraps runs any pending signal trap handlers.
func (s *shellState) runPendingTraps() {
	s.runPendingTrapsWithIO(os.Stdin, os.Stdout, os.Stderr)
}

// runPendingTrapsWithIO runs pending signal traps with the given I/O.
func (s *shellState) runPendingTrapsWithIO(stdin, stdout, stderr *os.File) {
	if s.trapRunning {
		return
	}
	// Drain pending signals under the lock, then run handlers outside it.
	s.pendingMu.Lock()
	var pending []string
	for name := range s.pendingSignals {
		pending = append(pending, name)
		delete(s.pendingSignals, name)
	}
	s.pendingMu.Unlock()

	for _, name := range pending {
		s.runTrapWithIO(name, stdin, stdout, stderr)
	}
}

// runTrap runs a named trap handler if one is registered.
func (s *shellState) runTrap(name string) {
	s.runTrapWithIO(name, os.Stdin, os.Stdout, os.Stderr)
}

// runTrapWithIO runs a named trap handler with the given I/O.
func (s *shellState) runTrapWithIO(name string, stdin, stdout, stderr *os.File) {
	s.trapsMu.RLock()
	cmd, ok := s.traps[name]
	s.trapsMu.RUnlock()
	if !ok {
		return
	}
	// Empty command = ignore the signal.
	if cmd == "" {
		return
	}
	s.trapRunning = true
	tokens, err := lexer.Lex(cmd)
	if err == nil {
		tokens = expandAliases(s, tokens)
		list, perr := parser.Parse(tokens)
		if perr == nil {
			execList(s, list, stdin, stdout, stderr)
		}
	}
	s.trapRunning = false
}

// --- Main loop ---

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("gosh %s\n", version)
		return
	}

	state := newShellState()

	// gosh -c 'command' [arg0 [args...]]
	if len(os.Args) >= 2 && os.Args[1] == "-c" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "gosh: -c: option requires an argument")
			os.Exit(2)
		}
		cmdStr := os.Args[2]
		if len(os.Args) > 3 {
			state.vars["0"] = os.Args[3]
			state.positionalParams = os.Args[4:]
		}
		runLine(state, cmdStr)
		state.runTrap("EXIT")
		os.Exit(state.lastStatus)
	}

	// If a script file is given as an argument, run it.
	if len(os.Args) >= 2 {
		status := runScript(state, os.Args[1])
		state.runTrap("EXIT")
		os.Exit(status)
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

	state.runTrap("EXIT")
	os.Exit(state.lastStatus)
}

// runScript executes a script file and returns the exit status.
func runScript(state *shellState, path string) int {
	return runScriptWithIO(state, path, os.Stdin, os.Stdout, os.Stderr)
}

// runScriptWithIO executes a script file with the given I/O.
func runScriptWithIO(state *shellState, path string, stdin, stdout, stderr *os.File) int {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: %v\n", err)
		return 127
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, bufio.MaxScanTokenSize), 1024*1024)
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
			fmt.Fprintf(stderr, "gosh: %v\n", err)
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
				fmt.Fprintf(stderr, "gosh: %v\n", hdErr)
				continue
			}
		}

		if runTokensWithIO(state, tokens, stdin, stdout, stderr) {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "gosh: %s: %v\n", path, err)
		if state.lastStatus == 0 {
			state.lastStatus = 1
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
	scanner.Buffer(make([]byte, bufio.MaxScanTokenSize), 1024*1024)
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

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "gosh: read error: %v\n", err)
	}
}

// expandAliases performs alias substitution on command-position words
// in the token stream. A word is in command position if it is the
// first word or follows |, ;, &&, ||, or &. When an alias value ends
// with a space, the next word is also checked for alias expansion.
// A set of already-expanded names prevents infinite recursion.
func expandAliases(state *shellState, tokens []lexer.Token) []lexer.Token {
	if len(state.aliases) == 0 {
		return tokens
	}

	var result []lexer.Token
	cmdPos := true // first token is in command position

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		if cmdPos && tok.Type == lexer.TOKEN_WORD {
			expanded := expandOneAlias(state, tok, nil)
			if expanded != nil {
				// Check if alias value ends with space — if so, the
				// next word should also be checked for aliases. We
				// flag this by keeping cmdPos true.
				val := state.aliases[tok.Val]
				trailingSpace := len(val) > 0 && val[len(val)-1] == ' '

				result = append(result, expanded...)

				// Determine cmdPos for the next token.
				if trailingSpace {
					cmdPos = true
				} else {
					cmdPos = false
				}
				continue
			}
		}

		result = append(result, tok)

		// Update cmdPos based on the token we just added.
		switch tok.Type {
		case lexer.TOKEN_PIPE, lexer.TOKEN_SEMI, lexer.TOKEN_AND,
			lexer.TOKEN_OR, lexer.TOKEN_AMP:
			cmdPos = true
		default:
			cmdPos = false
		}
	}

	return result
}

// expandOneAlias expands a single alias token, recursively expanding
// any aliases in the replacement text. The seen set prevents infinite
// recursion from circular aliases.
func expandOneAlias(state *shellState, tok lexer.Token, seen map[string]bool) []lexer.Token {
	name := tok.Val
	val, ok := state.aliases[name]
	if !ok || seen[name] {
		return nil
	}

	replacement, err := lexer.Lex(val)
	if err != nil {
		return nil
	}

	// Remove the trailing EOF token from the re-lexed replacement.
	if len(replacement) > 0 && replacement[len(replacement)-1].Type == lexer.TOKEN_EOF {
		replacement = replacement[:len(replacement)-1]
	}

	if len(replacement) == 0 {
		return nil
	}

	// Recursively expand aliases in the first word of the replacement.
	if replacement[0].Type == lexer.TOKEN_WORD {
		if seen == nil {
			seen = make(map[string]bool)
		}
		seen[name] = true
		if expanded := expandOneAlias(state, replacement[0], seen); expanded != nil {
			replacement = append(expanded, replacement[1:]...)
		}
	}

	return replacement
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
	return runLineWithIO(state, line, os.Stdin, os.Stdout, os.Stderr)
}

// runLineWithIO lexes, parses, and executes a line with the given I/O.
func runLineWithIO(state *shellState, line string, stdin, stdout, stderr *os.File) bool {
	tokens, err := lexer.Lex(line)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: %v\n", err)
		return false
	}
	return runTokensWithIO(state, tokens, stdin, stdout, stderr)
}

// runTokens parses and executes a pre-lexed token stream.
// Returns true if the shell should exit.
func runTokens(state *shellState, tokens []lexer.Token) bool {
	return runTokensWithIO(state, tokens, os.Stdin, os.Stdout, os.Stderr)
}

// runTokensWithIO parses and executes a pre-lexed token stream with the given I/O.
func runTokensWithIO(state *shellState, tokens []lexer.Token, stdin, stdout, stderr *os.File) bool {
	if len(tokens) == 1 && tokens[0].Type == lexer.TOKEN_EOF {
		return false
	}

	// Expand aliases before parsing.
	tokens = expandAliases(state, tokens)

	if state.debugTokens {
		for _, tok := range tokens {
			fmt.Fprintf(stderr, "  %s\n", tok)
		}
	}

	list, err := parser.Parse(tokens)
	if err != nil {
		fmt.Fprintf(stderr, "gosh: %v\n", err)
		return false
	}

	if state.debugAST {
		fmt.Fprintf(stderr, "  %s\n", list)
	}

	// execList handles per-entry expansion (lazy), so no
	// expander.Expand call here. debugExpanded is also in execList.
	execList(state, list, stdin, stdout, stderr)

	return state.exitFlag
}
