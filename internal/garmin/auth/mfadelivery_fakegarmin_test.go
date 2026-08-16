//go:build fakegarmin

package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// widgetMFAScript stages a widget login that lands on an MFA page carrying the
// inline variables, with the delivery endpoint answering as the caller wants.
func widgetMFAScript(page string, delivery testkit.Behavior) testkit.Script {
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.RateLimited(30)).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.HTML(http.StatusOK, page))
	return script.With(protocol.PathWidgetRequestMFACode, delivery)
}

// TestWidgetLoginRequestsAnEmailCode is upstream GH-386: this server asks Garmin to
// send the code rather than assuming the page already did.
func TestWidgetLoginRequestsAnEmailCode(t *testing.T) {
	h := newHarness(t, widgetMFAScript(
		testkit.WidgetMFAVarsHTML(testkit.WidgetTitleEmailMFA, "email", ""),
		testkit.JSON(http.StatusOK, `{"status":"ok"}`),
	))

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !result.NeedsMFA() || result.Strategy() != auth.StrategyWidget {
		t.Fatalf("Login returned %v, want a widget MFA challenge", result)
	}

	if got := h.requestCount(protocol.PathWidgetRequestMFACode); got != 1 {
		t.Fatalf("the delivery endpoint was asked %d times, want once: %v", got, h.paths())
	}
	if result.MFADeliveryUncertain() {
		t.Error("Garmin accepted the delivery request, so the code is not uncertain")
	}
	h.assertNoCredentialsInQueries()
}

// TestAnAuthenticatorAppIsNeverAskedForACode covers the case with nothing to send.
// Asking Garmin to deliver a code for a TOTP app is a request it cannot honour.
func TestAnAuthenticatorAppIsNeverAskedForACode(t *testing.T) {
	h := newHarness(t, widgetMFAScript(
		testkit.WidgetMFAVarsHTML(testkit.WidgetTitleTOTPMFA, "totp", ""),
		testkit.JSON(http.StatusOK, `{"status":"ok"}`),
	))

	if _, err := h.login(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := h.requestCount(protocol.PathWidgetRequestMFACode); got != 0 {
		t.Errorf("an authenticator-app page triggered %d delivery requests: %v",
			got, h.paths())
	}
}

// TestACodeAlreadySentIsNotRequestedTwice keeps this server from sending the account
// a second message when the sign-in POST already delivered one.
func TestACodeAlreadySentIsNotRequestedTwice(t *testing.T) {
	h := newHarness(t, widgetMFAScript(
		testkit.WidgetMFAVarsHTML(testkit.WidgetTitleEmailMFA, "email", "u***@example.com"),
		testkit.JSON(http.StatusOK, `{"status":"ok"}`),
	))

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := h.requestCount(protocol.PathWidgetRequestMFACode); got != 0 {
		t.Errorf("a code was already sent and %d further requests were made: %v",
			got, h.paths())
	}
	if result.MFADeliveryUncertain() {
		t.Error("the page named where the code went, so delivery is not uncertain")
	}
}

// TestAFailedDeliveryRequestStillAllowsTheLogin is the deliberate divergence from
// upstream, which raises. The account may have been sent a code anyway, so refusing
// the login would break a flow that works; the caller is told delivery is uncertain
// instead.
func TestAFailedDeliveryRequestStillAllowsTheLogin(t *testing.T) {
	for _, tc := range []struct {
		name     string
		delivery testkit.Behavior
	}{
		{"rate limited", testkit.RateLimited(30)},
		{"server error", testkit.JSON(http.StatusInternalServerError, `{"error":"nope"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, widgetMFAScript(
				testkit.WidgetMFAVarsHTML(testkit.WidgetTitleEmailMFA, "email", ""),
				tc.delivery,
			))

			result, err := h.login()
			if err != nil {
				t.Fatalf("a failed courtesy request must not fail the login: %v", err)
			}
			if !result.NeedsMFA() {
				t.Fatal("the challenge was lost")
			}
			if !result.MFADeliveryUncertain() {
				t.Error("the delivery request failed, so delivery is exactly what is uncertain")
			}
			if got := h.requestCount(protocol.PathWidgetRequestMFACode); got != 1 {
				t.Errorf("the delivery endpoint was asked %d times, want one attempt "+
					"and no retry: %v", got, h.paths())
			}
		})
	}
}

// TestTheDeliveryRequestCarriesTheAccountIdentifierOnlyInItsBody keeps the customer
// GUID out of the query string, where it would reach access logs and Referer
// headers. Upstream puts the client id in the query and the GUID in the body, and so
// does this.
func TestTheDeliveryRequestCarriesTheAccountIdentifierOnlyInItsBody(t *testing.T) {
	h := newHarness(t, widgetMFAScript(
		testkit.WidgetMFAVarsHTML(testkit.WidgetTitleEmailMFA, "email", ""),
		testkit.JSON(http.StatusOK, `{"status":"ok"}`),
	))

	if _, err := h.login(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	var seen int
	for _, request := range h.server.Requests() {
		if !strings.Contains(request.Path, protocol.PathWidgetRequestMFACode) {
			continue
		}
		seen++
		query := request.Query.Encode()
		if strings.Contains(query, testkit.FakeCustomerGUID) {
			t.Errorf("the customer GUID reached the query string: %s", query)
		}
		if request.Query.Get("clientId") == "" {
			t.Errorf("the client id is missing from the query: %s", query)
		}
		if !strings.Contains(string(request.Body), testkit.FakeCustomerGUID) {
			t.Error("the delivery request body does not identify the account")
		}
	}
	if seen != 1 {
		t.Fatalf("the delivery endpoint was reached %d times, want once", seen)
	}
}
