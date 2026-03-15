//go:build linux

package editor

import (
	"syscall"
	"unsafe"
)

// termios matches the Linux struct termios layout.
// On Linux, tcflag_t is unsigned int (4 bytes).
type termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   uint8
	Cc     [32]uint8
	Ispeed uint32
	Ospeed uint32
}

const (
	ioctlGetAttr = 0x5401 // TCGETS
	ioctlSetAttr = 0x5402 // TCSETS

	// c_lflag bits
	flagECHO   = 0x00000008
	flagICANON = 0x00000002
	flagISIG   = 0x00000001
	flagIEXTEN = 0x00008000

	// c_iflag bits
	flagICRNL = 0x00000100

	// c_oflag bits
	flagOPOST = 0x00000001

	// c_cc indices
	idxVMIN  = 6
	idxVTIME = 5
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

func makeRaw(t *termios) {
	t.Iflag &^= flagICRNL
	t.Oflag &^= flagOPOST
	t.Lflag &^= flagECHO | flagICANON | flagISIG | flagIEXTEN
	t.Cc[idxVMIN] = 1
	t.Cc[idxVTIME] = 0
}
