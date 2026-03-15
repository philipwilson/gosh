package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryAdd(t *testing.T) {
	h := &History{}
	h.Add("ls")
	h.Add("pwd")
	h.Add("echo hello")

	if h.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", h.Len())
	}
	if h.Get(0) != "ls" {
		t.Errorf("expected ls, got %q", h.Get(0))
	}
	if h.Get(2) != "echo hello" {
		t.Errorf("expected echo hello, got %q", h.Get(2))
	}
}

func TestHistorySkipDuplicates(t *testing.T) {
	h := &History{}
	h.Add("ls")
	h.Add("ls")
	h.Add("ls")

	if h.Len() != 1 {
		t.Fatalf("expected 1 entry (dupes skipped), got %d", h.Len())
	}
}

func TestHistorySkipEmpty(t *testing.T) {
	h := &History{}
	h.Add("")
	h.Add("   ")

	if h.Len() != 0 {
		t.Fatalf("expected 0 entries, got %d", h.Len())
	}
}

func TestHistoryMaxEntries(t *testing.T) {
	h := &History{}
	for i := 0; i < maxHistory+100; i++ {
		h.Add(fmt.Sprintf("cmd %d", i))
	}

	if h.Len() != maxHistory {
		t.Fatalf("expected %d entries, got %d", maxHistory, h.Len())
	}
	// Oldest entries should have been trimmed.
	if h.Get(0) != "cmd 100" {
		t.Errorf("expected cmd 100, got %q", h.Get(0))
	}
}

func TestHistorySaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_history")

	// Create and save.
	h1 := &History{path: path}
	h1.Add("echo one")
	h1.Add("echo two")
	h1.Save()

	// Load into new instance.
	h2 := NewHistory(path)
	if h2.Len() != 2 {
		t.Fatalf("expected 2 entries after load, got %d", h2.Len())
	}
	if h2.Get(0) != "echo one" {
		t.Errorf("expected echo one, got %q", h2.Get(0))
	}
	if h2.Get(1) != "echo two" {
		t.Errorf("expected echo two, got %q", h2.Get(1))
	}
}

func TestHistoryLoadMissing(t *testing.T) {
	h := NewHistory("/nonexistent/path/history")
	if h.Len() != 0 {
		t.Fatalf("expected 0 entries for missing file, got %d", h.Len())
	}
}

func TestHistoryGetOutOfBounds(t *testing.T) {
	h := &History{}
	h.Add("hello")

	if h.Get(-1) != "" {
		t.Errorf("expected empty for negative index")
	}
	if h.Get(5) != "" {
		t.Errorf("expected empty for out-of-bounds index")
	}
}

func TestHistoryEntries(t *testing.T) {
	h := &History{}
	h.Add("a")
	h.Add("b")

	entries := h.Entries()
	entries[0] = "modified"

	// Entries should return a copy.
	if h.Get(0) != "a" {
		t.Errorf("Entries() should return a copy, but original was modified")
	}
}

func TestHistoryFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_history")

	h := &History{path: path}
	h.Add("secret command")
	h.Save()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected permissions 0600, got %o", info.Mode().Perm())
	}
}
