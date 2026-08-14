//go:build !linux && !darwin && !windows

package cmd

import (
	"fmt"
	"os"
	"runtime"
)

// openTerminal reports that this platform has no supported terminal path.
//
// The flow is refused rather than degraded: a fallback that read a password with
// echo left on would print it to the screen and into the scrollback buffer, which is
// worse than not offering the flow at all. The browser flow remains available.
func openTerminal() (*os.File, error) {
	return nil, fmt.Errorf("%w: no terminal credential flow exists for %s",
		ErrNoTerminal, runtime.GOOS)
}

// readWithoutEcho is unreachable on this platform, because openTerminal refuses
// first. It exists so the package builds everywhere.
func readWithoutEcho(_ *os.File, _ int) (string, error) {
	return "", fmt.Errorf("%w: no terminal credential flow exists for %s",
		ErrNoTerminal, runtime.GOOS)
}
