//go:build fakegarmin

package auth_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// mobileMFAScript challenges the mobile login with an OTP and accepts the code on
// the mobile verify endpoint.
func mobileMFAScript() testkit.Script {
	return baseScript().
		With(protocol.PathMobileLogin, testkit.JSON(http.StatusOK,
			testkit.LoginMFARequiredJSON(protocol.MFAMethodEmail))).
		With(protocol.PathMobileMFAVerifyCode, testkit.JSON(http.StatusOK,
			testkit.LoginSuccessJSON(testTicket)))
}

// startMFA runs a login that must end in an MFA challenge and returns the
// capability.
func startMFA(t *testing.T, h *harness) string {
	t.Helper()

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !result.NeedsMFA() {
		t.Fatalf("Login returned %v, want an MFA challenge", result)
	}
	if result.TransactionID() == "" {
		t.Fatal("no transaction capability was issued")
	}
	return result.TransactionID()
}

func TestLoginMFAChallengeThenCompletion(t *testing.T) {
	h := newHarness(t, mobileMFAScript())

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.State() != auth.StateMFAPending {
		t.Fatalf("State() = %s, want mfa_pending", result.State())
	}
	if result.MFAMethod() != protocol.MFAMethodEmail {
		t.Errorf("MFAMethod() = %q, want email", result.MFAMethod())
	}
	if _, _, ok := h.store.get(testPrincipal); ok {
		t.Error("a pending MFA login stored a token set")
	}
	if h.registry.Len() != 1 {
		t.Errorf("registry holds %d transactions, want 1", h.registry.Len())
	}
	// The capability is a bearer credential: no rendering of the Result may show it.
	for form, rendered := range map[string]string{
		formString:   result.String(),
		formGoString: result.GoString(),
		formV:        fmt.Sprintf("%v", result),
		formPlusV:    fmt.Sprintf("%+v", result),
		formHashV:    fmt.Sprintf("%#v", result),
	} {
		if strings.Contains(rendered, result.TransactionID()) {
			t.Fatalf("%s rendering %q leaked the transaction capability", form, rendered)
		}
	}

	completed, err := h.auth.CompleteMFA(t.Context(), result.TransactionID(), testPrincipal, testMFACode)
	if err != nil {
		t.Fatalf("CompleteMFA: %v", err)
	}
	if completed.State() != auth.StateAuthenticated {
		t.Fatalf("State() = %s, want authenticated", completed.State())
	}
	if completed.TransactionID() != "" {
		t.Error("a completed result still carries a transaction capability")
	}
	if _, version, ok := h.store.get(testPrincipal); !ok || version != 1 {
		t.Errorf("stored version = %d, ok = %v, want 1, true", version, ok)
	}
	if h.registry.Len() != 0 {
		t.Errorf("the transaction survived completion: Len() = %d", h.registry.Len())
	}
	h.assertNoCredentialsInQueries()
}

// The code must reach the verify body and nothing else.
func TestCompleteMFASendsTheCodeOnlyInTheRequestBody(t *testing.T) {
	h := newHarness(t, mobileMFAScript())
	capability := startMFA(t, h)

	if _, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode); err != nil {
		t.Fatalf("CompleteMFA: %v", err)
	}

	var verified bool
	for _, req := range h.server.Requests() {
		if req.Path != protocol.PathMobileMFAVerifyCode {
			continue
		}
		verified = strings.Contains(string(req.Body), testMFACode)
		for key, values := range req.Query {
			for _, value := range values {
				if strings.Contains(value, testMFACode) {
					t.Fatalf("the OTP appeared in query key %q", key)
				}
			}
		}
	}
	if !verified {
		t.Fatal("the OTP never reached the verify request body")
	}
}

func TestCompleteMFARejectsBadInput(t *testing.T) {
	h := newHarness(t, mobileMFAScript())
	capability := startMFA(t, h)

	tests := map[string]struct {
		capability string
		principal  string
		code       string
		want       error
	}{
		"empty code": {
			capability: capability, principal: testPrincipal, want: auth.ErrMissingMFACode,
		},
		"empty principal": {
			capability: capability, code: testMFACode, want: auth.ErrMissingPrincipal,
		},
		"other principal": {
			capability: capability, principal: "principal-other", code: testMFACode,
			want: auth.ErrTransactionPrincipalMismatch,
		},
		"unknown capability": {
			capability: "forged", principal: testPrincipal, code: testMFACode,
			want: auth.ErrUnknownTransaction,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := h.auth.CompleteMFA(t.Context(), tc.capability, tc.principal, tc.code)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCompleteMFACannotBeReplayed(t *testing.T) {
	h := newHarness(t, mobileMFAScript())
	capability := startMFA(t, h)

	if _, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode); err != nil {
		t.Fatalf("CompleteMFA: %v", err)
	}

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode)
	if !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("replayed CompleteMFA: err = %v, want ErrUnknownTransaction", err)
	}
}

