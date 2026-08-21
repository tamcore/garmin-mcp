//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary compiles the command under test and returns its path. The binary
// is built with the same ldflags shape the release uses, so the version output
// asserted below is the one users see.
func buildBinary(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "garmin-mcp")
	cmd := exec.Command("go", "build",
		"-ldflags", "-X main.version=v0.0.0-e2e -X main.commit=e2ecommit",
		"-o", bin, "../cmd/garmin-mcp")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v\n%s", err, stderr.String())
	}

	return bin
}

// blackholeProxy keeps the start-up catalog read off the public service: nothing
// listens there, so it fails at once and the binary falls back. A loopback
// destination is never proxied, so a deployment under test is unaffected.
const blackholeProxy = "http://127.0.0.1:1"

// offlineEnvWithProxy is the offline environment pointed at proxyURL. The bypass
// lists are cleared too: an inherited NO_PROXY of "*" would route around it.
func offlineEnvWithProxy(proxyURL string) []string {
	return []string{
		"HTTPS_PROXY=" + proxyURL,
		"https_proxy=" + proxyURL,
		"NO_PROXY=",
		"no_proxy=",
	}
}

// garminMCPEnvPrefix is what every ambient setting this suite must never
// inherit is named under.
const garminMCPEnvPrefix = "GARMIN_MCP_"

// filteredEnviron is the current process environment with every GARMIN_MCP_*
// entry removed.
//
// GARMIN_MCP_ environment variables outrank the configuration file
// (docs/configuration.md: flags, then GARMIN_MCP_ env, then the file), so an
// operator who exports one of them ambiently — GARMIN_MCP_DATABASE_PATH,
// GARMIN_MCP_STATE_DIR, GARMIN_MCP_MASTER_KEY_FILE — and then runs this suite
// would have every subprocess this file starts silently override its own
// throwaway config with that ambient setting, and mutate the operator's real
// state instead of the test's private directory.
func filteredEnviron() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, garminMCPEnvPrefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// offlineCommand builds a command for the binary with the offline environment
// already applied, so no started process can reach the public service.
func offlineCommand(bin string, args ...string) *exec.Cmd {
	return offlineCommandWithProxy(bin, "", args...)
}

// offlineCommandWithProxy is offlineCommand with the outbound proxy under the
// caller's control. An empty proxyURL keeps the default blackhole.
func offlineCommandWithProxy(bin, proxyURL string, args ...string) *exec.Cmd {
	if proxyURL == "" {
		proxyURL = blackholeProxy
	}
	command := exec.Command(bin, args...)
	command.Env = append(filteredEnviron(), offlineEnvWithProxy(proxyURL)...)
	return command
}

// run executes the binary and reports its streams and exit code separately, so a
// test can assert that stdout stayed clean while stderr carried the reason.
func run(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	return runWithEnv(t, bin, nil, args...)
}

// runWithEnv is run with extra environment entries, so a test can point the binary
// at a private state directory instead of the developer's own.
func runWithEnv(t *testing.T, bin string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := offlineCommand(bin, args...)
	cmd.Env = append(cmd.Env, env...)

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run %v: %v", args, err)
	}

	return out.String(), errOut.String(), code
}

func TestVersionReportsTheInjectedBuildInfo(t *testing.T) {
	bin := buildBinary(t)

	stdout, stderr, code := run(t, bin, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	for _, want := range []string{"v0.0.0-e2e", "e2ecommit"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q does not contain %q", stdout, want)
		}
	}
}

// TestStdioTransportKeepsStdoutClean guards the invariant that matters most for a
// stdio MCP server: stdout carries protocol frames and nothing else, so a
// diagnostic or a log record must never appear there.
//
// The binary runs with a closed standard input, which is a peer that disconnects
// immediately: the server starts, sees end of input, and shuts down. No request was
// sent, so no frame is due, and stdout must therefore be byte-empty while the
// lifecycle records went to stderr.
func TestStdioTransportKeepsStdoutClean(t *testing.T) {
	bin := buildBinary(t)

	stdout, stderr, code := runWithEnv(t, bin,
		[]string{"GARMIN_MCP_STATE_DIR=" + stateDir(t)},
		"serve", "--transport=stdio")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for a peer that disconnected (stderr %q)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty: it is reserved for MCP frames", stdout)
	}
	if stderr == "" {
		t.Error("stderr is empty; the lifecycle records must be reported there")
	}
	if strings.Contains(stderr, "jsonrpc") {
		t.Errorf("stderr = %q, want no MCP frame on the log stream", stderr)
	}
}

func TestStdioEnablementFlagsControlWriteAndDestructiveTools(t *testing.T) {
	bin := buildBinary(t)
	enabled := stdioListedTools(t, bin, "--enable-write-tools", "--enable-destructive-tools")
	disabled := stdioListedTools(t, bin)

	for label, names := range map[string]map[string]struct{}{"enabled": enabled, "disabled": disabled} {
		if _, ok := names["server_info"]; !ok {
			t.Fatalf("%s tools/list omitted server_info", label)
		}
	}
	for _, want := range []string{"upload_workout", "delete_workout"} {
		if _, ok := enabled[want]; !ok {
			t.Errorf("tools/list omitted %q with both stdio tier flags enabled", want)
		}
		if _, ok := disabled[want]; ok {
			t.Errorf("tools/list exposed %q with stdio tier flags disabled", want)
		}
	}
}

