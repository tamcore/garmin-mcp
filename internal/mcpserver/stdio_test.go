package mcpserver_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// stdioHarness swaps os.Stdin and os.Stdout for pipes and restores them.
//
// The swap is what makes this a real proof rather than a proof about an injected
// writer: RunStdio is called with a zero StdioOptions, so it picks up the process
// streams exactly as it will in production.
type stdioHarness struct {
	stdinWriter  *os.File
	stdoutReader *os.File
}

func newStdioHarness(t *testing.T) *stdioHarness {
	t.Helper()

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the stdin pipe returned error: %v", err)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the stdout pipe returned error: %v", err)
	}

	originalStdin, originalStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinReader, stdoutWriter

	t.Cleanup(func() {
		os.Stdin, os.Stdout = originalStdin, originalStdout
		_ = stdinWriter.Close()
		_ = stdinReader.Close()
		_ = stdoutWriter.Close()
		_ = stdoutReader.Close()
	})

	return &stdioHarness{stdinWriter: stdinWriter, stdoutReader: stdoutReader}
}

// send writes one newline-delimited JSON-RPC frame to the server's stdin.
func (h *stdioHarness) send(t *testing.T, frame string) {
	t.Helper()

	if _, err := fmt.Fprintln(h.stdinWriter, frame); err != nil {
		t.Fatalf("writing a frame to stdin returned error: %v", err)
	}
}

// closeStdin ends the session, which makes RunStdio return.
func (h *stdioHarness) closeStdin(t *testing.T) {
	t.Helper()

	if err := h.stdinWriter.Close(); err != nil {
		t.Fatalf("closing stdin returned error: %v", err)
	}
}

// readFrames reads up to want complete lines from the server's stdout.
func (h *stdioHarness) readFrames(t *testing.T, want int) []string {
	t.Helper()

	type readResult struct {
		lines []string
		err   error
	}
	results := make(chan readResult, 1)

	go func() {
		scanner := bufio.NewScanner(h.stdoutReader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var lines []string
		for len(lines) < want && scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		err := scanner.Err()
		if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
			err = nil
		}
		results <- readResult{lines: lines, err: err}
	}()

	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("reading stdout returned error: %v", result.err)
		}
		return result.lines
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reading frames from stdout")
		return nil
	}
}

const (
	initializeFrame = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2026-07-28","capabilities":{},` +
		`"clientInfo":{"name":"stdio-test","version":"1"}}}`
	initializedFrame = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
)

