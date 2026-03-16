package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"gosh/lexer"
	"gosh/parser"
)

// nameToSig maps canonical signal names to their syscall.Signal values.
var nameToSig = map[string]syscall.Signal{
	"HUP":  syscall.SIGHUP,
	"INT":  syscall.SIGINT,
	"QUIT": syscall.SIGQUIT,
	"ABRT": syscall.SIGABRT,
	"KILL": syscall.SIGKILL,
	"PIPE": syscall.SIGPIPE,
	"ALRM": syscall.SIGALRM,
	"TERM": syscall.SIGTERM,
	"STOP": syscall.SIGSTOP,
	"TSTP": syscall.SIGTSTP,
	"CONT": syscall.SIGCONT,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
}

// numToName maps signal numbers to canonical names, built from nameToSig
// so it's platform-portable.
var numToName = func() map[int]string {
	m := make(map[int]string, len(nameToSig))
	for name, sig := range nameToSig {
		m[int(sig)] = name
	}
	return m
}()

// signalName maps an os.Signal to its canonical trap name.
func signalName(sig os.Signal) string {
	if s, ok := sig.(syscall.Signal); ok {
		if name, ok := numToName[int(s)]; ok {
			return name
		}
	}
	return ""
}

// parseSignalSpec normalizes a signal specification (e.g. "INT", "SIGINT",
// "int", "2") to a canonical name and syscall.Signal.
func parseSignalSpec(spec string) (string, syscall.Signal, error) {
	upper := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(spec), "SIG"))

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
		if name, ok := numToName[n]; ok {
			return name, nameToSig[name], nil
		}
	}

	return "", 0, fmt.Errorf("invalid signal specification: %s", spec)
}

// signalEntry holds a signal number and name for listing.
type signalEntry struct {
	Num  int
	Name string
}

// allSignals returns all known signals sorted by number.
func allSignals() []signalEntry {
	entries := make([]signalEntry, 0, len(nameToSig))
	for name, sig := range nameToSig {
		entries = append(entries, signalEntry{Num: int(sig), Name: name})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Num < entries[j].Num
	})
	return entries
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
