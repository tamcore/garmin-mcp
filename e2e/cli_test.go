//go:build e2e

package e2e

import (
	"bytes"
	"errors"
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

// run executes the binary and reports its streams and exit code separately, so a
// test can assert that stdout stayed clean while stderr carried the reason.
func run(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := exec.Command(bin, args...)

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
// diagnostic or an error must never appear there. serve is unimplemented in this
// milestone, which is exactly why the failure path is worth asserting now.
func TestStdioTransportKeepsStdoutClean(t *testing.T) {
	bin := buildBinary(t)

	stdout, stderr, code := run(t, bin, "serve", "--transport=stdio")
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero while serve is unimplemented")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if stderr == "" {
		t.Error("stderr is empty; the reason for the failure must be reported")
	}
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