func TestCompleteMFARejectsExpiredTransaction(t *testing.T) {
	h := newHarness(t, mobileMFAScript())
	capability := startMFA(t, h)

	h.clock.Advance(auth.DefaultTransactionTTL)

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode)
	if !errors.Is(err, auth.ErrTransactionExpired) {
		t.Fatalf("err = %v, want ErrTransactionExpired", err)
	}
}

// The two verify endpoints sit in different rate-limit buckets, so a 429 on the
// flow's own endpoint must fall back to the other one.
func TestCompleteMFAFallsBackToTheOtherRateLimitBucket(t *testing.T) {
	script := mobileMFAScript().
		With(protocol.PathMobileMFAVerifyCode, testkit.RateLimited(30)).
		With(protocol.PathPortalMFAVerifyCode, testkit.JSON(http.StatusOK,
			testkit.LoginSuccessJSON(testTicket)))

	h := newHarness(t, script)
	capability := startMFA(t, h)

	if _, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode); err != nil {
		t.Fatalf("CompleteMFA: %v", err)
	}
	if h.requestCount(protocol.PathPortalMFAVerifyCode) != 1 {
		t.Errorf("the alternate verify endpoint was not used: %v", h.paths())
	}
}

// A wrong code fails the attempt but keeps the transaction usable, because the
// attempt budget is what bounds retries.
func TestCompleteMFAWrongCodeKeepsTheTransactionRetryable(t *testing.T) {
	script := mobileMFAScript().
		With(protocol.PathMobileMFAVerifyCode,
			testkit.JSON(http.StatusOK, testkit.LoginInvalidCredentialsJSON()),
			testkit.JSON(http.StatusOK, testkit.LoginSuccessJSON(testTicket))).
		With(protocol.PathPortalMFAVerifyCode,
			testkit.JSON(http.StatusOK, testkit.LoginInvalidCredentialsJSON()))

	h := newHarness(t, script)
	capability := startMFA(t, h)

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, "000000")
	if err == nil {
		t.Fatal("a wrong code was accepted")
	}
	if !errors.Is(err, protocol.ErrMFARejected) {
		t.Fatalf("err = %v, want errors.Is match on protocol.ErrMFARejected", err)
	}
	if errors.Is(err, protocol.ErrInvalidCredentials) {
		t.Fatalf("err = %v must not also match protocol.ErrInvalidCredentials: "+
			"a wrong OTP is not a wrong password", err)
	}
	if h.registry.Len() != 1 {
		t.Fatalf("the transaction was destroyed by a wrong code: Len() = %d", h.registry.Len())
	}

	if _, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode); err != nil {
		t.Fatalf("retry with the right code: %v", err)
	}
}

// A rejected OTP on the widget flow is classified the same way: OutcomeMFARejected,
// never OutcomeInvalidCredentials, and the transaction survives for a retry.
func TestCompleteMFAWidgetWrongCodeIsClassifiedAsRejectedNotInvalidCredentials(t *testing.T) {
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.RateLimited(30)).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.HTML(http.StatusOK, testkit.WidgetMFAHTML(testkit.WidgetTitleTOTPMFA))).
		With(protocol.PathWidgetVerifyMFA,
			testkit.HTML(http.StatusOK, testkit.WidgetErrorHTML("Invalid Code")),
			testkit.HTML(http.StatusOK, testkit.WidgetSuccessHTML(testTicket)))

	h := newHarness(t, script)

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !result.NeedsMFA() || result.Strategy() != auth.StrategyWidget {
		t.Fatalf("Login returned %v, want a widget MFA challenge", result)
	}

	_, err = h.auth.CompleteMFA(t.Context(), result.TransactionID(), testPrincipal, "000000")
	if err == nil {
		t.Fatal("a wrong widget code was accepted")
	}
	if !errors.Is(err, protocol.ErrMFARejected) {
		t.Fatalf("err = %v, want errors.Is match on protocol.ErrMFARejected", err)
	}
	if errors.Is(err, protocol.ErrInvalidCredentials) {
		t.Fatalf("err = %v must not also match protocol.ErrInvalidCredentials", err)
	}
	if h.registry.Len() != 1 {
		t.Fatalf("the transaction was destroyed by a wrong code: Len() = %d", h.registry.Len())
	}

	completed, err := h.auth.CompleteMFA(t.Context(), result.TransactionID(), testPrincipal, testMFACode)
	if err != nil {
		t.Fatalf("retry with the right code: %v", err)
	}
	if completed.State() != auth.StateAuthenticated {
		t.Fatalf("State() = %s, want authenticated", completed.State())
	}
}

