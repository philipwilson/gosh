package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gosh/editor"
	"gosh/lexer"
	"gosh/parser"
)

func main() {
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "--version":
			printVersion(os.Stdout)
			return
		case "-h", "--help":
			printUsage(os.Stdout)
			return
		}
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

	if state.ed != nil {
		state.ed.Close()
	}
	state.runTrap("EXIT")
	os.Exit(state.lastStatus)
}

// --- Script and line execution ---

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

// --- Interactive and non-interactive input loops ---

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

// --- Alias expansion ---

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

// --- Input completeness detection ---

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