func stdioListedTools(t *testing.T, bin string, flags ...string) map[string]struct{} {
	t.Helper()
	cmd, stdin, scanner, stderr := startStdioSession(t, bin, flags...)

	writeFrame(t, stdin,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{"elicitation":{}},"clientInfo":{"name":"stdio-e2e","version":"1"}}}`)
	initialize := readFrame(t, scanner)
	if !strings.Contains(initialize, `"id":1`) {
		t.Fatalf("initialize response = %q, want id 1", initialize)
	}
	writeFrame(t, stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	writeFrame(t, stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	listResponse := readFrame(t, scanner)
	if err := stdin.Close(); err != nil {
		t.Errorf("close stdin: %v", err)
	}
	waitStdioSession(t, cmd, stderr)

	return listedTools(t, listResponse)
}

func waitStdioSession(t *testing.T, cmd *exec.Cmd, stderr *bytes.Buffer) {
	t.Helper()
	// SDK may report peer EOF as exit 1 after valid frames; no other nonzero exit is accepted.
	err := cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 ||
			!strings.Contains(stderr.String(), "server is closing: EOF") {
			t.Fatalf("stdio session: %v (stderr %q)", err, stderr.String())
		}
	}
}

func startStdioSession(
	t *testing.T, bin string, flags ...string,
) (*exec.Cmd, io.WriteCloser, *bufio.Scanner, *bytes.Buffer) {
	t.Helper()
	args := append([]string{"serve", "--transport=stdio"}, flags...)
	cmd := offlineCommand(bin, args...)
	cmd.Env = append(cmd.Env, "GARMIN_MCP_STATE_DIR="+stateDir(t))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("open stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("open stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stdio session: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return cmd, stdin, scanner, &stderr
}

func writeFrame(t *testing.T, stdin io.Writer, frame string) {
	t.Helper()
	if _, err := fmt.Fprintln(stdin, frame); err != nil {
		t.Fatalf("write stdio frame: %v", err)
	}
}

func readFrame(t *testing.T, scanner *bufio.Scanner) string {
	t.Helper()
	if !scanner.Scan() {
		t.Fatalf("read stdio frame: %v", scanner.Err())
	}
	return scanner.Text()
}

func listedTools(t *testing.T, responseFrame string) map[string]struct{} {
	t.Helper()

	var response struct {
		ID     int             `json:"id"`
		Error  json.RawMessage `json:"error"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(responseFrame), &response); err != nil {
		t.Fatalf("decode tools/list response %q: %v", responseFrame, err)
	}
	if response.ID != 2 {
		t.Fatalf("tools/list response id = %d, want 2", response.ID)
	}
	if len(response.Error) != 0 {
		t.Fatalf("tools/list returned JSON-RPC error: %s", response.Error)
	}
	names := make(map[string]struct{}, len(response.Result.Tools))
	for _, tool := range response.Result.Tools {
		names[tool.Name] = struct{}{}
	}
	return names
}

// stateDir returns a private, symlink-free state directory. Every path component
// must be real, because the secure file layer refuses a symlinked ancestor, and on
// macOS the temporary directory sits under /var, which is a symlink.
func stateDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	return dir
}

func TestUnknownCommandFailsWithoutTouchingStdout(t *testing.T) {
	bin := buildBinary(t)

	stdout, _, code := run(t, bin, "definitely-not-a-command")
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for an unknown command")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

// TestRemoteDeploymentIgnoresAnInheritedGarminMCPEnvironmentVariable is the
// mutant this test catches: a build that appended the offline environment
// without first stripping GARMIN_MCP_* from the inherited one would let an
// operator's own exported GARMIN_MCP_DATABASE_PATH — which outranks the
// deployment's config file under the documented precedence — silently redirect
// every subprocess this suite starts at the operator's real database instead of
// the test's private one. That is not merely a test hygiene problem: it is the
// suite mutating the operator's real state.
//
// The ambient variable is set in this test process with t.Setenv, which
// filteredEnviron must strip before the child ever sees it. Without the strip,
// GARMIN_MCP_DATABASE_PATH's higher precedence over the config file's
// database-path setting (docs/configuration.md) makes the remote deployment
// open and create the ambient path instead, which this test would then observe
// on disk.
func TestRemoteDeploymentIgnoresAnInheritedGarminMCPEnvironmentVariable(t *testing.T) {
	ambientDir := t.TempDir()
	ambientDB := filepath.Join(ambientDir, "ambient-should-never-be-touched.db")
	t.Setenv("GARMIN_MCP_DATABASE_PATH", ambientDB)

	startRemoteServer(t)

	if _, err := os.Stat(ambientDB); err == nil {
		t.Fatalf("the deployment created %s: an inherited GARMIN_MCP_DATABASE_PATH "+
			"leaked into the subprocess and overrode its own config file", ambientDB)
	}
}
