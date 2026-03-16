// Package editor provides a line editor with history for interactive
// shell use. It puts the terminal in raw mode during editing and
// restores it before returning, so child processes see a normal
// terminal.
//
// Supported keys:
//
//	Enter        — accept line
//	Ctrl-D       — EOF (on empty line) or delete char under cursor
//	Ctrl-C       — cancel current line
//	Ctrl-A/Home  — move to start of line
//	Ctrl-E/End   — move to end of line
//	Ctrl-B/Left  — move left
//	Ctrl-F/Right — move right
//	Ctrl-K       — kill to end of line
//	Ctrl-U       — kill to start of line
//	Ctrl-W       — kill word backward
//	Ctrl-L       — clear screen
//	Backspace    — delete char before cursor
//	Delete       — delete char under cursor
//	Up/Down      — history navigation
package editor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// CompleteFunc takes the current line and cursor position, and returns
// a list of possible completions for the word under the cursor.
type CompleteFunc func(line string, pos int) []string

// Editor handles line editing with history support.
type Editor struct {
	History  *History
	Complete CompleteFunc
	WinchCh  <-chan struct{} // signaled when SIGWINCH is received
	Cols     int             // terminal width (updated on SIGWINCH)
	Rows     int             // terminal height (updated on SIGWINCH)
	fd       int
	in       *os.File
	orig     termios
}

// New creates an Editor that reads from the given file descriptor
// (typically os.Stdin.Fd()). It saves the original terminal state
// for later restoration.
func New(fd int, historyPath string) (*Editor, error) {
	orig, err := tcgetattr(fd)
	if err != nil {
		return nil, fmt.Errorf("tcgetattr: %w", err)
	}
	cols, rows := termSize(fd)
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return &Editor{
		History: NewHistory(historyPath),
		Cols:    cols,
		Rows:    rows,
		fd:      fd,
		in:      os.NewFile(uintptr(fd), "/dev/stdin"),
		orig:    orig,
	}, nil
}

// Close restores the terminal to its original state and saves history.
func (e *Editor) Close() {
	tcsetattr(e.fd, &e.orig)
	e.History.Save()
}

// QuerySize re-queries the terminal dimensions via TIOCGWINSZ and
// updates Cols/Rows. Returns true if either dimension changed.
func (e *Editor) QuerySize() bool {
	cols, rows := termSize(e.fd)
	if cols <= 0 || rows <= 0 {
		return false
	}
	changed := cols != e.Cols || rows != e.Rows
	e.Cols = cols
	e.Rows = rows
	return changed
}

// ReadLine displays the prompt and reads a line of input with editing
// support. Returns io.EOF when the user presses Ctrl-D on an empty line.
func (e *Editor) ReadLine(prompt string) (string, error) {
	if err := e.enterRaw(); err != nil {
		return "", err
	}
	defer e.exitRaw()

	line, err := e.edit(prompt)
	return line, err
}

func (e *Editor) enterRaw() error {
	raw := e.orig
	makeRaw(&raw)
	return tcsetattr(e.fd, &raw)
}

func (e *Editor) exitRaw() {
	tcsetattr(e.fd, &e.orig)
}

