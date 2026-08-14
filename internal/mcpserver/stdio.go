package mcpserver

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
)

// StdioOptions selects the streams the stdio transport runs over.
//
// Both fields default to the process streams, which is the production case. They
// are injectable so a test can drive the transport without touching process-global
// state, and so a future embedding caller can supply its own pipes.
type StdioOptions struct {
	// In is the frame source. Zero means os.Stdin.
	In io.Reader

	// Out is the frame sink. Zero means os.Stdout.
	//
	// Whatever this is, it carries MCP frames and nothing else. The logger cannot
	// be pointed at stdout — mcplog.New refuses it — so there is no configuration
	// in which a log record and a frame share this stream.
	Out io.Writer
}

// RunStdio serves MCP over the stdio transport until the peer disconnects or ctx is
// cancelled.
//
// Stdout is reserved exclusively for MCP frames. Two things enforce that rather than
// merely intending it: mcplog refuses os.Stdout as a sink, and nothing in this
// package writes to a stream other than the transport's own.
//
// The SDK's mcp.StdioTransport is not used, because at v1.7.0 it is a bare struct{}
// that reads os.Stdin and writes os.Stdout with no injection point. mcp.IOTransport
// is the same newline-delimited JSON conversation over supplied streams — the SDK's
// own StdioTransport.Connect is exactly an io conn over os.Stdin and os.Stdout — so
// defaulting IOTransport to the process streams is byte-for-byte the stdio
// transport, and it is testable.
func (s *Server) RunStdio(ctx context.Context, opts StdioOptions) error {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	s.deps.Logger.Lifecycle(mcplog.LifecycleEvent{
		Phase:           "serving",
		Transport:       "stdio",
		Mode:            s.deps.Policy.Mode().String(),
		ProtocolVersion: ProtocolVersion,
		ToolCount:       s.registry.Len(),
	})
	defer s.deps.Logger.Lifecycle(mcplog.LifecycleEvent{Phase: "shutdown", Transport: "stdio"})

	// The transport closes what it is given. The process streams must outlive the
	// run, and an injected stream belongs to its caller, so both are wrapped in
	// closers that do nothing.
	transport := &mcp.IOTransport{
		Reader: nopReadCloser{in},
		Writer: nopWriteCloser{out},
	}

	if err := s.mcpServer.Run(ctx, transport); err != nil {
		return fmt.Errorf("serving MCP over stdio: %w", err)
	}
	return nil
}

// nopReadCloser adapts an io.Reader, ignoring Close.
type nopReadCloser struct{ io.Reader }

func (nopReadCloser) Close() error { return nil }

// nopWriteCloser adapts an io.Writer, ignoring Close.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
