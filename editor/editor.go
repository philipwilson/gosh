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
	"fmt"
	"io"
	"os"
)

// Editor handles line editing with history support.
type Editor struct {
	History *History
	fd      int
	orig    termios
}

// New creates an Editor that reads from the given file descriptor
// (typically os.Stdin.Fd()). It saves the original terminal state
// for later restoration.
func New(fd int, historyPath string) (*Editor, error) {
	orig, err := tcgetattr(fd)
	if err != nil {
		return nil, fmt.Errorf("tcgetattr: %w", err)
	}
	return &Editor{
		History: NewHistory(historyPath),
		fd:      fd,
		orig:    orig,
	}, nil
}

// Close restores the terminal to its original state and saves history.
func (e *Editor) Close() {
	tcsetattr(e.fd, &e.orig)
	e.History.Save()
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
	}
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
)

// readKey reads a single keypress, decoding escape sequences.
func (e *Editor) readKey() (int, error) {
	var b [1]byte
	n, err := os.Stdin.Read(b[:])
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}

	ch := b[0]

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
	case ch == 23: // Ctrl-W
		return keyCtrlW, nil
	case ch == 27: // ESC — start of escape sequence
		return e.readEscape()
	}

	return int(ch), nil
}

// readEscape reads the rest of an escape sequence after ESC.
func (e *Editor) readEscape() (int, error) {
	var seq [2]byte

	n, err := os.Stdin.Read(seq[:1])
	if err != nil || n == 0 {
		return 27, err // bare ESC
	}

	if seq[0] == '[' {
		n, err = os.Stdin.Read(seq[1:])
		if err != nil || n == 0 {
			return 27, err
		}

		switch seq[1] {
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
			var tilde [1]byte
			os.Stdin.Read(tilde[:])
			return keyDelete, nil
		}
	} else if seq[0] == 'O' {
		n, err = os.Stdin.Read(seq[1:])
		if err != nil || n == 0 {
			return 27, err
		}
		switch seq[1] {
		case 'H':
			return keyHome, nil
		case 'F':
			return keyEnd, nil
		}
	}

	return 27, nil // unrecognized escape sequence
}