// edit is the core editing loop. It reads keys and updates the line
// buffer and cursor position.
func (e *Editor) edit(prompt string) (string, error) {
	buf := []rune{}
	pos := 0
	histPos := e.History.Len() // one past the end = "current input"
	savedLine := ""            // saves current input when browsing history
	lastWasTab := false        // for double-tab candidate listing

	refresh := func() {
		// Move to column 0, write prompt + buffer, clear to end of line,
		// then reposition cursor.
		fmt.Fprintf(os.Stderr, "\r%s%s\x1b[K", prompt, string(buf))
		// Move cursor back to the correct position.
		if back := len(buf) - pos; back > 0 {
			fmt.Fprintf(os.Stderr, "\x1b[%dD", back)
		}
	}

	refresh()

	for {
		key, err := e.readKey()
		if err != nil {
			return "", err
		}

		// After each key read (which may have been interrupted by
		// SIGWINCH), check for pending resize and redraw if needed.
		e.drainWinch()

		switch key {
		case keyEnter:
			fmt.Fprintf(os.Stderr, "\r\n")
			return string(buf), nil

		case keyCtrlD:
			if len(buf) == 0 {
				fmt.Fprintf(os.Stderr, "\r\n")
				return "", io.EOF
			}
			// Delete char under cursor.
			if pos < len(buf) {
				buf = append(buf[:pos], buf[pos+1:]...)
				refresh()
			}

		case keyCtrlC:
			fmt.Fprintf(os.Stderr, "^C\r\n")
			// Return empty string to cancel current line.
			return "", nil

		case keyBackspace:
			if pos > 0 {
				pos--
				buf = append(buf[:pos], buf[pos+1:]...)
				refresh()
			}

		case keyDelete:
			if pos < len(buf) {
				buf = append(buf[:pos], buf[pos+1:]...)
				refresh()
			}

		case keyCtrlA, keyHome:
			pos = 0
			refresh()

		case keyCtrlE, keyEnd:
			pos = len(buf)
			refresh()

		case keyCtrlB, keyLeft:
			if pos > 0 {
				pos--
				refresh()
			}

		case keyCtrlF, keyRight:
			if pos < len(buf) {
				pos++
				refresh()
			}

		case keyCtrlK:
			buf = buf[:pos]
			refresh()

		case keyCtrlU:
			buf = buf[pos:]
			pos = 0
			refresh()

		case keyCtrlW:
			if pos > 0 {
				// Delete backward to previous word boundary.
				i := pos - 1
				for i > 0 && buf[i-1] == ' ' {
					i--
				}
				for i > 0 && buf[i-1] != ' ' {
					i--
				}
				buf = append(buf[:i], buf[pos:]...)
				pos = i
				refresh()
			}

		case keyTab:
			e.handleTab(&buf, &pos, prompt, refresh, &lastWasTab)
			continue

		case keyCtrlL:
			fmt.Fprintf(os.Stderr, "\x1b[2J\x1b[H")
			refresh()

		case keyUp:
			if histPos > 0 {
				if histPos == e.History.Len() {
					savedLine = string(buf)
				}
				histPos--
				buf = []rune(e.History.Get(histPos))
				pos = len(buf)
				refresh()
			}

		case keyDown:
			if histPos < e.History.Len() {
				histPos++
				if histPos == e.History.Len() {
					buf = []rune(savedLine)
				} else {
					buf = []rune(e.History.Get(histPos))
				}
				pos = len(buf)
				refresh()
			}

		default:
			if key >= 0 && key < 32 {
				// Ignore unhandled control characters.
				continue
			}
			if key >= 0 {
				buf = append(buf[:pos], append([]rune{rune(key)}, buf[pos:]...)...)
				pos++
				refresh()
			}
		}
		lastWasTab = false
	}
}

// handleTab performs tab completion using the Complete callback.
func (e *Editor) handleTab(buf *[]rune, pos *int, prompt string, refresh func(), lastWasTab *bool) {
	if e.Complete == nil {
		return
	}

	line := string(*buf)
	candidates := e.Complete(line, *pos)
	if len(candidates) == 0 {
		fmt.Fprintf(os.Stderr, "\a") // bell
		return
	}

	// Find the word start (scan backward from pos).
	wordStart := *pos
	for wordStart > 0 && (*buf)[wordStart-1] != ' ' {
		wordStart--
	}
	prefix := string((*buf)[wordStart:*pos])

	if len(candidates) == 1 {
		// Single match: replace the partial word with the completion.
		completion := candidates[0]
		tail := append([]rune(completion), (*buf)[*pos:]...)
		*buf = append((*buf)[:wordStart], tail...)
		*pos = wordStart + len([]rune(completion))
		refresh()
		return
	}

	// Multiple matches: insert the longest common prefix.
	lcp := longestCommonPrefix(candidates)
	if len(lcp) > len(prefix) {
		tail := append([]rune(lcp), (*buf)[*pos:]...)
		*buf = append((*buf)[:wordStart], tail...)
		*pos = wordStart + len([]rune(lcp))
		refresh()
		*lastWasTab = false
		return
	}

	// No new characters to insert. On double-tab, display candidates.
	if !*lastWasTab {
		*lastWasTab = true
		fmt.Fprintf(os.Stderr, "\a") // bell on first tab with no progress
		return
	}

	// Double-tab: display candidate list.
	*lastWasTab = false
	e.displayCandidates(candidates, prompt, string(*buf), *pos, len(*buf))
	refresh()
}

