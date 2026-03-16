package editor

import (
	"bufio"
	"os"
	"strings"
)

const maxHistory = 1000

// History stores command history and supports load/save to a file.
type History struct {
	entries []string
	path    string
}

// NewHistory creates a History that will persist to the given file path.
func NewHistory(path string) *History {
	h := &History{path: path}
	h.load()
	return h
}

// Add appends a line to the history, skipping consecutive duplicates
// and empty lines.
func (h *History) Add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == line {
		return
	}
	h.entries = append(h.entries, line)
	if len(h.entries) > maxHistory {
		h.entries = h.entries[len(h.entries)-maxHistory:]
	}
}

// Len returns the number of history entries.
func (h *History) Len() int {
	return len(h.entries)
}

// Get returns the entry at index i (0 = oldest).
func (h *History) Get(i int) string {
	if i < 0 || i >= len(h.entries) {
		return ""
	}
	return h.entries[i]
}

// Entries returns a copy of all history entries.
func (h *History) Entries() []string {
	cp := make([]string, len(h.entries))
	copy(cp, h.entries)
	return cp
}

func (h *History) load() {
	if h.path == "" {
		return
	}
	f, err := os.Open(h.path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			h.entries = append(h.entries, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return // silently discard partial history on error
	}
	if len(h.entries) > maxHistory {
		h.entries = h.entries[len(h.entries)-maxHistory:]
	}
}

// Save writes the history to disk.
func (h *History) Save() {
	if h.path == "" {
		return
	}
	f, err := os.OpenFile(h.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, line := range h.entries {
		w.WriteString(line)
		w.WriteByte('\n')
	}
	w.Flush()
}
