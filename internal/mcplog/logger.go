// Package mcplog is the structured logging seam for the MCP server.
//
// It exists to make the redaction rules structural rather than advisory. The
// exported API accepts only two closed event types, ToolEvent and LifecycleEvent,
// and neither has a free-form attribute channel. A caller therefore cannot log a
// request or response body, a token, a cookie, a password, an MFA code, an email,
// a health metric, a coordinate, or a Garmin payload, because there is nowhere to
// put one.
//
// Two further rules are enforced in code:
//
//   - The sink is never stdout. New refuses os.Stdout outright, because under the
//     stdio transport stdout carries MCP frames and a single stray line corrupts
//     the stream.
//   - An exact tool name is emitted only under an explicit debug policy. A tool
//     name can itself disclose a sensitive domain — a women's-health tool
//     discloses one by being called at all — so the default record carries the
//     coarse category instead.
//
// The MCP `logging` protocol capability is deliberately unused: SEP-2577
// deprecates it as of protocol version 2026-07-28, and this package is the local
// replacement named by ADR 0002.
package mcplog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// A Format selects the record encoding.
type Format int

// The formats. The zero value is FormatJSON, so a zero Config is usable and
// machine-readable.
const (
	// FormatJSON emits one JSON object per record. This is the default.
	FormatJSON Format = iota

	// FormatText emits slog's key=value text encoding, for a human at a
	// terminal.
	FormatText
)

// Config configures a Logger. The zero value is valid: JSON at info level, with
// exact tool names withheld.
type Config struct {
	// Level is the minimum level emitted. The zero value is slog.LevelInfo.
	Level slog.Level

	// Format selects the encoding.
	Format Format

	// DebugToolNames turns on exact tool names. It is the explicit safe-debugging
	// policy the redaction rules require, and it must stay off in ordinary
	// operation.
	DebugToolNames bool
}

// A Logger emits the server's structured records.
//
// A nil *Logger is a valid no-op on every method, so a caller that has not built
// one does not have to branch. It is safe for concurrent use.
type Logger struct {
	base           *slog.Logger
	sink           io.Writer
	debugToolNames bool
	level          slog.Level
}

// New returns a Logger that writes to w.
//
// It returns ErrNoSink for a nil writer, ErrStdoutReserved for os.Stdout, and
// ErrInvalidFormat for an unrecognized format.
func New(w io.Writer, cfg Config) (*Logger, error) {
	if w == nil {
		return nil, fmt.Errorf("no writer given: %w", ErrNoSink)
	}
	if w == io.Writer(os.Stdout) {
		return nil, fmt.Errorf("refusing to log to stdout: %w", ErrStdoutReserved)
	}

	opts := &slog.HandlerOptions{Level: cfg.Level}
	var handler slog.Handler
	switch cfg.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(w, opts)
	case FormatText:
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("format %d: %w", int(cfg.Format), ErrInvalidFormat)
	}

	return &Logger{
		base:           slog.New(handler),
		sink:           w,
		debugToolNames: cfg.DebugToolNames,
		level:          cfg.Level,
	}, nil
}

// NewStderr returns a Logger that writes to os.Stderr, which is the only sink the
// stdio transport permits.
func NewStderr(cfg Config) (*Logger, error) { return New(os.Stderr, cfg) }

// Sink reports the writer records go to, or nil for a nil Logger. It exists so a
// caller can assert the sink is not stdout.
func (l *Logger) Sink() io.Writer {
	if l == nil {
		return nil
	}
	return l.sink
}

// DebugToolNames reports whether exact tool names are being emitted.
func (l *Logger) DebugToolNames() bool {
	if l == nil {
		return false
	}
	return l.debugToolNames
}

// Enabled reports whether a record at level would be emitted.
func (l *Logger) Enabled(level slog.Level) bool {
	if l == nil {
		return false
	}
	return level >= l.level
}

// ToolCall records one completed tool call. The event's outcome selects the level.
func (l *Logger) ToolCall(event ToolEvent) {
	if l == nil {
		return
	}
	l.base.LogAttrs(context.Background(), event.Outcome.level(), "tool call",
		event.attrs(l.debugToolNames)...)
}

// Lifecycle records a server transition at info level.
func (l *Logger) Lifecycle(event LifecycleEvent) {
	if l == nil {
		return
	}
	l.base.LogAttrs(context.Background(), slog.LevelInfo, "server lifecycle", event.attrs()...)
}
