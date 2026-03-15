package main

import (
	"syscall"
	"unsafe"
)

func isatty(fd int) bool {
	var pgrp int
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCGPGRP),
		uintptr(unsafe.Pointer(&pgrp)),
	)
	return errno == 0
}

func tcsetpgrp(fd int, pgid int) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCSPGRP),
		uintptr(unsafe.Pointer(&pgid)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
