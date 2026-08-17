package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// The GOOS values the launcher and the platform checks compare against.
const (
	osDarwin = "darwin"
	osLinux  = "linux"
)

// xdgOpen is the freedesktop launcher every supported free-Unix desktop provides.
const xdgOpen = "xdg-open"

// browserLaunchTimeout bounds the helper that hands the URL to the desktop. The
// helper returns immediately in normal operation; the bound exists so a broken or
// missing desktop session cannot hold the login run open.
const browserLaunchTimeout = 10 * time.Second

// ErrNoBrowser reports that no browser could be launched. It is informational: the
// URL is printed before any launch is attempted, so an operator can always open the
// page manually.
var ErrNoBrowser = errors.New("no browser could be opened")

// launchBrowser asks the desktop to open endpoint.
//
// Only a URL this process just built for its own loopback listener is ever passed,
// and it is passed as one argument to a fixed helper, never through a shell, so
// there is no string a caller could inject into. A platform with no known helper is
// reported rather than guessed at.
func launchBrowser(ctx context.Context, endpoint string) error {
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") {
		return fmt.Errorf("%w: refusing to open a page that is not this run's loopback page",
			ErrNoBrowser)
	}

	name, args := browserCommand(runtime.GOOS, endpoint)
	if name == "" {
		return fmt.Errorf("%w: no launcher is known for %s", ErrNoBrowser, runtime.GOOS)
	}

	launchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), browserLaunchTimeout)
	defer cancel()

	if err := exec.CommandContext(launchCtx, name, args...).Run(); err != nil {
		return fmt.Errorf("%w: %s did not open the page: %w", ErrNoBrowser, name, err)
	}
	return nil
}

// browserCommand reports the launcher of goos and its arguments, or an empty name
// when none is known.
//
// The platform is a parameter rather than a read of runtime.GOOS, so every branch
// is reachable from a test on one machine. A launcher that is wrong on a platform
// nobody develops on is exactly the kind of defect that otherwise ships.
func browserCommand(goos, endpoint string) (name string, args []string) {
	switch goos {
	case osDarwin:
		return "open", []string{endpoint}
	case osLinux, "freebsd", "netbsd", "openbsd":
		return xdgOpen, []string{endpoint}
	default:
		return "", nil
	}
}
