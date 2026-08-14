package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cmd"
)

// TestUnimplementedCommandsFailLoudly covers every command whose subsystem does
// not exist yet. Each must report the gap and exit non-zero; none may look like
// success, and none may write to the frame stream.
func TestUnimplementedCommandsFailLoudly(t *testing.T) {
	clearGarminEnv(t)

	tests := []struct {
		name string
		args []string
	}{
		{name: "tools list", args: []string{cmdTools, cmdList}},
		{name: cmdMigrate, args: []string{cmdMigrate}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, err := runCommand(t, tc.args...)

			if err == nil {
				t.Fatalf("%s reported success for a subsystem that does not exist", tc.name)
			}
			if !errors.Is(err, cmd.ErrNotImplemented) {
				t.Errorf("error %v does not match cmd.ErrNotImplemented", err)
			}

			var gap *cmd.NotImplementedError
			if !errors.As(err, &gap) {
				t.Fatal("errors.As does not extract *cmd.NotImplementedError")
			}
			if !strings.Contains(gap.Error(), "docs/implementation-status.md") {
				t.Errorf("error %q does not point at the status document", gap.Error())
			}
			if stdout != "" {
				t.Errorf("%s wrote %q to stdout", tc.name, stdout)
			}
		})
	}
}

// TestUnimplementedCommandsExitNonZeroWithEmptyStdout goes through Execute, which
// is what the process does: the exit status must be non-zero, standard output must
// be byte-empty because it is reserved for MCP frames, and the operator must learn
// about the gap on the error stream.
func TestUnimplementedCommandsExitNonZeroWithEmptyStdout(t *testing.T) {
	clearGarminEnv(t)

	for _, args := range pendingCommandArgs() {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cmd.Execute(context.Background(), cmd.Options{
				BuildInfo: cmd.BuildInfo{Version: testVersion, Commit: testCommit},
				Args:      args,
				Stdout:    &stdout,
				Stderr:    &stderr,
			})

			if code == 0 {
				t.Error("exit code = 0 for a subsystem that does not exist")
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want byte-empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), "not implemented in this milestone") {
				t.Errorf("stderr = %q, want the not-implemented diagnostic", stderr.String())
			}
		})
	}
}

// TestUnimplementedCommandsReportConfigurationFaultsBeforeTheGap fixes the
// ordering through Execute as well: a misconfigured deployment is reported as
// such, and the not-implemented diagnostic never reaches the operator instead.
func TestUnimplementedCommandsReportConfigurationFaultsBeforeTheGap(t *testing.T) {
	clearGarminEnv(t)

	for _, args := range pendingCommandArgs() {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cmd.Execute(context.Background(), cmd.Options{
				Args:   append(args, "--log-level=trace"),
				Stdout: &stdout,
				Stderr: &stderr,
			})

			if code == 0 {
				t.Error("exit code = 0 for an invalid log level")
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want byte-empty", stdout.String())
			}
			if strings.Contains(stderr.String(), "not implemented in this milestone") {
				t.Errorf("stderr = %q, want the configuration fault instead of the gap", stderr.String())
			}
			if !strings.Contains(stderr.String(), "log-level") {
				t.Errorf("stderr = %q, want it to name the rejected setting", stderr.String())
			}
		})
	}
}

// pendingCommandArgs lists the invocations whose subsystem is still missing. Each
// call returns a fresh slice, so a subtest may append to it.
func pendingCommandArgs() [][]string {
	return [][]string{{cmdTools, cmdList}, {cmdMigrate}}
}

// TestUnimplementedCommandsStillValidateConfiguration keeps configuration a real
// gate rather than something a stub skips: a bad setting must be reported as
// such, not masked by the missing subsystem.
func TestUnimplementedCommandsStillValidateConfiguration(t *testing.T) {
	clearGarminEnv(t)

	for _, args := range pendingCommandArgs() {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := runCommand(t, append(args, "--log-level=trace")...)

			if err == nil {
				t.Fatal("an invalid log level was accepted")
			}
			if errors.Is(err, cmd.ErrNotImplemented) {
				t.Error("configuration was not validated before the gap was reported")
			}
		})
	}
}

// TestToolsWithoutASubcommandShowsHelp keeps the group command informative
// instead of silently succeeding.
func TestToolsWithoutASubcommandShowsHelp(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runCommand(t, cmdTools)
	if err != nil {
		t.Fatalf("tools = %v, want the help text", err)
	}
	if !strings.Contains(stdout, cmdList) {
		t.Errorf("stdout = %q, want it to mention the list subcommand", stdout)
	}
}

// TestRootShowsHelpOnStdout keeps a bare invocation discoverable.
func TestRootShowsHelpOnStdout(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runCommand(t)
	if err != nil {
		t.Fatalf("bare invocation = %v, want the help text", err)
	}
	for _, want := range []string{cmdServe, cmdAuth, cmdDoctor, cmdTools, cmdMigrate, cmdVersion} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not list the %q command", want)
		}
	}
}
