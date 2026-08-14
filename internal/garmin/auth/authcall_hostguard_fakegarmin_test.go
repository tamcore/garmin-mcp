//go:build fakegarmin

package auth_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The allowlist is derived from the configured Hosts, so a testkit override still
// reaches the fake origin, while a real Garmin host is refused for the same
// Refresher.
func TestDoAllowsTheTestkitOverriddenOrigin(t *testing.T) {
	server := testkit.NewServer(t, testkit.NewScript().
		With(protocol.PathSocialProfile, testkit.JSON(http.StatusOK,
			testkit.SocialProfileJSON("Fake User"))))
	store := newFakeStore()
	store.put(testPrincipalID, storedSet(refreshStart().Add(time.Hour)), 1)

	refresher, err := auth.NewRefresher(auth.RefreshConfig{
		Hosts:     server.Hosts(protocol.DomainGlobal),
		Transport: server.Doer(),
		Store:     store,
		Clock:     testkit.NewFakeClock(refreshStart()),
	})
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		server.BaseURL()+protocol.PathSocialProfile, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := refresher.Do(t.Context(), testPrincipalID, req)
	if err != nil {
		t.Fatalf("Do against the fake origin: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(server.Requests()) != 1 {
		t.Fatalf("%d requests reached the fake server, want 1", len(server.Requests()))
	}

	foreign, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://connectapi.garmin.com"+protocol.PathSocialProfile, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if _, err := refresher.Do(t.Context(), testPrincipalID, foreign); !errors.Is(err, auth.ErrForeignHost) {
		t.Fatalf("err = %v, want ErrForeignHost for a real Garmin host under overrides", err)
	}
	if len(server.Requests()) != 1 {
		t.Fatalf("the refused request reached the fake server: %v", server.Requests())
	}
}
