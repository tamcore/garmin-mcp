package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/config"
)

// Command names and repeated flags used across the command tests.
const (
	cmdServe   = "serve"
	cmdAuth    = "auth"
	cmdDoctor  = "doctor"
	cmdTools   = "tools"
	cmdList    = "list"
	cmdMigrate = "migrate"
	cmdVersion = "version"

	flagStdio = "--transport=stdio"

	// The synthetic build identity every command test injects.
	testVersion = "v0.0.0-test"
	testCommit  = "testcommit"
)

// clearGarminEnv removes every GARMIN_MCP_ variable for the duration of the test,
// so a developer's or a CI runner's environment cannot change what a command
// resolves. An empty value counts as unset.
func clearGarminEnv(t *testing.T) {
	t.Helper()

	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(name, "GARMIN_MCP_") {
			t.Setenv(name, "")
		}
	}
}

// runCommand executes the command tree with args and reports what reached the
// result stream together with the error. Diagnostics are asserted separately, by
// the tests that go through Execute.
func runCommand(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer
	root := cmd.NewRootCommand(cmd.Options{
		BuildInfo: cmd.BuildInfo{Version: testVersion, Commit: testCommit},
		Args:      args,
		Stdout:    &out,
		Stderr:    &errOut,
	})
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestServeParsesAndValidatesBeforeReportingTheGap(t *testing.T) {
	clearGarminEnv(t)

	tests := []struct {
		name     string
		args     []string
		sentinel error
	}{
		{
			name:     "stdio validates and reports the missing server",
			args:     []string{cmdServe, flagStdio},
			sentinel: cmd.ErrNotImplemented,
		},
		{
			name: "streamable http validates and reports the missing server",
			args: []string{
				cmdServe, "--transport=streamable-http",
				"--public-url=http://127.0.0.1:8180",
				"--database-path=/var/lib/garmin-mcp/state.db",
				"--master-key-file=/var/lib/garmin-mcp/master.key",
			},
			sentinel: cmd.ErrNotImplemented,
		},
		{
			name:     "unknown transport is rejected",
			args:     []string{cmdServe, "--transport=sse"},
			sentinel: config.ErrUnsupportedTransport,
		},
		{
			name:     "incomplete streamable http is rejected",
			args:     []string{cmdServe, "--transport=streamable-http"},
			sentinel: config.ErrMissingSetting,
		},
		{
			name:     "a listener setting is rejected for stdio",
			args:     []string{cmdServe, flagStdio, "--public-url=https://mcp.example.test"},
			sentinel: config.ErrInapplicableSetting,
		},
		{
			name:     "an unknown region is rejected",
			args:     []string{cmdServe, "--region=garmin.example.test"},
			sentinel: config.ErrInvalidConfig,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, err := runCommand(t, tc.args...)

			if err == nil {
				t.Fatal("serve returned no error, but no server exists to have been started")
			}
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("error %v does not match %v", err, tc.sentinel)
			}
			if stdout != "" {
				t.Errorf("serve wrote %q to stdout, which is reserved for MCP frames", stdout)
			}
		})
	}
}

// TestServeStdioWritesNothingToStdout is the frame-stream guarantee: in stdio
// mode standard output carries MCP protocol output and nothing else, so a
// diagnostic or an error must never appear there.
func TestServeStdioWritesNothingToStdout(t *testing.T) {
	clearGarminEnv(t)

	var stdout, stderr bytes.Buffer
	code := cmd.Execute(context.Background(), cmd.Options{
		BuildInfo: cmd.BuildInfo{Version: testVersion, Commit: testCommit},
		Args:      []string{cmdServe, flagStdio},
		Stdout:    &stdout,
		Stderr:    &stderr,
	})

	if code == 0 {
		t.Error("exit code = 0, but no MCP server was started")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty: it is reserved for MCP frames", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not implemented in this milestone") {
		t.Errorf("stderr = %q, want the not-implemented diagnostic", stderr.String())
	}
}

// TestServeUsageErrorStaysOffStdout covers the flag-parsing path, which Cobra
// would otherwise report on the output stream.
func TestServeUsageErrorStaysOffStdout(t *testing.T) {
	clearGarminEnv(t)

	var stdout, stderr bytes.Buffer
	code := cmd.Execute(context.Background(), cmd.Options{
		Args:   []string{cmdServe, "--not-a-flag"},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if code == 0 {
		t.Error("exit code = 0 for an unknown flag")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty: the operator learns nothing about the rejected flag")
	}
}

// TestServeRejectsAnInlineSecretOrCredentialFlag proves the credential and secret
// rules hold on the command line: neither the master key, a token document, a
// password, nor an MFA code has a flag.
func TestServeRejectsAnInlineSecretOrCredentialFlag(t *testing.T) {
	clearGarminEnv(t)

	for _, arg := range []string{"--master-key=x", "--garmin-tokens=x", "--password=x", "--mfa-code=1"} {
		t.Run(arg, func(t *testing.T) {
			stdout, err := runCommand(t, cmdServe, arg)

			if err == nil {
				t.Fatalf("serve accepted %s", arg)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
		})
	}
}
