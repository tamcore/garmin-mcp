//go:build linux || darwin

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// terminalDevice is the controlling terminal. It is opened by name rather than taken
// from standard input, so a redirected or piped standard input can never become a
// credential prompt.
const terminalDevice = "/dev/tty"

// openTerminal opens the controlling terminal, or reports that there is none.
func openTerminal() (*os.File, error) {
	tty, err := os.OpenFile(terminalDevice, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %s could not be opened", ErrNoTerminal, terminalDevice)
	}
	if _, err := unix.IoctlGetTermios(int(tty.Fd()), ioctlReadTermios); err != nil {
		_ = tty.Close()
		return nil, fmt.Errorf("%w: %s is not a terminal", ErrNoTerminal, terminalDevice)
	}
	return tty, nil
}

// readWithoutEcho reads one line from tty with echo disabled.
//
// The previous terminal state is restored whatever happens, including on a read
// error: a terminal left with echo off would silently swallow everything the user
// types next.
func readWithoutEcho(tty *os.File, maxLen int) (string, error) {
	fd := int(tty.Fd())

	previous, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return "", fmt.Errorf("%w: the terminal state could not be read", ErrNoTerminal)
	}

	quiet := *previous
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &quiet); err != nil {
		return "", fmt.Errorf("%w: the terminal echo could not be disabled", ErrNoTerminal)
	}
	defer func() { _ = unix.IoctlSetTermios(fd, ioctlWriteTermios, previous) }()

	reader := bufio.NewReaderSize(io.LimitReader(tty, int64(maxLen)+2), maxLen+2)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading from the terminal: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