func callFrame(id int, tool string) string {
	return fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{}}}`,
		id, tool)
}

// runStdio starts the server on the swapped process streams and returns a channel
// carrying its exit error.
func runStdio(server *mcpserver.Server, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- server.RunStdio(ctx, mcpserver.StdioOptions{}) }()
	return done
}

// stdioServer builds a server whose logger writes to an in-memory sink, plus a
// policy that refuses the write tier so the run also exercises a denial.
func stdioServer(t *testing.T) (*mcpserver.Server, *syncBuffer) {
	t.Helper()

	sink := &syncBuffer{}
	deps := mcpserver.Deps{
		Info:   mcpserver.Info{Name: testServerName, Version: testVersion},
		Logger: mustLogger(t, sink, mcplog.Config{}),
		Policy: mustPolicy(t, policy.Config{
			Mode:          policy.ModeLocal,
			ReadOnlyTools: []string{mcpserver.ServerInfoToolName, readTool},
			WriteTools:    []string{writeTool},
		}),
		Principals: mustResolver(t),
		Registrars: []mcpserver.ToolRegistrar{registrarFunc(func(r *mcpserver.Registry) error {
			if err := mcpserver.AddTool(r, spec(readTool, policy.TierReadOnly),
				(&probe{}).handler); err != nil {
				return err
			}
			return mcpserver.AddTool(r, spec(writeTool, policy.TierWrite), (&probe{}).handler)
		})},
	}
	return newTestServer(t, deps), sink
}

// This is the marquee assertion: over the real stdio path, every byte on stdout
// belongs to an MCP frame. Log records, refusals, and lifecycle events all go to
// stderr, which here is an in-memory sink.
func TestStdioStdoutCarriesMCPFramesOnly(t *testing.T) {
	harness := newStdioHarness(t)
	server, sink := stdioServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := runStdio(server, ctx)

	harness.send(t, initializeFrame)
	harness.send(t, initializedFrame)
	harness.send(t, callFrame(2, mcpserver.ServerInfoToolName))
	// A refused tool, so a denial is logged while frames are flowing.
	harness.send(t, callFrame(3, writeTool))

	// initialize, server_info, write_thing: three responses.
	frames := harness.readFrames(t, 3)
	harness.closeStdin(t)

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("RunStdio returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunStdio did not return after stdin closed")
	}

	if len(frames) != 3 {
		t.Fatalf("read %d frames, want 3: %q", len(frames), frames)
	}
	for i, line := range frames {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("stdout line %d is not JSON, so stdout is not frames only: %q", i, line)
		}
		if frame["jsonrpc"] != "2.0" {
			t.Fatalf("stdout line %d is JSON but not a JSON-RPC frame: %q", i, line)
		}
		if strings.Contains(line, `"outcome"`) {
			t.Fatalf("a log record reached stdout: %q", line)
		}
	}

	// The denial must have been recorded somewhere, and that somewhere is stderr.
	if logged := sink.String(); !strings.Contains(logged, "denied") {
		t.Fatalf("the refusal was not logged to the stderr sink: %q", logged)
	}
}

// The lifecycle record is written at construction, before any frame exists. It
// must not be on stdout either.
func TestStdioStartupLoggingDoesNotTouchStdout(t *testing.T) {
	harness := newStdioHarness(t)
	server, sink := stdioServer(t)

	if !strings.Contains(sink.String(), "startup") {
		t.Fatalf("startup was not logged to the stderr sink: %q", sink.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := runStdio(server, ctx)
	harness.send(t, initializeFrame)
	frames := harness.readFrames(t, 1)
	harness.closeStdin(t)
	<-done

	if len(frames) != 1 {
		t.Fatalf("read %d frames, want 1", len(frames))
	}
	if strings.Contains(frames[0], "startup") {
		t.Fatalf("the startup record reached stdout: %q", frames[0])
	}
}

// The explicit streams are honored, which is what lets a caller run the server
// over something other than the process streams.
func TestRunStdioHonorsExplicitStreams(t *testing.T) {
	t.Parallel()

	server := stdioServerInjected(t)

	in, inWriter := io.Pipe()
	var out syncBuffer

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- server.RunStdio(ctx, mcpserver.StdioOptions{In: in, Out: &out}) }()

	if _, err := fmt.Fprintln(inWriter, initializeFrame); err != nil {
		t.Fatalf("writing the initialize frame returned error: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for !strings.Contains(out.String(), `"result"`) {
		select {
		case <-deadline:
			t.Fatalf("no response arrived on the injected writer: %q", out.String())
		case err := <-done:
			t.Fatalf("RunStdio returned early: %v", err)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	if err := inWriter.Close(); err != nil {
		t.Fatalf("closing the injected reader returned error: %v", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("RunStdio returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunStdio did not return after the injected reader closed")
	}
}

// A cancelled context must end the run rather than leaving the transport open.
func TestRunStdioReturnsOnContextCancellation(t *testing.T) {
	t.Parallel()

	server := stdioServerInjected(t)

	in, inWriter := io.Pipe()
	t.Cleanup(func() { _ = inWriter.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.RunStdio(ctx, mcpserver.StdioOptions{In: in, Out: &syncBuffer{}}) }()

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunStdio did not return after its context was cancelled")
	}
}

// stdioServerInjected is stdioServer without the process-stream swap, for the
// tests that pass explicit streams and can therefore run in parallel.
func stdioServerInjected(t *testing.T) *mcpserver.Server {
	t.Helper()

	sink := &syncBuffer{}
	deps := mcpserver.Deps{
		Info:   mcpserver.Info{Name: testServerName, Version: testVersion},
		Logger: mustLogger(t, sink, mcplog.Config{}),
		Policy: mustPolicy(t, policy.Config{
			Mode:          policy.ModeLocal,
			ReadOnlyTools: []string{mcpserver.ServerInfoToolName},
		}),
		Principals: mustResolver(t),
	}
	return newTestServer(t, deps)
}
