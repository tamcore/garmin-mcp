package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const (
	testPrincipal    = "principal-api-0001"
	fakeDisplayName  = "fake-tester"
	fakeFullName     = "Fake Tester"
	testCalendarDate = "2026-01-31"

	// Enumeration keys the fixtures reuse across the domain tests.
	typeKeyRunning      = "running"
	categorySquat       = "SQUAT"
	categoryPushUp      = "PUSH_UP"
	exerciseBackSquat   = "BACK_SQUAT"
	categoryUnsupported = "NOT_A_CATEGORY"
)

// caller is the principal-scoped caller for the fake service. In production this is
// *auth.Refresher, which attaches the DI bearer token and enforces the Garmin host
// allowlist; here testkit's Doer enforces the origin, so no credential is in play and
// no test can reach the real service.
type caller struct {
	doer testkit.Doer
}

func (c caller) Do(ctx context.Context, principal string, req *http.Request) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("caller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// harness wires a scripted fake Garmin service to a request layer and a session.
type harness struct {
	server  *testkit.Server
	rc      *client.Client
	session client.Session
}

func newHarness(t *testing.T, script testkit.Script, limits client.Limits) harness {
	t.Helper()

	server := testkit.NewServer(t, script)
	rc, err := client.New(client.Config{
		Hosts:   server.Hosts(protocol.DomainGlobal),
		Limits:  limits,
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	session, err := client.NewSession(caller{doer: server.Doer()}, testPrincipal)
	if err != nil {
		t.Fatalf("client.NewSession() = %v", err)
	}
	return harness{server: server, rc: rc, session: session}
}

func mustDisplayName(t *testing.T) client.DisplayName {
	t.Helper()

	name, err := client.ParseDisplayName(fakeDisplayName)
	if err != nil {
		t.Fatalf("ParseDisplayName() = %v", err)
	}
	return name
}

func mustDate(t *testing.T, value string) client.Date {
	t.Helper()

	date, err := client.ParseDate(value)
	if err != nil {
		t.Fatalf("ParseDate(%q) = %v", value, err)
	}
	return date
}

// mustID is the synthetic activity identifier every activity-detail test uses.
func mustID(t *testing.T) client.ID {
	t.Helper()

	id, err := client.NewID(testActivityID)
	if err != nil {
		t.Fatalf("NewID(%d) = %v", testActivityID, err)
	}
	return id
}

func TestEveryDomainClientRefusesANilRequestLayer(t *testing.T) {
	t.Parallel()

	constructors := map[string]func() error{
		"profile":          func() error { _, err := api.NewProfile(nil); return err },
		"activities":       func() error { _, err := api.NewActivities(nil); return err },
		"wellness":         func() error { _, err := api.NewWellness(nil); return err },
		"devices":          func() error { _, err := api.NewDevices(nil); return err },
		"activity details": func() error { _, err := api.NewActivityDetails(nil); return err },
	}

	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := construct(); !errors.Is(err, client.ErrNotConfigured) {
				t.Errorf("constructor = %v, want ErrNotConfigured", err)
			}
		})
	}
}

func TestDomainMethodsRefuseAnUnusableSession(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	profile, err := api.NewProfile(h.rc)
	if err != nil {
		t.Fatalf("NewProfile() = %v", err)
	}

	if _, err := profile.Social(t.Context(), client.Session{}); !errors.Is(err, client.ErrMissingPrincipal) {
		t.Errorf("Social() with a zero session = %v, want ErrMissingPrincipal", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}
