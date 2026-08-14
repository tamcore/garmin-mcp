package mcplog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/mcplog"
)

// Synthetic labels shared by these tests.
const (
	categoryActivities = "activities"
	phaseStartup       = "startup"
	transportStdio     = "stdio"
)

// TestNewRefusesStdout is covered in stderr_test.go, where the process streams are
// made distinguishable first. It cannot be asserted here: under `go test -json`
// the toolchain points os.Stderr at os.Stdout, so os.Stdout is also the correct
// sink and the refusal would fire on every logger the suite builds.

func TestNewRefusesANilWriter(t *testing.T) {
	t.Parallel()

	_, err := mcplog.New(nil, mcplog.Config{})
	if !errors.Is(err, mcplog.ErrNoSink) {
		t.Fatalf("New(nil) error = %v, want ErrNoSink", err)
	}
}

func TestNewRefusesAnUnknownFormat(t *testing.T) {
	t.Parallel()

	_, err := mcplog.New(&bytes.Buffer{}, mcplog.Config{Format: mcplog.Format(99)})
	if !errors.Is(err, mcplog.ErrInvalidFormat) {
		t.Fatalf("New with an unknown format error = %v, want ErrInvalidFormat", err)
	}
}

func TestNewStderrWritesToStderrAndNotStdout(t *testing.T) {
	t.Parallel()

	logger, err := mcplog.NewStderr(mcplog.Config{})
	if err != nil {
		t.Fatalf("NewStderr returned error: %v", err)
	}
	if logger == nil {
		t.Fatal("NewStderr returned a nil logger")
	}
	if logger.Sink() != os.Stderr {
		t.Fatalf("Sink() = %v, want os.Stderr", logger.Sink())
	}
}

func TestZeroConfigDefaultsToJSONAtInfo(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, err := mcplog.New(&buf, mcplog.Config{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if logger.Enabled(slog.LevelDebug) {
		t.Error("the default level must not enable debug")
	}
	if !logger.Enabled(slog.LevelInfo) {
		t.Error("the default level must enable info")
	}

	logger.Lifecycle(mcplog.LifecycleEvent{Phase: phaseStartup, Transport: transportStdio})

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("the default format must be JSON, got %q: %v", buf.String(), err)
	}
}

func TestTextFormatIsAvailable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, err := mcplog.New(&buf, mcplog.Config{Format: mcplog.FormatText})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger.Lifecycle(mcplog.LifecycleEvent{Phase: phaseStartup, Transport: transportStdio})

	if !strings.Contains(buf.String(), "phase=startup") {
		t.Fatalf("text output %q does not carry phase=startup", buf.String())
	}
	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Fatal("the text format must not emit JSON")
	}
}

func TestLevelIsHonored(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, err := mcplog.New(&buf, mcplog.Config{Level: slog.LevelWarn})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger.Lifecycle(mcplog.LifecycleEvent{Phase: phaseStartup, Transport: transportStdio})
	if buf.Len() != 0 {
		t.Fatalf("an info-level lifecycle event was emitted at warn level: %q", buf.String())
	}
}

// A nil *Logger is a valid no-op, so a caller that has not built one yet does not
// have to branch. It must never panic and must never write anywhere.
func TestNilLoggerIsANoOp(t *testing.T) {
	t.Parallel()

	var logger *mcplog.Logger

	logger.Lifecycle(mcplog.LifecycleEvent{Phase: phaseStartup})
	logger.ToolCall(mcplog.ToolEvent{Category: categoryActivities, Outcome: mcplog.OutcomeOK})

	if logger.Enabled(slog.LevelError) {
		t.Error("a nil logger must report nothing enabled")
	}
	if logger.Sink() != nil {
		t.Error("a nil logger must report no sink")
	}
	if logger.DebugToolNames() {
		t.Error("a nil logger must not claim the debug tool-name policy")
	}
}

func TestLoggerIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, err := mcplog.New(&syncWriter{w: &buf}, mcplog.Config{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			logger.ToolCall(mcplog.ToolEvent{Category: categoryActivities, Outcome: mcplog.OutcomeOK})
		})
	}
	wg.Wait()

	lines := strings.Count(strings.TrimSpace(buf.String()), "\n") + 1
	if lines != 32 {
		t.Fatalf("emitted %d lines, want 32", lines)
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
