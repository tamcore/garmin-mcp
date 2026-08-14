package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// consoleInput is the console's input device. Like /dev/tty on Unix it is opened by
// name rather than taken from standard input, so a redirected standard input cannot
// become a credential prompt.
const consoleInput = "CONIN$"

// openTerminal opens the console input device, or reports that there is none.
func openTerminal() (*os.File, error) {
	tty, err := os.OpenFile(consoleInput, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %s could not be opened", ErrNoTerminal, consoleInput)
	}

	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(tty.Fd()), &mode); err != nil {
		_ = tty.Close()
		return nil, fmt.Errorf("%w: %s is not a console", ErrNoTerminal, consoleInput)
	}
	return tty, nil
}

// readWithoutEcho reads one line from the console with echo disabled.
//
// The previous console mode is restored whatever happens, including on a read error:
// a console left with echo off would silently swallow everything typed next.
func readWithoutEcho(tty *os.File, maxLen int) (string, error) {
	handle := windows.Handle(tty.Fd())

	var previous uint32
	if err := windows.GetConsoleMode(handle, &previous); err != nil {
		return "", fmt.Errorf("%w: the console mode could not be read", ErrNoTerminal)
	}

	quiet := previous &^ windows.ENABLE_ECHO_INPUT
	quiet |= windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
	if err := windows.SetConsoleMode(handle, quiet); err != nil {
		return "", fmt.Errorf("%w: the console echo could not be disabled", ErrNoTerminal)
	}
	defer func() { _ = windows.SetConsoleMode(handle, previous) }()

	reader := bufio.NewReaderSize(io.LimitReader(tty, int64(maxLen)+2), maxLen+2)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading from the console: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