// displayCandidates shows completion candidates below the current line,
// formatted in columns based on terminal width.
func (e *Editor) displayCandidates(candidates []string, prompt, buf string, pos, bufLen int) {
	width := e.Cols
	if width <= 0 {
		width = 80
	}

	// Find the longest candidate to determine column width.
	maxLen := 0
	for _, c := range candidates {
		if len(c) > maxLen {
			maxLen = len(c)
		}
	}
	colWidth := maxLen + 2 // 2 spaces between columns
	cols := width / colWidth
	if cols < 1 {
		cols = 1
	}

	fmt.Fprintf(os.Stderr, "\r\n")
	for i, c := range candidates {
		if i > 0 && i%cols == 0 {
			fmt.Fprintf(os.Stderr, "\r\n")
		}
		fmt.Fprintf(os.Stderr, "%-*s", colWidth, c)
	}
	fmt.Fprintf(os.Stderr, "\r\n")
}

// longestCommonPrefix returns the longest string that is a prefix of
// all the given strings.
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for i := 0; i < len(prefix); i++ {
			if i >= len(s) || s[i] != prefix[i] {
				prefix = prefix[:i]
				break
			}
		}
	}
	return prefix
}

// Key constants for special keys (negative values to avoid
// collision with Unicode codepoints).
const (
	keyEnter     = -1
	keyBackspace = -2
	keyDelete    = -3
	keyUp        = -4
	keyDown      = -5
	keyLeft      = -6
	keyRight     = -7
	keyHome      = -8
	keyEnd       = -9
	keyCtrlA     = -10
	keyCtrlB     = -11
	keyCtrlC     = -12
	keyCtrlD     = -13
	keyCtrlE     = -14
	keyCtrlF     = -15
	keyCtrlK     = -16
	keyCtrlU     = -17
	keyCtrlW     = -18
	keyCtrlL     = -19
	keyTab       = -20
)

// readByte reads a single byte from the terminal, retrying on EINTR.
// Signals (e.g., SIGCHLD from a finished background job, or SIGWINCH
// from a terminal resize) can interrupt the read syscall. On EINTR we
// drain the WinchCh to detect resize events and retry.
func (e *Editor) readByte() (byte, error) {
	var b [1]byte
	for {
		n, err := e.in.Read(b[:])
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				e.drainWinch()
				continue
			}
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		return b[0], nil
	}
}

// drainWinch non-blockingly drains the WinchCh and updates terminal
// dimensions if a SIGWINCH was received.
func (e *Editor) drainWinch() {
	if e.WinchCh == nil {
		return
	}
	for {
		select {
		case <-e.WinchCh:
			e.QuerySize()
		default:
			return
		}
	}
}

// readKey reads a single keypress, decoding escape sequences.
func (e *Editor) readKey() (int, error) {
	ch, err := e.readByte()
	if err != nil {
		return 0, err
	}

	switch {
	case ch == '\r' || ch == '\n':
		return keyEnter, nil
	case ch == 127 || ch == 8: // DEL or BS
		return keyBackspace, nil
	case ch == 1: // Ctrl-A
		return keyCtrlA, nil
	case ch == 2: // Ctrl-B
		return keyCtrlB, nil
	case ch == 3: // Ctrl-C
		return keyCtrlC, nil
	case ch == 4: // Ctrl-D
		return keyCtrlD, nil
	case ch == 5: // Ctrl-E
		return keyCtrlE, nil
	case ch == 6: // Ctrl-F
		return keyCtrlF, nil
	case ch == 11: // Ctrl-K
		return keyCtrlK, nil
	case ch == 12: // Ctrl-L
		return keyCtrlL, nil
	case ch == 21: // Ctrl-U
		return keyCtrlU, nil
	case ch == 9: // Tab
		return keyTab, nil
	case ch == 23: // Ctrl-W
		return keyCtrlW, nil
	case ch == 27: // ESC — start of escape sequence
		return e.readEscape()
	}

	return int(ch), nil
}

// readEscape reads the rest of an escape sequence after ESC.
func (e *Editor) readEscape() (int, error) {
	b1, err := e.readByte()
	if err != nil {
		return 27, nil // bare ESC
	}

	if b1 == '[' {
		b2, err := e.readByte()
		if err != nil {
			return 27, nil
		}

		switch b2 {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C':
			return keyRight, nil
		case 'D':
			return keyLeft, nil
		case 'H':
			return keyHome, nil
		case 'F':
			return keyEnd, nil
		case '3': // Delete key: ESC [ 3 ~
			e.readByte() // consume ~
			return keyDelete, nil
		}
	} else if b1 == 'O' {
		b2, err := e.readByte()
		if err != nil {
			return 27, nil
		}
		switch b2 {
		case 'H':
			return keyHome, nil
		case 'F':
			return keyEnd, nil
		}
	}

	return 27, nil // unrecognized escape sequence
}