// The attempt budget, not the outcome, bounds retries: a transaction whose
// attempts are exhausted reports ErrTransactionAttemptsExhausted even though every
// wrong submission before it was a plain rejected OTP.
func TestCompleteMFAWrongCodeExhaustsAttemptBudget(t *testing.T) {
	rejectBehavior := testkit.JSON(http.StatusOK, testkit.LoginInvalidCredentialsJSON())
	// A single-behavior queue repeats indefinitely once drained, so one scripted
	// rejection per endpoint covers every attempt.
	script := mobileMFAScript().
		With(protocol.PathMobileMFAVerifyCode, rejectBehavior).
		With(protocol.PathPortalMFAVerifyCode, rejectBehavior)
	h := newHarness(t, script)
	capability := startMFA(t, h)

	var lastErr error
	for range auth.DefaultMaxAttempts {
		_, lastErr = h.auth.CompleteMFA(t.Context(), capability, testPrincipal, "000000")
		if lastErr == nil {
			t.Fatal("a wrong code was accepted")
		}
	}
	if !errors.Is(lastErr, protocol.ErrMFARejected) {
		t.Fatalf("last attempt err = %v, want errors.Is match on protocol.ErrMFARejected", lastErr)
	}

	// One more attempt spends past the budget: the registry itself refuses before
	// the code is even submitted, and the transaction is gone.
	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, "000000")
	if !errors.Is(err, auth.ErrTransactionAttemptsExhausted) {
		t.Fatalf("err = %v, want ErrTransactionAttemptsExhausted", err)
	}
	if h.registry.Len() != 0 {
		t.Fatalf("the exhausted transaction survived: Len() = %d", h.registry.Len())
	}
}

// Concurrent wrong-code submissions must not corrupt the attempt budget or
// destroy the transaction: each is a plain rejection, and the transaction stays
// usable afterward for a correct retry. Run under -race.
func TestConcurrentWrongCodeSubmissionsLeaveTransactionRetryable(t *testing.T) {
	const callers = 3
	// A single-behavior queue repeats indefinitely, so every concurrent call gets
	// rejected regardless of how many of them actually reach the network (some may
	// instead lose the single-completion race with ErrCompletionInFlight).
	rejectBehavior := testkit.JSON(http.StatusOK, testkit.LoginInvalidCredentialsJSON())
	script := mobileMFAScript().
		With(protocol.PathMobileMFAVerifyCode, rejectBehavior).
		With(protocol.PathPortalMFAVerifyCode, rejectBehavior)
	h := newHarness(t, script)
	capability := startMFA(t, h)

	var wg sync.WaitGroup
	errs := make([]error, callers)
	wg.Add(callers)
	for i := range callers {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = h.auth.CompleteMFA(t.Context(), capability, testPrincipal, "000000")
		}(i)
	}
	wg.Wait()

	// Each caller either lost the single-completion race (ErrCompletionInFlight,
	// charged nothing) or reached the verify endpoint and had its wrong code
	// rejected (ErrMFARejected, never ErrInvalidCredentials). Neither ever
	// succeeds, and neither destroys the transaction.
	for i, err := range errs {
		if err == nil {
			t.Fatalf("caller %d: a wrong code was accepted", i)
		}
		if !errors.Is(err, protocol.ErrMFARejected) && !errors.Is(err, auth.ErrCompletionInFlight) {
			t.Errorf("caller %d: err = %v, want ErrMFARejected or ErrCompletionInFlight", i, err)
		}
		if errors.Is(err, protocol.ErrInvalidCredentials) {
			t.Errorf("caller %d: err = %v must not match protocol.ErrInvalidCredentials", i, err)
		}
	}

	// The concurrent rejections spent at most `callers` of the attempt budget and
	// never the completion lease itself, so the transaction is still resident and
	// still addressable by the same capability.
	if got := h.registry.Len(); got != 1 {
		t.Fatalf("registry holds %d transactions, want 1: concurrent rejections must not destroy it", got)
	}
}

