//go:build fakegarmin

package cmd

import (
	"net/http"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The synthetic account the scripted Garmin service answers for. Nothing here
// resembles a real account, ticket, or token, and no request leaves the process.
const (
	fakeEmail    = "fake-user@example.invalid"
	fakePassword = "fake-password-0500"
	fakeTicket   = "ST-fake-0501"
	fakeAccess   = "di-access-fake-0502"
	fakeRefresh  = "di-refresh-fake-0503"
)

// newFakeAuthenticator wires an authenticator to a scripted successful login,
// writing through the store the caller supplies — which is the real SQLite-backed
// adapter in these tests, because the point is that the composition root's own seam
// persists what a login produced.
func newFakeAuthenticator(t *testing.T, tokens auth.TokenStore) *auth.Authenticator {
	t.Helper()

	script := testkit.NewScript().
		With(protocol.PathMobileLogin,
			testkit.JSON(http.StatusOK, testkit.LoginSuccessJSON(fakeTicket))).
		With(protocol.PathDIToken,
			testkit.JSON(http.StatusOK, testkit.DITokenJSON(fakeAccess, fakeRefresh))).
		With(protocol.PathSocialProfile,
			testkit.JSON(http.StatusOK, testkit.SocialProfileJSON("Fake User")))

	return newAuthenticatorOn(t, testkit.NewServer(t, script), tokens)
}

// newAuthenticatorOn wires an authenticator to an already-scripted server, so a
// test can script a rejection as easily as a success.
func newAuthenticatorOn(
	t *testing.T, server *testkit.Server, tokens auth.TokenStore,
) *auth.Authenticator {
	t.Helper()

	registry, err := auth.NewRegistry(auth.RegistryConfig{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(auth.Config{
		Hosts:     server.Hosts(protocol.DomainGlobal),
		Transport: server.Doer(),
		Store:     tokens,
		Registry:  registry,
		// The pacing jitter is pinned to its lower bound, so the test observes
		// the behavior without waiting out the anti-WAF window.
		Jitter: func(minDelay, _ time.Duration) time.Duration { return minDelay },
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return authenticator
}

// TestRemoteLoginPersistsTokensForTheResolvedPrincipal is the multi-user login end
// to end through the composition root's own seams: the account is discovered from
// the credentials, a principal is resolved for it, and the DI token set the login
// produced is stored under that principal and nobody else.
func TestRemoteLoginPersistsTokensForTheResolvedPrincipal(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	seam := newRemoteSeam(t, remote)

	attempt, err := seam.Login(t.Context(), fakeEmail, fakePassword)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if attempt.NeedsMFA {
		t.Fatal("the scripted login asked for a one-time code")
	}
	if attempt.Principal == "" {
		t.Fatal("a completed login resolved no principal, so no transaction could be bound")
	}

	stored, _, err := remote.deps.tokens.Load(t.Context(), attempt.Principal)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if stored.Token() != fakeAccess || stored.RefreshToken() != fakeRefresh {
		t.Error("the stored token set is not the one the login produced")
	}

	// A second login for the same account is the same principal: one Garmin
	// account is one tenant, however many times its owner authorizes a client.
	second, err := seam.Login(t.Context(), fakeEmail, fakePassword)
	if err != nil {
		t.Fatalf("the second Login returned error: %v", err)
	}
	if second.Principal != attempt.Principal {
		t.Errorf("two logins resolved %q and %q for one account",
			attempt.Principal, second.Principal)
	}
}
