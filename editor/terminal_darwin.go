//go:build darwin

package editor

import (
	"syscall"
	"unsafe"
)

// termios matches the Darwin struct termios layout.
// On 64-bit macOS, tcflag_t and speed_t are unsigned long (8 bytes).
type termios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [20]uint8
	Ispeed uint64
	Ospeed uint64
}

const (
	ioctlGetAttr = 0x40487413 // TIOCGETA
	ioctlSetAttr = 0x80487414 // TIOCSETA

	// c_lflag bits
	flagECHO   = 0x00000008
	flagICANON = 0x00000100
	flagISIG   = 0x00000080
	flagIEXTEN = 0x00000400

	// c_iflag bits
	flagICRNL = 0x00000100

	// c_oflag bits
	flagOPOST = 0x00000001

	// c_cc indices
	idxVMIN  = 16
	idxVTIME = 17
)

func tcgetattr(fd int) (termios, error) {
	var t termios
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(ioctlGetAttr),
		uintptr(unsafe.Pointer(&t)),
	)
	if errno != 0 {
		return t, errno
	}
	return t, nil
}

func tcsetattr(fd int, t *termios) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(ioctlSetAttr),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// makeRaw modifies a termios to disable canonical mode, echo,
// signals, and extended processing. Sets VMIN=1, VTIME=0 so
// reads return one byte at a time.
// winsize matches the struct winsize used by TIOCGWINSZ.
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

const ioctlGetWinsz = 0x40087468 // TIOCGWINSZ

// termWidth returns the terminal width in columns, or -1 on error.
func termWidth(fd int) int {
	var ws winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(ioctlGetWinsz),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return -1
	}
	return int(ws.Col)
}

func makeRaw(t *termios) {
	t.Iflag &^= flagICRNL
	t.Oflag &^= flagOPOST
	t.Lflag &^= flagECHO | flagICANON | flagISIG | flagIEXTEN
	t.Cc[idxVMIN] = 1
	t.Cc[idxVTIME] = 0
}
