package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"gosh/lexer"
	"gosh/parser"
)

// processProcSubsts scans a command's args for process substitution parts
// (ProcSubstIn / ProcSubstOut). For each one it creates a FIFO (named pipe),
// spawns a goroutine to run the inner command, and replaces the arg with
// the FIFO path. Returns a cleanup function that waits for goroutines and
// removes the FIFOs.
func processProcSubsts(state *shellState, cmd *parser.SimpleCmd, stdin, stdout, stderr *os.File) func() {
	var wg sync.WaitGroup
	var fifoDir string

	for i := range cmd.Args {
		if len(cmd.Args[i]) != 1 {
			continue
		}
		part := cmd.Args[i][0]
		if part.Quote != lexer.ProcSubstIn && part.Quote != lexer.ProcSubstOut {
			continue
		}

		// Create a temp directory for FIFOs on first use.
		if fifoDir == "" {
			var err error
			fifoDir, err = os.MkdirTemp("", "gosh-procsub-*")
			if err != nil {
				fmt.Fprintf(stderr, "gosh: process substitution: %v\n", err)
				continue
			}
		}

		fifoPath := filepath.Join(fifoDir, fmt.Sprintf("fifo%d", i))
		if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
			fmt.Fprintf(stderr, "gosh: process substitution: mkfifo: %v\n", err)
			continue
		}

		innerCmd := part.Text
		isInput := part.Quote == lexer.ProcSubstIn

		cmd.Args[i] = lexer.Word{lexer.WordPart{Text: fifoPath, Quote: lexer.SingleQuoted}}

		wg.Add(1)
		if isInput {
			// <(cmd): goroutine runs cmd, writes stdout to FIFO
			go func(cmdText, path string) {
				defer wg.Done()
				f, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					return
				}
				defer f.Close()
				cloned := state.clone()
				cloned.substDepth++
				lexer.ExtglobEnabled = cloned.shoptExtglob
				tokens, err := lexer.Lex(cmdText)
				if err != nil {
					return
				}
				list, err := parser.Parse(tokens)
				if err != nil {
					return
				}
				execList(cloned, list, os.Stdin, f, os.Stderr)
			}(innerCmd, fifoPath)
		} else {
			// >(cmd): goroutine runs cmd, reads stdin from FIFO
			go func(cmdText, path string) {
				defer wg.Done()
				f, err := os.Open(path)
				if err != nil {
					return
				}
				defer f.Close()
				cloned := state.clone()
				cloned.substDepth++
				lexer.ExtglobEnabled = cloned.shoptExtglob
				tokens, err := lexer.Lex(cmdText)
				if err != nil {
					return
				}
				list, err := parser.Parse(tokens)
				if err != nil {
					return
				}
				execList(cloned, list, f, os.Stdout, os.Stderr)
			}(innerCmd, fifoPath)
		}
	}

	return func() {
		wg.Wait()
		if fifoDir != "" {
			os.RemoveAll(fifoDir)
		}
	}
}

