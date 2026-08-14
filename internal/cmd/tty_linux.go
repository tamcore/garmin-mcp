package cmd

import "golang.org/x/sys/unix"

// The termios ioctl requests. Linux and the BSDs spell them differently, which is
// why they live in a per-platform file instead of a runtime switch: a wrong request
// number does not fail to compile, it fails at run time on a user's terminal.
const (
	ioctlReadTermios  = unix.TCGETS
	ioctlWriteTermios = unix.TCSETS
)
