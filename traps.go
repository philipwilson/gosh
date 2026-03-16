package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"gosh/lexer"
	"gosh/parser"
)

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
