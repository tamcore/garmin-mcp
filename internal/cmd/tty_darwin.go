package cmd

import "golang.org/x/sys/unix"

// The termios ioctl requests on Darwin. See tty_linux.go for why these are
// per-platform constants rather than a runtime switch.
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
