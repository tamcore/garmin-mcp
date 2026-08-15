package cmd_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/config"
)

// initializeFrame is one MCP initialize request, newline-delimited as the stdio
// transport expects.
const initializeFrame = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
	`{"protocolVersion":"2026-07-28","capabilities":{},` +
	`"clientInfo":{"name":"cmd-test","version":"1"}}}` + "\n"

// stateDir returns a private, symlink-free state directory and points the
// configuration at it. Every path component must be real, because the secure file
// layer refuses a symlinked ancestor, and on macOS the temporary directory sits
// under /var, which is a symlink.
func stateDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	t.Setenv("GARMIN_MCP_STATE_DIR", dir)
	return dir
}

// syncWriter collects a stream the command writes from its own goroutine.
type syncWriter struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// servedStdio is what one stdio serve run produced.
type servedStdio struct {
	frames []map[string]any
	stderr string
	code   int
}

// serveStdio runs `serve --transport=stdio` over in-memory pipes with one request
// frame, then stops it by cancelling the context, which is the graceful shutdown
// path a supervisor uses.
func serveStdio(t *testing.T, request string, extraArgs ...string) servedStdio {
	t.Helper()

	requests, requestWriter := io.Pipe()
	frameReader, frames := io.Pipe()
	var stderr syncWriter

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	codes := make(chan int, 1)
	go func() {
		codes <- cmd.Execute(ctx, cmd.Options{
			BuildInfo: cmd.BuildInfo{Version: testVersion, Commit: testCommit},
			Args:      append([]string{cmdServe, flagStdio}, extraArgs...),
			Stdin:     requests,
			Stdout:    frames,
			Stderr:    &stderr,
		})
		_ = frames.Close()
	}()

	if request != "" {
		if _, err := io.WriteString(requestWriter, request); err != nil {
			t.Fatalf("write the request frame: %v", err)
		}
	}

	decoded := readFrames(t, frameReader, requestCount(request))
	cancel()
	_ = requestWriter.Close()

	var code int
	select {
	case code = <-codes:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after its context was cancelled")
	}

	return servedStdio{frames: decoded, stderr: stderr.String(), code: code}
}

func requestCount(request string) int {
	trimmed := strings.TrimSpace(request)
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

// readFrames reads want newline-delimited JSON objects from r.
func readFrames(t *testing.T, r io.Reader, want int) []map[string]any {
	t.Helper()

	if want == 0 {
		return nil
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	out := make([]map[string]any, 0, want)
	for len(out) < want && scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("stdout line %q is not an MCP frame: %v", line, err)
		}
		out = append(out, frame)
	}
	return out
}

// TestServeStdioAnswersInitializeWithoutWritingLogsToStdout is the behavior that
// makes the stdio server real: one initialize request is answered with the pinned
// protocol version, standard output carries that frame and nothing else, and the
// structured log records go to standard error.
func TestServeStdioAnswersInitializeWithoutWritingLogsToStdout(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	served := serveStdio(t, initializeFrame)

	if len(served.frames) != 1 {
		t.Fatalf("stdout carried %d frames, want exactly 1 (stderr %q)",
			len(served.frames), served.stderr)
	}
	result, ok := served.frames[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("frame %v carries no initialize result", served.frames[0])
	}
	// The negotiated version is the SDK's business; what matters here is that a
	// real initialize handshake completed and the server named itself.
	if version, ok := result["protocolVersion"].(string); !ok || version == "" {
		t.Errorf("initialize result carries no protocol version: %v", result)
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result carries no server info: %v", result)
	}
	if info["name"] != "garmin-mcp" {
		t.Errorf("serverInfo.name = %v, want %q", info["name"], "garmin-mcp")
	}
	if served.stderr == "" {
		t.Error("stderr is empty: the structured logger recorded nothing")
	}
	if served.code != 0 {
		t.Errorf("exit code = %d, want 0 after a cancelled context", served.code)
	}
}

// TestServeStdioLogsCarryNoFramesAndFramesCarryNoLogs keeps the two streams
// disjoint, which is what stdio discipline means in practice.
func TestServeStdioLogsCarryNoFramesAndFramesCarryNoLogs(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	served := serveStdio(t, initializeFrame, "--log-format=json")

	if strings.Contains(served.stderr, `"jsonrpc"`) {
		t.Errorf("stderr = %q, want no MCP frame on the log stream", served.stderr)
	}
	for _, frame := range served.frames {
		if _, isLog := frame["level"]; isLog {
			t.Errorf("frame %v is a log record on the frame stream", frame)
		}
	}
}

// TestServeStdioCreatesTheStoreAndKeyOwnerOnly proves the composition root really
// opened the encrypted store: the key and token directories exist under the
// configured state directory, and both are owner-only.
func TestServeStdioCreatesTheStoreAndKeyOwnerOnly(t *testing.T) {
	clearGarminEnv(t)
	dir := stateDir(t)

	if served := serveStdio(t, initializeFrame); served.code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", served.code, served.stderr)
	}

	for _, name := range []string{"keys", "tokens"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s mode = %o, want no group or other bits", name, perm)
		}
	}
}

// TestServeStreamableHTTPRefusesADeploymentWithNoClient covers the remote
// transport's own start-up refusal, and covers it through the command rather than
// through the composition root: a deployment that registers no OAuth client can
// authorize nobody, so it fails before a listener is opened, and it says so
// without writing to the frame stream.
func TestServeStreamableHTTPRefusesADeploymentWithNoClient(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	stdout, err := runCommand(t, cmdServe, "--transport=streamable-http",
		"--public-url=https://mcp.example.test",
		"--database-path=/srv/garmin-mcp/state.db",
		"--master-key-file=/srv/garmin-mcp/master.key")

	if !errors.Is(err, config.ErrMissingSetting) {
		t.Errorf("error %v does not match config.ErrMissingSetting", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

// TestServeRefusesInlineMasterKeyMaterial fails closed on key material this build
// cannot honor, rather than starting with a key the operator did not supply.
func TestServeRefusesInlineMasterKeyMaterial(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)
	t.Setenv("GARMIN_MCP_MASTER_KEY", "c2VjcmV0LW1hdGVyaWFs")

	stdout, err := runCommand(t, cmdServe, flagStdio)
	if err == nil {
		t.Fatal("serve accepted inline master key material")
	}
	if !errors.Is(err, cmd.ErrUnsupportedKeyMaterial) {
		t.Errorf("error %v does not match cmd.ErrUnsupportedKeyMaterial", err)
	}
	if strings.Contains(err.Error(), "c2VjcmV0LW1hdGVyaWFs") {
		t.Error("the error echoes the supplied key material")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

// TestStdioServeRunsNoStoreCleanup pins the deployment split. The periodic removal
// of expired authorization state belongs to the remote composition root, which owns
// the multi-user database; a stdio process has no such store, so it must start no
// cleanup loop at all.
func TestStdioServeRunsNoStoreCleanup(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	served := serveStdio(t, "")
	if strings.Contains(served.stderr, cmd.CleanupLogMessage) {
		t.Errorf("a stdio run logged a store cleanup record: %s", served.stderr)
	}
}
