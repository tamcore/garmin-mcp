package mcplog

import "errors"

// Sentinel errors New returns. All three are start-up failures.
var (
	// ErrStdoutReserved reports that a caller tried to build a logger over
	// os.Stdout. Under the stdio transport stdout carries MCP frames and nothing
	// else, so a single log line there corrupts the protocol stream. The check is
	// unconditional rather than transport-dependent, because a logger built for
	// one transport must not become a hazard when it is reused for another.
	ErrStdoutReserved = errors.New("mcplog: stdout is reserved for MCP frames")

	// ErrNoSink reports a nil writer.
	ErrNoSink = errors.New("mcplog: no log sink")

	// ErrInvalidFormat reports an unrecognized Config.Format.
	ErrInvalidFormat = errors.New("mcplog: invalid log format")
)
