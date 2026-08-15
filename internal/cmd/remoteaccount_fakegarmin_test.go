//go:build fakegarmin

package cmd

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/store"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The second login handle the same Garmin account is reached under. One person
// changing the email on their Garmin account, or spelling it differently, is one
// tenant either way.
const fakeSecondEmail = "renamed-user@example.invalid"

// The account identifier the scripted profile reports, as testkit writes it.
const fakeAccountID = "900001"

// newRemoteSeam builds the login seam over the deployment's own token store,
// staging area and token gate, which is what the composition root does.
func newRemoteSeam(t *testing.T, remote *remoteDeployment) *remoteLogin {
	t.Helper()

	seam, err := newRemoteLogin(remoteLoginDeps{
		authenticator: newFakeAuthenticator(t, remote.deps.tokens),
		directory:     remote.sqlite,
		tokens:        remote.deps.tokens,
		staging:       remote.deps.staging,
		gate:          remote.deps.tokenGate,
	})
	if err != nil {
		t.Fatalf("newRemoteLogin returned error: %v", err)
	}
	return seam
}

// The isolation key must not be the email. Two logins Garmin attributes to one
// account are one principal, however the login handle is spelled.
func TestRemoteLoginKeysThePrincipalOnTheGarminAccount(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	seam := newRemoteSeam(t, remote)

	first, err := seam.Login(t.Context(), fakeEmail, fakePassword)
	if err != nil {
		t.Fatalf("the first Login returned error: %v", err)
	}
	second, err := seam.Login(t.Context(), fakeSecondEmail, fakePassword)
	if err != nil {
		t.Fatalf("the second Login returned error: %v", err)
	}

	if first.Principal == "" {
		t.Fatal("a completed login resolved no principal")
	}
	if second.Principal != first.Principal {
		t.Errorf("one garmin account became two principals: %q and %q",
			first.Principal, second.Principal)
	}

	linked, err := remote.sqlite.PrincipalByGarminAccount(t.Context(),
		store.NewSecret(fakeAccountID))
	if err != nil {
		t.Fatalf("PrincipalByGarminAccount: %v", err)
	}
	if linked.ID != first.Principal {
		t.Errorf("the garmin account is linked to %q, want the logged-in principal %q",
			linked.ID, first.Principal)
	}
	if !linked.GarminLinked {
		t.Error("the principal reports no garmin linkage after a completed login")
	}

	// The second handle never became a principal of its own: email is a login
	// handle and a display string, never the boundary.
	if _, err := remote.sqlite.PrincipalByEmail(t.Context(), fakeSecondEmail); !errors.Is(
		err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByEmail for the second handle = %v, want ErrPrincipalNotFound", err)
	}
}

// A principal is the record of an account Garmin confirmed. Creating one before the
// credentials are accepted lets anyone who can reach the login page write rows.
func TestAFailedLoginLeavesNoPrincipalBehind(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	rejection := testkit.JSON(http.StatusOK, testkit.LoginInvalidCredentialsJSON())
	server := testkit.NewServer(t, testkit.NewScript().
		With(protocol.PathMobileLogin, rejection).
		With(protocol.PathWidgetEmbed, rejection).
		With(protocol.PathPortalLogin, rejection))

	seam, err := newRemoteLogin(remoteLoginDeps{
		authenticator: newAuthenticatorOn(t, server, remote.deps.tokens),
		directory:     remote.sqlite,
		tokens:        remote.deps.tokens,
		staging:       remote.deps.staging,
		gate:          remote.deps.tokenGate,
	})
	if err != nil {
		t.Fatalf("newRemoteLogin returned error: %v", err)
	}

	if _, err := seam.Login(t.Context(), fakeEmail, fakePassword); err == nil {
		t.Fatal("the scripted rejection reported a successful login")
	}

	if _, err := remote.sqlite.PrincipalByEmail(t.Context(), fakeEmail); !errors.Is(
		err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByEmail after a failed login = %v, want ErrPrincipalNotFound", err)
	}
	if _, err := remote.sqlite.PrincipalByGarminAccount(t.Context(),
		store.NewSecret(fakeAccountID)); !errors.Is(err, store.ErrPrincipalNotFound) {
		t.Errorf("PrincipalByGarminAccount after a failed login = %v, want ErrPrincipalNotFound", err)
	}
}

// An MFA login is two requests, and the principal is resolved in the second. The
// staging key has to survive the continuation, or every MFA-protected account would
// lose the token set its login produced.
func TestRemoteLoginBindsAnMFAContinuation(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	server := testkit.NewServer(t, testkit.NewScript().
		With(protocol.PathMobileLogin, testkit.JSON(http.StatusOK,
			testkit.LoginMFARequiredJSON(protocol.MFAMethodEmail))).
		With(protocol.PathMobileMFAVerifyCode, testkit.JSON(http.StatusOK,
			testkit.LoginSuccessJSON(fakeTicket))).
		With(protocol.PathDIToken, testkit.JSON(http.StatusOK,
			testkit.DITokenJSON(fakeAccess, fakeRefresh))).
		With(protocol.PathSocialProfile, testkit.JSON(http.StatusOK,
			testkit.SocialProfileJSON("Fake User"))))

	seam, err := newRemoteLogin(remoteLoginDeps{
		authenticator: newAuthenticatorOn(t, server, remote.deps.tokens),
		directory:     remote.sqlite,
		tokens:        remote.deps.tokens,
		staging:       remote.deps.staging,
		gate:          remote.deps.tokenGate,
	})
	if err != nil {
		t.Fatalf("newRemoteLogin returned error: %v", err)
	}

	challenge, err := seam.Login(t.Context(), fakeEmail, fakePassword)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if !challenge.NeedsMFA {
		t.Fatal("the scripted login did not ask for a one-time code")
	}
	if challenge.Principal != "" {
		t.Error("a pending login already reported a principal")
	}

	completed, err := seam.CompleteMFA(t.Context(), challenge.TransactionID, "424242")
	if err != nil {
		t.Fatalf("CompleteMFA returned error: %v", err)
	}
	if completed.Principal == "" {
		t.Fatal("a completed continuation resolved no principal")
	}

	stored, _, err := remote.deps.tokens.Load(t.Context(), completed.Principal)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Token() != fakeAccess {
		t.Error("the continuation did not commit the token set the login produced")
	}
	if remote.deps.staging.inFlight() != 0 {
		t.Errorf("%d logins are still staged after a completed continuation, want none",
			remote.deps.staging.inFlight())
	}
}

// The linkage is what a later login is resolved by, so it has to survive in a form
// the store can match — and never in the clear, which is the store's own rule.
func TestRemoteLoginStoresTheGarminIdentity(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	seam := newRemoteSeam(t, remote)

	attempt, err := seam.Login(t.Context(), fakeEmail, fakePassword)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	identity, err := remote.sqlite.GarminIdentity(t.Context(), attempt.Principal)
	if err != nil {
		t.Fatalf("GarminIdentity: %v", err)
	}
	if identity.AccountID.Reveal() != fakeAccountID {
		t.Errorf("the stored account id is %q, want %q",
			identity.AccountID.Reveal(), fakeAccountID)
	}

	stored, _, err := remote.deps.tokens.Load(t.Context(), attempt.Principal)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Token() != fakeAccess || stored.RefreshToken() != fakeRefresh {
		t.Error("the token set was not committed to the resolved principal")
	}
}
