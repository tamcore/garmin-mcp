package mcplog_test

import (
	"errors"
	"os"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/mcplog"
)

// TestNewStderrSucceedsWhenStderrIsStdout pins the behavior that `go test -json`
// exposed in CI. In that mode the toolchain points the test binary's os.Stderr at
// os.Stdout so stderr output is captured in the JSON event stream, which makes the
// two values identical. A constructor that refuses that case fails in exactly the
// environment CI runs in while proving nothing: the invariant is that records
// never reach the stream carrying MCP frames, and the frame stream is known only
// where the transport is built.
//
// This test is not parallel, because it swaps process-wide globals.
func TestNewStderrSucceedsWhenStderrIsStdout(t *testing.T) {
	originalStdout, originalStderr := os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = originalStdout, originalStderr })

	// Reproduce the -json arrangement exactly: one file, two names.
	shared := originalStdout
	os.Stdout, os.Stderr = shared, shared

	logger, err := mcplog.NewStderr(mcplog.Config{})
	if err != nil {
		t.Fatalf("NewStderr returned %v, want success even when os.Stderr is os.Stdout", err)
	}
	if logger.Sink() != os.Stderr {
		t.Errorf("Sink() = %v, want os.Stderr", logger.Sink())
	}
}

// TestNewStillRefusesAnExplicitStdout keeps the guard that matters: a caller that
// hands stdout to New is making a mistake, and the constructor must say so.
func TestNewStillRefusesAnExplicitStdout(t *testing.T) {
	originalStdout, originalStderr := os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = originalStdout, originalStderr })

	shared := originalStdout
	os.Stdout, os.Stderr = shared, shared

	if _, err := mcplog.New(os.Stdout, mcplog.Config{}); !errors.Is(err, mcplog.ErrStdoutReserved) {
		t.Fatalf("New(os.Stdout) error = %v, want ErrStdoutReserved", err)
	}
}
