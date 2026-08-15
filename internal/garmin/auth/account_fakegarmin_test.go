//go:build fakegarmin

package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The identifiers the scripted social profile reports. They are what
// testkit.SocialProfileJSON writes, and neither is a real account.
const (
	fakeProfileID   = "900001"
	fakeProfileName = "Fake User"
)

// The isolation key of a multi-user deployment cannot be an email, so a login has
// to report the account Garmin itself identified. The profile is already fetched to
// validate the session, so the identifier costs no extra request.
func TestLoginSurfacesTheGarminAccountIdentifier(t *testing.T) {
	h := newHarness(t, mobileSuccessScript())

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if result.GarminAccountID() != fakeProfileID {
		t.Errorf("GarminAccountID() = %q, want %q", result.GarminAccountID(), fakeProfileID)
	}
	if result.GarminDisplayName() != fakeProfileName {
		t.Errorf("GarminDisplayName() = %q, want %q", result.GarminDisplayName(), fakeProfileName)
	}
	if h.requestCount(protocol.PathSocialProfile) != 1 {
		t.Errorf("the profile was fetched %d times, want exactly the one validation call",
			h.requestCount(protocol.PathSocialProfile))
	}
}

// A login that finishes through an MFA continuation is the same login, so it must
// report the same account. Without this, every MFA-protected account would reach
// the remote flow with no identifier at all.
func TestCompleteMFASurfacesTheGarminAccountIdentifier(t *testing.T) {
	h := newHarness(t, mobileMFAScript())

	pending, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !pending.NeedsMFA() {
		t.Fatal("the scripted login did not ask for a one-time code")
	}
	if pending.GarminAccountID() != "" {
		t.Error("a pending login reported an account before any credential was accepted")
	}

	completed, err := h.auth.CompleteMFA(t.Context(), pending.TransactionID(), testPrincipal, testMFACode)
	if err != nil {
		t.Fatalf("CompleteMFA: %v", err)
	}
	if completed.GarminAccountID() != fakeProfileID {
		t.Errorf("GarminAccountID() = %q, want %q", completed.GarminAccountID(), fakeProfileID)
	}
}

// A failed login reports no account: the identifier is what Garmin confirmed, so
// reporting one for a login Garmin refused would let a caller act on a claim
// nothing verified.
func TestAFailedLoginReportsNoGarminAccount(t *testing.T) {
	h := newHarness(t, baseScript().With(protocol.PathMobileLogin,
		testkit.JSON(http.StatusOK, testkit.LoginInvalidCredentialsJSON())))

	result, _ := h.login()

	if result.GarminAccountID() != "" {
		t.Errorf("a failed login reported an account identifier")
	}
}

// The account identifier is stable, guessable and account-scoped: it is the value
// an attacker would want in order to correlate users, so it follows the same rule
// as every other secret-bearing field and appears in no rendering.
func TestResultRenderingNeverShowsTheGarminAccount(t *testing.T) {
	h := newHarness(t, mobileSuccessScript())

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	renderings := map[string]string{
		"String":   result.String(),
		"GoString": result.GoString(),
		"%v":       fmt.Sprintf("%v", result),
		"%+v":      fmt.Sprintf("%+v", result),
		"%#v":      fmt.Sprintf("%#v", result),
	}
	for form, rendered := range renderings {
		if strings.Contains(rendered, fakeProfileID) {
			t.Errorf("%s rendering %q leaked the garmin account identifier", form, rendered)
		}
		if strings.Contains(rendered, fakeProfileName) {
			t.Errorf("%s rendering %q leaked the garmin display name", form, rendered)
		}
	}
	if !strings.Contains(result.String(), "garminAccount:present") {
		t.Errorf("rendering %q does not report that an account is present", result.String())
	}
}
