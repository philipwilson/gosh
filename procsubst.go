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
				cloned := cloneShellState(state)
				cloned.substDepth++
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
				cloned := cloneShellState(state)
				cloned.substDepth++
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

// cloneShellState creates a deep copy of shell state for use in process
// substitution goroutines. The clone is isolated: no editor, no jobs,
// non-interactive.
func cloneShellState(state *shellState) *shellState {
	s := &shellState{
		vars:             make(map[string]string, len(state.vars)),
		arrays:           make(map[string][]string, len(state.arrays)),
		exported:         make(map[string]bool, len(state.exported)),
		aliases:          make(map[string]string, len(state.aliases)),
		funcs:            make(map[string]*parser.List, len(state.funcs)),
		traps:            make(map[string]string, len(state.traps)),
		pendingSignals:   make(map[string]bool),
		sigCh:            make(chan os.Signal, 8),
		positionalParams: state.positionalParams,
		lastStatus:       state.lastStatus,
		substDepth:       state.substDepth,
		optErrexit:       state.optErrexit,
		optNounset:       state.optNounset,
		optXtrace:        state.optXtrace,
		optPipefail:      state.optPipefail,
		termFd:           state.termFd,
		startTime:        state.startTime,
	}
	for k, v := range state.vars {
		s.vars[k] = v
	}
	for k, v := range state.arrays {
		cp := make([]string, len(v))
		copy(cp, v)
		s.arrays[k] = cp
	}
	for k, v := range state.exported {
		s.exported[k] = v
	}
	for k, v := range state.aliases {
		s.aliases[k] = v
	}
	for k, v := range state.funcs {
		s.funcs[k] = v
	}
	state.trapsMu.RLock()
	for k, v := range state.traps {
		s.traps[k] = v
	}
	state.trapsMu.RUnlock()
	return s
}