// TestCompleteMFAStopsAtADefinitiveRejectionWithoutConsultingTheAlternateEndpoint
// scripts the two verify endpoints with genuinely different responses, which every
// fixture above this test did not: both scripted the same behavior on both
// endpoints, which hides a bug where the loop kept the LAST endpoint's verdict
// rather than the most definitive one. Here the flow's own endpoint gives a
// definitive rejection and the alternate would report a rate limit if asked. The
// alternate must never be asked at all: Garmin's own OTP attempt counter would
// otherwise be spent twice for one local attempt, and the caller must still see
// the rejection rather than a rate-limit or unknown verdict from a response that
// was never fetched.
func TestCompleteMFAStopsAtADefinitiveRejectionWithoutConsultingTheAlternateEndpoint(t *testing.T) {
	script := mobileMFAScript().
		With(protocol.PathMobileMFAVerifyCode, testkit.JSON(http.StatusOK, testkit.LoginInvalidCredentialsJSON())).
		With(protocol.PathPortalMFAVerifyCode, testkit.RateLimited(30))

	h := newHarness(t, script)
	capability := startMFA(t, h)

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, "000000")
	if !errors.Is(err, protocol.ErrMFARejected) {
		t.Fatalf("err = %v, want errors.Is match on protocol.ErrMFARejected", err)
	}
	if errors.Is(err, protocol.ErrRateLimited) {
		t.Fatalf("err = %v must not match ErrRateLimited: an inconclusive response "+
			"from an endpoint that was never consulted overwrote a definitive rejection", err)
	}
	if h.requestCount(protocol.PathPortalMFAVerifyCode) != 0 {
		t.Fatalf("the alternate endpoint was consulted after a definitive rejection: %v", h.paths())
	}
}

// TestCompleteMFAStopsAtAnAccountLockoutWithoutConsultingTheAlternateEndpoint is
// the same property for a lockout: it is definitive for the account, so the loop
// must stop even though the alternate endpoint here would otherwise report
// success.
func TestCompleteMFAStopsAtAnAccountLockoutWithoutConsultingTheAlternateEndpoint(t *testing.T) {
	script := mobileMFAScript().
		With(protocol.PathMobileMFAVerifyCode, testkit.JSON(http.StatusOK, testkit.LoginAccountLockedJSON())).
		With(protocol.PathPortalMFAVerifyCode, testkit.JSON(http.StatusOK, testkit.LoginSuccessJSON(testTicket)))

	h := newHarness(t, script)
	capability := startMFA(t, h)

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, "000000")
	if !errors.Is(err, protocol.ErrAccountLocked) {
		t.Fatalf("err = %v, want errors.Is match on protocol.ErrAccountLocked", err)
	}
	if h.requestCount(protocol.PathPortalMFAVerifyCode) != 0 {
		t.Fatalf("the alternate endpoint was consulted after an account lockout: %v", h.paths())
	}
}

func TestCompleteMFAWidgetFlow(t *testing.T) {
	script := widgetPages(baseScript()).
		With(protocol.PathMobileLogin, testkit.RateLimited(30)).
		With(protocol.PathWidgetSignIn,
			testkit.HTML(http.StatusOK, testkit.WidgetSignInPageHTML(testCSRF)),
			testkit.HTML(http.StatusOK, testkit.WidgetMFAHTML(testkit.WidgetTitleTOTPMFA))).
		With(protocol.PathWidgetVerifyMFA, testkit.HTML(http.StatusOK, testkit.WidgetSuccessHTML(testTicket)))

	h := newHarness(t, script)

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !result.NeedsMFA() || result.Strategy() != auth.StrategyWidget {
		t.Fatalf("Login returned %v, want a widget MFA challenge", result)
	}

	completed, err := h.auth.CompleteMFA(t.Context(), result.TransactionID(), testPrincipal, testMFACode)
	if err != nil {
		t.Fatalf("CompleteMFA: %v", err)
	}
	if completed.State() != auth.StateAuthenticated {
		t.Fatalf("State() = %s, want authenticated", completed.State())
	}
	if h.requestCount(protocol.PathWidgetVerifyMFA) != 1 {
		t.Errorf("the widget verify endpoint was not used once: %v", h.paths())
	}
	h.assertNoCredentialsInQueries()
}

func TestCompleteMFAErrorsCarryNoSecrets(t *testing.T) {
	leaky := `{"mfaVerificationCode":"` + testMFACode + `","password":"` + testPassword + `"}`
	script := mobileMFAScript().
		With(protocol.PathMobileMFAVerifyCode, testkit.JSON(http.StatusInternalServerError, leaky)).
		With(protocol.PathPortalMFAVerifyCode, testkit.JSON(http.StatusInternalServerError, leaky))

	h := newHarness(t, script)
	capability := startMFA(t, h)

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode)
	if err == nil {
		t.Fatal("CompleteMFA succeeded against a failing verify endpoint")
	}
	for _, bad := range []string{testMFACode, testPassword, capability, leaky} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("error %q leaked %q", err, bad)
		}
	}
}

