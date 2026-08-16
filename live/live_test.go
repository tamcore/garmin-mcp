//go:build garminlive

package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The gate environment variables. All three must be set, on top of the garminlive
// build tag, before one request is dispatched.
const (
	envUsername = "GARMIN_USERNAME"
	envPassword = "GARMIN_PASSWORD"
	envAck      = "GARMIN_LIVE_ACK"

	// envMFACode carries a one-time code for an account that challenges the login.
	// It is optional: without it an MFA challenge skips the suite rather than
	// hanging on a prompt no test can answer.
	envMFACode = "GARMIN_LIVE_MFA_CODE"
)

// ackValue is the exact value envAck must carry. It is spelled out rather than
// truthy, so no stray "1" in an environment can start live traffic by accident.
const ackValue = "i-accept-live-garmin-traffic"

// livePrincipal is the synthetic account key this suite stores its token set under.
// It is not an account selector: the credentials decide whose account is reached,
// and this is only the key of the temporary store record.
const livePrincipal = "garminlive-suite"

// Pacing and bounds. This suite is a guest on an unofficial private API: requests
// are serial, spaced, bounded in number, and given a generous but finite deadline.
const (
	requestPause   = 400 * time.Millisecond
	requestTimeout = 90 * time.Second
	dialTimeout    = 15 * time.Second
	keyVersion     = 1

	// maxFITCandidates bounds how many recent activities the FIT cross-check may
	// download before it gives up looking for one it can analyse.
	maxFITCandidates = 3
)

// stateDir is the temporary directory every piece of state lives in. It is created
// by TestMain and removed when the suite ends, so this suite never reads or writes
// the maintainer's own token store, key or configuration.
var stateDir string

// closers releases what the shared session opened. TestMain runs them after the
// tests, because the session outlives every individual test by design: one login is
// shared by the whole suite rather than repeated per test.
var closers []func()

// shared builds the one authenticated session the suite uses.
var shared = sync.OnceValues(buildEnv)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "garmin-mcp-live")
	if err != nil {
		suiteLogger().Error("live: creating the temporary state directory",
			slog.String("reason", safeError(err)))
		os.Exit(1)
	}
	// internal/securefile refuses a path reached through a symlink, and the
	// platform temporary directory is one on macOS, so the directory is resolved
	// before any secret file is installed under it.
	stateDir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		suiteLogger().Error("live: resolving the temporary state directory",
			slog.String("reason", safeError(err)))
		os.Exit(1)
	}

	code := m.Run()

	// A created object that is still owned when the suite ends was not deleted, and
	// a leaked object on a real account is a defect rather than a warning. It is
	// reported here because no individual test outlives its own cleanup.
	if leaked := leakedObjects(); leaked != "" && code == 0 {
		code = 1
	}

	for _, release := range closers {
		release()
	}
	if err := os.RemoveAll(dir); err != nil {
		suiteLogger().Error("live: removing the temporary state directory",
			slog.String("reason", safeError(err)))
	}
	os.Exit(code)
}

// gate reports why the suite must not run, or "" when every gate is open.
func gate() string {
	if os.Getenv(envAck) != ackValue {
		return fmt.Sprintf(
			"not run — acknowledgement absent: set %s=%s to allow this suite to contact the real Garmin service",
			envAck, ackValue)
	}
	if os.Getenv(envUsername) == "" || os.Getenv(envPassword) == "" {
		return fmt.Sprintf(
			"not run — credentials unavailable: set %s and %s for a dedicated non-primary Garmin account",
			envUsername, envPassword)
	}
	return ""
}

// env is the one authenticated live session, plus everything built on it.
type env struct {
	// skip is the reason the suite must not run. Every other field is unset when
	// it is non-empty.
	skip string

	// strategy names the login strategy that succeeded. It is the drift signal: a
	// fallback that used to be unnecessary becoming necessary is visible here.
	strategy string

	// now is the one instant every date argument derives from, captured at start-up
	// so a run crossing UTC midnight still asks every tool for the same day.
	now time.Time

	session client.Session
	mcp     *mcp.ClientSession

	// caller is the read-only guard every tool call passes through. The sweep reads
	// its request count, so a tool that answered without reaching Garmin fails.
	caller *readOnlyCaller

	// rest and refresher are the two pieces the write layer builds its own guarded
	// session on. They are retained rather than rebuilt so the whole suite performs
	// exactly one login: the write layer wraps the same refresher in its own guard.
	rest      *client.Client
	refresher client.Caller

	activities *api.Activities
	details    *api.ActivityDetails
	files      *api.ActivityFiles
	profile    *api.Profile
	devices    *api.Devices
}

// liveEnv returns the shared session, skipping the calling test when a gate is shut.
func liveEnv(t *testing.T) *env {
	t.Helper()

	e, err := shared()
	if err != nil {
		t.Fatalf("live: preparing the authenticated session: %v", err)
	}
	if e.skip != "" {
		t.Skip(e.skip)
	}
	return e
}

// The read-only guard every client and tool in this suite reaches Garmin through
// lives in readguard_test.go.

// The assembly of the shared session — the temporary store, the login, the domain
// clients and the MCP server — lives in liveenv_test.go.

// call invokes one tool over the MCP session and requires a successful result. It
// paces itself, so a whole-surface sweep does not arrive at Garmin as a burst.
func (e *env) call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()

	result := e.rawCall(t, name, args)
	if result.IsError {
		t.Fatalf("%s returned an error result: %s", name, resultText(result))
	}
	if result.StructuredContent == nil {
		t.Fatalf("%s returned no structured content", name)
	}

	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshalling the %s result: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding the %s result: %v", name, err)
	}
	return out
}

// rawCall invokes one tool and returns whatever came back, an error result included.
func (e *env) rawCall(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	if args == nil {
		args = map[string]any{}
	}
	time.Sleep(requestPause)

	ctx, cancel := context.WithTimeout(t.Context(), 4*requestTimeout)
	defer cancel()

	result, err := e.mcp.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error: %v", name, err)
	}
	return result
}

// resultText joins the textual content of a result. It is used only for a refusal,
// whose message is an authored remediation rather than a payload.
func resultText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String()
}
