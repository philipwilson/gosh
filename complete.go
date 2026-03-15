package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// complete returns completions for the word at cursor position pos in line.
// Command-position words complete from builtins + PATH executables.
// Argument-position words complete from filenames.
func (s *shellState) complete(line string, pos int) []string {
	// Find the word being completed by scanning backward from pos.
	wordStart := pos
	for wordStart > 0 && line[wordStart-1] != ' ' {
		wordStart--
	}
	prefix := line[wordStart:pos]

	if isCommandPosition(line, wordStart) {
		return s.commandCompletions(prefix)
	}
	return filenameCompletions(prefix)
}

// isCommandPosition returns true if wordStart is at command position:
// first word on the line, or immediately after |, ;, &, &&, ||.
func isCommandPosition(line string, wordStart int) bool {
	// Scan backward from wordStart, skipping spaces.
	i := wordStart - 1
	for i >= 0 && line[i] == ' ' {
		i--
	}
	if i < 0 {
		return true // first word
	}
	ch := line[i]
	return ch == '|' || ch == ';' || ch == '&'
}

// commandCompletions returns sorted, deduplicated completions from
// builtins and PATH executables matching the given prefix.
func (s *shellState) commandCompletions(prefix string) []string {
	seen := make(map[string]bool)
	var results []string

	// Builtins.
	for name := range builtins {
		if strings.HasPrefix(name, prefix) && !seen[name] {
			seen[name] = true
			results = append(results, name+" ")
		}
	}

	// PATH executables.
	for _, match := range s.executablesOnPath(prefix) {
		if !seen[match] {
			seen[match] = true
			results = append(results, match+" ")
		}
	}

	sort.Strings(results)
	return results
}

// executablesOnPath scans PATH directories for executables matching prefix.
// First occurrence wins (matches real PATH semantics).
func (s *shellState) executablesOnPath(prefix string) []string {
	seen := make(map[string]bool)
	var results []string

	pathVal := s.vars["PATH"]
	if pathVal == "" {
		return nil
	}

	for _, dir := range strings.Split(pathVal, ":") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			if entry.IsDir() {
				continue
			}
			if seen[name] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Mode()&0111 != 0 {
				seen[name] = true
				results = append(results, name)
			}
		}
	}
	return results
}

// filenameCompletions returns filename matches for the given prefix.
// Directories get a trailing /, other files get a trailing space.
func filenameCompletions(prefix string) []string {
	// Use Glob to find matches.
	matches, err := filepath.Glob(prefix + "*")
	if err != nil || len(matches) == 0 {
		return nil
	}

	var results []string
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		if info.IsDir() {
			results = append(results, match+"/")
		} else {
			results = append(results, match+" ")
		}
	}
	return results
}