// A rejected OTP is the new path this package adds (OutcomeMFARejected). Its
// error must carry the same guarantees as every other MFA failure: no wrong
// code, no cookie, no capability, and no response body, even though the
// response here is a definitive JSON verdict rather than a 500.
func TestCompleteMFARejectedCodeErrorsCarryNoSecrets(t *testing.T) {
	const wrongCode = "654321"
	leaky := `{"responseStatus":{"type":"INVALID_USERNAME_PASSWORD"},` +
		`"mfaVerificationCode":"` + wrongCode + `","password":"` + testPassword + `"}`
	script := mobileMFAScript().
		With(protocol.PathMobileMFAVerifyCode, testkit.JSON(http.StatusOK, leaky)).
		With(protocol.PathPortalMFAVerifyCode, testkit.JSON(http.StatusOK, leaky))

	h := newHarness(t, script)
	capability := startMFA(t, h)

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, wrongCode)
	if err == nil {
		t.Fatal("a wrong code was accepted")
	}
	if !errors.Is(err, protocol.ErrMFARejected) {
		t.Fatalf("err = %v, want errors.Is match on protocol.ErrMFARejected", err)
	}

	msg := err.Error()
	for _, bad := range []string{wrongCode, testPassword, capability, leaky} {
		if strings.Contains(msg, bad) {
			t.Fatalf("error %q leaked %q", msg, bad)
		}
	}
}

// TestInterleavedMFALoginsStayIsolated drives two logins through the same
// Authenticator at once and proves neither can complete with the other's
// capability. Run under -race.
func TestInterleavedMFALoginsStayIsolated(t *testing.T) {
	h := newHarness(t, mobileMFAScript())

	const principalB = "principal-fake-b"
	capabilities := make(map[string]string, 2)
	for _, principal := range []string{testPrincipal, principalB} {
		result, err := h.auth.Login(t.Context(), principal, auth.NewCredentials(testEmail, testPassword))
		if err != nil {
			t.Fatalf("Login %s: %v", principal, err)
		}
		capabilities[principal] = result.TransactionID()
	}

	if capabilities[testPrincipal] == capabilities[principalB] {
		t.Fatal("two logins share one capability")
	}
	// Neither principal may use the other's transaction.
	_, err := h.auth.CompleteMFA(t.Context(), capabilities[principalB], testPrincipal, testMFACode)
	if !errors.Is(err, auth.ErrTransactionPrincipalMismatch) {
		t.Fatalf("cross-transaction completion: err = %v, want ErrTransactionPrincipalMismatch", err)
	}

	var wg sync.WaitGroup
	wg.Add(len(capabilities))
	for principal, capability := range capabilities {
		go func(principal, capability string) {
			defer wg.Done()
			if _, err := h.auth.CompleteMFA(t.Context(), capability, principal, testMFACode); err != nil {
				t.Errorf("CompleteMFA %s: %v", principal, err)
			}
		}(principal, capability)
	}
	wg.Wait()

	for _, principal := range []string{testPrincipal, principalB} {
		set, version, ok := h.store.get(principal)
		if !ok || version != 1 {
			t.Errorf("%s: stored version = %d, ok = %v, want 1, true", principal, version, ok)
			continue
		}
		if set.Token() != testAccessToken {
			t.Errorf("%s: stored the wrong token", principal)
		}
	}
	if h.registry.Len() != 0 {
		t.Errorf("registry holds %d transactions, want 0", h.registry.Len())
	}
}

// A concurrent double submission of one capability must be won by exactly one
// caller: the terminal transition is single-use. Run under -race.
func TestConcurrentCompleteMFAIsSingleUse(t *testing.T) {
	h := newHarness(t, mobileMFAScript())
	capability := startMFA(t, h)

	const callers = 4
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)
	wg.Add(callers)

	for range callers {
		go func() {
			defer wg.Done()
			if _, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("%d callers completed the same transaction, want exactly 1", succeeded)
	}
	if _, _, ok := h.store.get(testPrincipal); !ok {
		t.Fatal("no token set was stored")
	}
	if h.registry.Len() != 0 {
		t.Fatalf("registry holds %d transactions, want 0", h.registry.Len())
	}
}
