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

// TestNewRefusesStdoutWhenTheStreamsDiffer keeps the guard that matters: where the
// process can tell its streams apart, a caller handing stdout to New is making a
// mistake and the constructor must say so.
func TestNewRefusesStdoutWhenTheStreamsDiffer(t *testing.T) {
	originalStdout, originalStderr := os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = originalStdout, originalStderr })

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("create the stand-in stdout: %v", err)
	}
	errOut, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("create the stand-in stderr: %v", err)
	}
	t.Cleanup(func() { _ = out.Close(); _ = errOut.Close() })

	os.Stdout, os.Stderr = out, errOut

	if _, err := mcplog.New(os.Stdout, mcplog.Config{}); !errors.Is(err, mcplog.ErrStdoutReserved) {
		t.Fatalf("New(os.Stdout) error = %v, want ErrStdoutReserved", err)
	}
	if _, err := mcplog.New(os.Stderr, mcplog.Config{}); err != nil {
		t.Fatalf("New(os.Stderr) error = %v, want success", err)
	}
}

// TestNewAcceptsStderrWhenItIsStdout covers the ambiguous case: with one file
// behind both names, no choice of writer can satisfy a stdout refusal, so the
// constructor must not pretend otherwise.
func TestNewAcceptsStderrWhenItIsStdout(t *testing.T) {
	originalStdout, originalStderr := os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = originalStdout, originalStderr })

	shared := originalStdout
	os.Stdout, os.Stderr = shared, shared

	if _, err := mcplog.New(os.Stderr, mcplog.Config{}); err != nil {
		t.Fatalf("New(os.Stderr) error = %v, want success when the streams are one file", err)
	}
}
