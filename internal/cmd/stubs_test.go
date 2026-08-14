package cmd_test

import (
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
		{name: cmdAuth, args: []string{cmdAuth}},
		{name: cmdDoctor, args: []string{cmdDoctor}},
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

// TestUnimplementedCommandsStillValidateConfiguration keeps configuration a real
// gate rather than something a stub skips: a bad setting must be reported as
// such, not masked by the missing subsystem.
func TestUnimplementedCommandsStillValidateConfiguration(t *testing.T) {
	clearGarminEnv(t)

	for _, args := range [][]string{{cmdAuth}, {cmdDoctor}, {cmdTools, cmdList}, {cmdMigrate}} {
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
