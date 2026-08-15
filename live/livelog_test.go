//go:build garminlive

package live

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// This file is the one place this suite writes a diagnostic of its own, and it exists
// because a live run's diagnostics are the one output that has seen real account data.
//
// AGENTS.md allows structured slog logging only and forbids logging bodies. A live suite
// has a sharper version of the same rule. A raw transport error is not a sentence: it is
// a *url.Error, and a *url.Error carries the request URL — which for every request this
// suite guards is a Garmin object path with an account identifier in it. Printing one
// with %v puts that identifier on a terminal and into whatever captured the run. Nothing
// in this package formats an error value directly any more; every one goes through
// safeError first.

// suiteLogger is the logger every diagnostic of this suite goes through.
//
// It writes to stderr, never to stdout: stdout belongs to the test binary's own report,
// and a run's diagnostics must not be confusable with a test result. It is built per call
// rather than held in a package-level variable, because package-level mutable state is
// forbidden and a handler over os.Stderr costs nothing to build.
func suiteLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// safeError renders an error as something that provably carries no account data.
//
// There are exactly three answers. A *client.APIError anywhere in the chain is rendered
// by the request layer's own redacting renderer, which prints operation, endpoint label,
// class and status and degrades an unrecognised cause to its Go type name — that is the
// most useful answer and it is sanitized by construction. A cancelled or expired context
// is named as itself. Everything else — a *url.Error above all — is reduced to the Go
// type of the deepest error in the chain, which is a type name and never a message, a
// URL or an identifier.
func safeError(err error) string {
	if err == nil {
		return "none"
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Error()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded.Error()
	}
	return fmt.Sprintf("%T", deepest(err))
}

// deepest returns the innermost error of a chain, so the type name safeError reports is
// the one that describes what actually failed rather than the fmt wrapper around it.
func deepest(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}
