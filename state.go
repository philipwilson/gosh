package main

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gosh/editor"
	"gosh/expander"
	"gosh/lexer"
	"gosh/parser"
)

// version is set at build time via -ldflags "-X main.version=...".
// Defaults to "dev" for plain `go build` without flags.
var version = "dev"

// Variable attribute flags.
const (
	attrExport   uint8 = 1 << 0 // -x: export to children
	attrReadonly uint8 = 1 << 1 // -r: readonly
	attrInteger  uint8 = 1 << 2 // -i: integer
	attrAssoc    uint8 = 1 << 3 // -A: associative array
)

// shellState holds the shell's mutable state: variables, export
// set, last exit status, and terminal control info.
type shellState struct {
	vars             map[string]string              // shell variables
	arrays           map[string][]string          // indexed arrays
	assocArrays      map[string]map[string]string // associative arrays
	attrs            map[string]uint8             // variable attributes (export, readonly, integer, assoc)
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
		vars:        make(map[string]string),
		arrays:      make(map[string][]string),
		assocArrays: make(map[string]map[string]string),
		attrs:       make(map[string]uint8),
		aliases:     make(map[string]string),
		funcs:       make(map[string]*parser.List),
		termFd:      int(os.Stdin.Fd()),
	}

	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			s.vars[k] = v
			s.attrs[k] |= attrExport
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

	s.vars["OPTIND"] = "1"
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
					if m, ok := s.assocArrays[arrName]; ok {
						return strconv.Itoa(len(m))
					}
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

		// Key enumeration: !arr[@] or !arr[*] — return sorted keys.
		if strings.HasPrefix(name, "!") {
			rest := name[1:]
			if arrName, subscript, ok := parseArrayRef(rest); ok {
				if subscript == "@" || subscript == "*" {
					if m, ok := s.assocArrays[arrName]; ok {
						keys := make([]string, 0, len(m))
						for k := range m {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						return strings.Join(keys, " ")
					}
					// Indexed array key enumeration: return indices of non-empty elements.
					if arr, ok := s.arrays[arrName]; ok {
						var indices []string
						for i, v := range arr {
							if v != "" {
								indices = append(indices, strconv.Itoa(i))
							}
						}
						return strings.Join(indices, " ")
					}
				}
			}
		}

		// Array subscripts: arr[N], arr[@], arr[*]
		if arrName, subscript, ok := parseArrayRef(name); ok {
			// Check associative arrays first.
			if m, ok := s.assocArrays[arrName]; ok {
				switch subscript {
				case "@", "*":
					keys := make([]string, 0, len(m))
					for k := range m {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					vals := make([]string, 0, len(m))
					for _, k := range keys {
						vals = append(vals, m[k])
					}
					return strings.Join(vals, " ")
				default:
					return m[subscript]
				}
			}
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

		// Bare associative array name returns "" (bash behavior).
		if _, ok := s.assocArrays[name]; ok {
			return ""
		}

		// Bare indexed array name returns element 0.
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
		if _, exists := s.assocArrays[arrName]; exists {
			return true
		}
		_, exists := s.arrays[arrName]
		return exists
	}
	if _, ok := s.assocArrays[name]; ok {
		return true
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

	// ${!arrName[@]} — key enumeration for "${!arr[@]}".
	if strings.HasPrefix(name, "!") {
		rest := name[1:]
		if arrName, subscript, ok := parseArrayRef(rest); ok && subscript == "@" {
			if m, ok := s.assocArrays[arrName]; ok {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				return keys, true
			}
			if arr, ok := s.arrays[arrName]; ok {
				var indices []string
				for i, v := range arr {
					if v != "" {
						indices = append(indices, strconv.Itoa(i))
					}
				}
				return indices, true
			}
			return nil, true // empty
		}
	}

	// ${arr[@]}
	if arrName, subscript, ok := parseArrayRef(name); ok {
		if subscript == "@" {
			// Associative array.
			if m, ok := s.assocArrays[arrName]; ok {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				vals := make([]string, 0, len(m))
				for _, k := range keys {
					vals = append(vals, m[k])
				}
				return vals, true
			}
			// Indexed array.
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
	for k, a := range s.attrs {
		if a&attrExport == 0 {
			continue
		}
		// Arrays are not exported to children (bash behavior).
		if _, isArray := s.arrays[k]; isArray {
			continue
		}
		if _, isAssoc := s.assocArrays[k]; isAssoc {
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
	// Array element assignment: arr[N]=value or arr[key]=value
	if arrName, subscript, ok := parseArrayRef(name); ok {
		// Readonly check on the array name.
		if s.isReadonly(arrName) {
			fmt.Fprintf(os.Stderr, "gosh: %s: readonly variable\n", arrName)
			return
		}
		// Associative array: use subscript as string key.
		if s.isAssoc(arrName) {
			if s.assocArrays[arrName] == nil {
				s.assocArrays[arrName] = make(map[string]string)
			}
			s.assocArrays[arrName][subscript] = value
			return
		}
		idx, err := strconv.Atoi(subscript)
		if err != nil || idx < 0 {
			return
		}
		// Integer attribute: evaluate value as arithmetic.
		if s.isInteger(arrName) {
			value = s.evalIntegerValue(value)
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
	// Readonly check.
	if s.isReadonly(name) {
		fmt.Fprintf(os.Stderr, "gosh: %s: readonly variable\n", name)
		return
	}
	// Integer attribute: evaluate value as arithmetic.
	if s.isInteger(name) {
		value = s.evalIntegerValue(value)
	}
	s.vars[name] = value
}

// isReadonly returns true if the variable has the readonly attribute.
func (s *shellState) isReadonly(name string) bool {
	return s.attrs[name]&attrReadonly != 0
}

// isInteger returns true if the variable has the integer attribute.
func (s *shellState) isInteger(name string) bool {
	return s.attrs[name]&attrInteger != 0
}

// isAssoc returns true if the variable has the associative array attribute.
func (s *shellState) isAssoc(name string) bool {
	return s.attrs[name]&attrAssoc != 0
}

// evalIntegerValue evaluates a string as an arithmetic expression,
// returning the result as a string. On error, returns "0".
func (s *shellState) evalIntegerValue(value string) string {
	expr := expander.ExpandDollar(value, s.lookup)
	result, err := expander.EvalArith(expr, s.lookup, s.setVar)
	if err != nil {
		return "0"
	}
	return strconv.FormatInt(result, 10)
}

// setArray sets an array variable, replacing any existing value.
func (s *shellState) setArray(name string, vals []string) {
	if s.isReadonly(name) {
		fmt.Fprintf(os.Stderr, "gosh: %s: readonly variable\n", name)
		return
	}
	s.arrays[name] = vals
}

// appendArray appends values to an existing array (or creates one).
func (s *shellState) appendArray(name string, vals []string) {
	if s.isReadonly(name) {
		fmt.Fprintf(os.Stderr, "gosh: %s: readonly variable\n", name)
		return
	}
	s.arrays[name] = append(s.arrays[name], vals...)
}

// setAssocArray sets an associative array, replacing any existing value.
func (s *shellState) setAssocArray(name string, m map[string]string) {
	if s.isReadonly(name) {
		fmt.Fprintf(os.Stderr, "gosh: %s: readonly variable\n", name)
		return
	}
	delete(s.arrays, name)
	delete(s.vars, name)
	s.assocArrays[name] = m
}

func (s *shellState) exportVar(name string) {
	s.attrs[name] |= attrExport
}

// unsetVar removes a variable. Returns false if the variable is readonly.
func (s *shellState) unsetVar(name string) bool {
	// Array element: unset arr[N] or unset arr[key]
	if arrName, subscript, ok := parseArrayRef(name); ok {
		if s.isReadonly(arrName) {
			fmt.Fprintf(os.Stderr, "gosh: unset: %s: readonly variable\n", arrName)
			return false
		}
		// Associative array element.
		if m, ok := s.assocArrays[arrName]; ok {
			if subscript == "@" || subscript == "*" {
				delete(s.assocArrays, arrName)
				delete(s.attrs, arrName)
				return true
			}
			delete(m, subscript)
			return true
		}
		// Indexed array element.
		arr := s.arrays[arrName]
		if subscript == "@" || subscript == "*" {
			delete(s.arrays, arrName)
			return true
		}
		idx, err := strconv.Atoi(subscript)
		if err != nil || idx < 0 || idx >= len(arr) {
			return true
		}
		arr[idx] = ""
		s.arrays[arrName] = arr
		return true
	}
	if s.isReadonly(name) {
		fmt.Fprintf(os.Stderr, "gosh: unset: %s: readonly variable\n", name)
		return false
	}
	delete(s.vars, name)
	delete(s.arrays, name)
	delete(s.assocArrays, name)
	delete(s.attrs, name)
	return true
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
