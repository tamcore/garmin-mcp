// This test file is deliberately in the external test package: it asserts what a
// package that only imports auth can and cannot reach.
package auth_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// Method-stripping aliases. A defined type with the same underlying type as a
// secret-bearing value has none of its methods, so fmt cannot call String,
// GoString, MarshalJSON or LogValue and falls back to reflection. Nothing readable
// may come out of that path under any verb.
type (
	strippedTokenSet    auth.TokenSet
	strippedCredentials auth.Credentials
	strippedPending     auth.Pending
)

// A Result is stripped in result_internal_test.go instead: its secret-bearing
// content — the MFA capability and the confirmed Garmin account — can only be
// produced inside the package, and the stripping path fmt takes is the same one
// either way.

// strippedForms are the verbs a stripped alias is rendered under. %s and %q reach
// fmt's badVerb path, which re-prints the value at depth zero and therefore
// dereferences the pointer to the sealed content.
func strippedForms(value any) map[string]string {
	return map[string]string{
		"%v":  fmt.Sprintf("%v", value),
		"%+v": fmt.Sprintf("%+v", value),
		"%#v": fmt.Sprintf("%#v", value),
		"%s":  fmt.Sprintf("%s", value),
		"%q":  fmt.Sprintf("%q", value),
	}
}

func TestStrippedTokenSetLeaksNothingUnderAnyVerb(t *testing.T) {
	set := strippedTokenSet(secretTokenSet())

	for verb, rendered := range strippedForms(set) {
		assertNoTokenLeak(t, "stripped TokenSet "+verb, rendered)
	}
}

func TestStrippedCredentialsLeakNothingUnderAnyVerb(t *testing.T) {
	creds := strippedCredentials(auth.NewCredentials(leakEmail, leakPassword))

	for verb, rendered := range strippedForms(creds) {
		assertNoTokenLeak(t, "stripped Credentials "+verb, rendered)
	}
}

func TestStrippedPendingLeaksNothingUnderAnyVerb(t *testing.T) {
	pending := strippedPending(pendingFor(principalA, leakCSRF, leakCookie))

	for verb, rendered := range strippedForms(pending) {
		for _, bad := range []string{leakCSRF, leakCookie, principalA} {
			if strings.Contains(rendered, bad) {
				t.Fatalf("stripped Pending %s rendering %q leaked %q", verb, rendered, bad)
			}
		}
	}
}

// The counter-test: the normal rendering must stay informative, or the redaction
// would be indistinguishable from rendering nothing at all.
func TestNormalRenderingRemainsInformative(t *testing.T) {
	cases := map[string]struct {
		rendered string
		want     []string
	}{
		"TokenSet": {
			rendered: fmt.Sprintf("%v", secretTokenSet()),
			want: []string{
				"auth.TokenSet", "token:present", "refreshToken:present",
				testClientID,
			},
		},
		"Credentials": {
			rendered: fmt.Sprintf("%v", auth.NewCredentials(leakEmail, leakPassword)),
			want:     []string{"auth.Credentials", "email:present", "password:present"},
		},
		"Pending": {
			rendered: fmt.Sprintf("%v", pendingFor(principalA, leakCSRF, leakCookie)),
			want:     []string{"auth.Pending", "csrfToken:present", "cookieCount:1", "mfa_pending"},
		},
	}

	for name, tc := range cases {
		for _, want := range tc.want {
			if !strings.Contains(tc.rendered, want) {
				t.Errorf("%s rendering %q does not report %q", name, tc.rendered, want)
			}
		}
	}
}

// An empty secret must be reported as absent, not as a present empty value: the
// sealed representation carries no pointer for it.
func TestPresenceOfEmptySecretsIsReportedAsAbsent(t *testing.T) {
	set := auth.NewTokenSet("", "", "", secretTokenSet().ExpiresAt())

	rendered := set.String()
	if !strings.Contains(rendered, "token:absent") || !strings.Contains(rendered, "refreshToken:absent") {
		t.Errorf("rendering %q does not report the absent tokens", rendered)
	}
	if !set.IsZero() {
		t.Error("a set with no token is not reported as zero")
	}
	if set.Token() != "" || set.RefreshToken() != "" {
		t.Error("an empty secret did not round-trip as empty")
	}
}
