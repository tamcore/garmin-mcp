package cmd_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/cmd"
)

// runAuth executes the auth command with a bounded context and reports both streams
// and the exit code.
func runAuth(t *testing.T, timeout time.Duration, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var out, errOut bytes.Buffer
	code = cmd.Execute(ctx, cmd.Options{
		BuildInfo: cmd.BuildInfo{Version: testVersion, Commit: testCommit},
		Args:      append([]string{cmdAuth}, args...),
		Stdin:     strings.NewReader(""),
		Stdout:    &out,
		Stderr:    &errOut,
	})
	return out.String(), errOut.String(), code
}

// TestAuthPrintsALoopbackURLAndNeverAWildcardBind is the listener contract: the
// browser is sent to 127.0.0.1 with a kernel-chosen port, never to a wildcard bind
// that would accept credentials from the whole network.
func TestAuthPrintsALoopbackURLAndNeverAWildcardBind(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	stdout, stderr, code := runAuth(t, 2*time.Second, "--no-browser")

	if !strings.Contains(stdout, "http://127.0.0.1:") {
		t.Errorf("stdout does not carry the loopback URL:\n%s", stdout)
	}
	for _, forbidden := range []string{"0.0.0.0", "[::]", "http://localhost"} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("stdout offers %q, which is not a loopback bind:\n%s", forbidden, stdout)
		}
	}
	if code == 0 {
		t.Error("exit code = 0, but the run was cancelled before any account was linked")
	}
	if !strings.Contains(stderr, "cancel") && !strings.Contains(stderr, "not linked") {
		t.Errorf("stderr does not explain the outcome:\n%s", stderr)
	}
}

// TestAuthTTYFlowRefusesWhenNoTerminalIsAttached is the rule that keeps Garmin
// credentials off MCP stdio: the terminal flow runs only on a real terminal, and a
// pipe is refused instead of read.
func TestAuthTTYFlowRefusesWhenNoTerminalIsAttached(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	stdout, stderr, code := runAuth(t, 5*time.Second, "--tty")

	if code == 0 {
		t.Error("exit code = 0, but no terminal is attached")
	}
	if strings.Contains(stdout, "assword") {
		t.Errorf("a credential was prompted for on a stream that is not a terminal:\n%s", stdout)
	}
	if !strings.Contains(stderr, "terminal") {
		t.Errorf("stderr does not name the missing terminal:\n%s", stderr)
	}
}

// TestAuthTTYAndBrowserFlowsAreExclusive keeps the two entry points from racing for
// the same transaction.
func TestAuthTTYAndBrowserFlowsAreExclusive(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	_, stderr, code := runAuth(t, 5*time.Second, "--tty", "--browser")

	if code == 0 {
		t.Error("exit code = 0 for two mutually exclusive flows")
	}
	if !strings.Contains(stderr, "tty") {
		t.Errorf("stderr does not name the conflicting flags:\n%s", stderr)
	}
}

// TestAuthRefusesInlineMasterKeyMaterial fails closed before any listener binds.
func TestAuthRefusesInlineMasterKeyMaterial(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)
	t.Setenv("GARMIN_MCP_MASTER_KEY", "c2VjcmV0LW1hdGVyaWFs")

	stdout, stderr, code := runAuth(t, 5*time.Second, "--no-browser")

	if code == 0 {
		t.Fatal("auth accepted inline master key material")
	}
	if strings.Contains(stdout, "http://127.0.0.1:") {
		t.Error("a listener was offered despite the refused key material")
	}
	if strings.Contains(stderr, "c2VjcmV0LW1hdGVyaWFs") {
		t.Error("stderr echoes the supplied key material")
	}
}

// TestAuthCredentialFlagsDoNotExist keeps credentials off the command line, where
// every local process can read them.
func TestAuthCredentialFlagsDoNotExist(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	for _, arg := range []string{"--email=person@example.test", "--password=x", "--mfa-code=1"} {
		t.Run(arg, func(t *testing.T) {
			_, err := runCommand(t, cmdAuth, arg)
			if err == nil {
				t.Fatalf("auth accepted %s", arg)
			}
			if !strings.Contains(err.Error(), "unknown flag") {
				t.Errorf("error %v does not report an unknown flag", err)
			}
		})
	}
}

// TestAuthReportsTheOutcomeWithoutLeakingTheTransaction keeps the run's capability
// out of everything a user or a log can see, and proves the command is no longer a
// declared gap.
func TestAuthReportsTheOutcomeWithoutLeakingTheTransaction(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	stdout, stderr, _ := runAuth(t, 2*time.Second, "--no-browser")

	for _, stream := range []string{stdout, stderr} {
		if strings.Contains(stream, "garmin_mcp_login=") {
			t.Errorf("a stream carries the run cookie:\n%s", stream)
		}
		if strings.Contains(stream, "not implemented in this milestone") {
			t.Errorf("auth still reports itself as unimplemented:\n%s", stream)
		}
	}
}
