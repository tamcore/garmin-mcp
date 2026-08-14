package auth_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const (
	leakCSRF   = "csrf-secret-0400"
	leakCookie = "cookie-secret-0401"
	// principalA is the account most registry tests run as.
	principalA = "principal-a"
)

func registryStart() time.Time { return time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC) }

func newTestRegistry(t *testing.T, clock *testkit.FakeClock, cfg auth.RegistryConfig) *auth.Registry {
	t.Helper()

	cfg.Clock = clock
	registry, err := auth.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func pendingFor(principal, csrf, cookieValue string) auth.Pending {
	return auth.NewPending(auth.PendingParams{
		Principal:  principal,
		Strategy:   auth.StrategyMobileIOS,
		MFAMethod:  protocol.MFAMethodEmail,
		CSRFToken:  csrf,
		Cookies:    []*http.Cookie{{Name: "GARMIN-SSO-GUID", Value: cookieValue}},
		Query:      url.Values{"clientId": {protocol.ClientIDIOS}},
		ServiceURL: "https://sso.example.invalid/gcm/ios",
	})
}

func TestRegistryCreateAndAttempt(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(id) < 40 {
		t.Fatalf("capability %q is too short to carry 256 bits", id)
	}
	if registry.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", registry.Len())
	}

	attempt, err := registry.Attempt(id, principalA)
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	pending := attempt.Pending()
	if pending.CSRFToken() != leakCSRF {
		t.Errorf("CSRFToken() = %q", pending.CSRFToken())
	}
	if pending.Strategy() != auth.StrategyMobileIOS {
		t.Errorf("Strategy() = %q", pending.Strategy())
	}
	if pending.MFAMethod() != protocol.MFAMethodEmail {
		t.Errorf("MFAMethod() = %q", pending.MFAMethod())
	}
	if got := pending.Cookies(); len(got) != 1 || got[0].Value != leakCookie {
		t.Errorf("Cookies() = %v", got)
	}
	if pending.State() != auth.StateMFAPending {
		t.Errorf("State() = %s, want mfa_pending", pending.State())
	}
}

func TestRegistryRejectsUnknownAndTamperedCapability(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for name, candidate := range map[string]string{
		"empty":     "",
		"unknown":   "not-a-capability",
		"truncated": id[:len(id)-1],
		"prefixed":  "x" + id,
	} {
		if _, err := registry.Attempt(candidate, principalA); !errors.Is(err, auth.ErrUnknownTransaction) {
			t.Errorf("%s capability: err = %v, want ErrUnknownTransaction", name, err)
		}
	}
}

func TestRegistryRejectsCrossPrincipalAttempt(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = registry.Attempt(id, "principal-b")
	if !errors.Is(err, auth.ErrTransactionPrincipalMismatch) {
		t.Fatalf("err = %v, want ErrTransactionPrincipalMismatch", err)
	}
	if strings.Contains(fmt.Sprint(err), leakCSRF) || strings.Contains(fmt.Sprint(err), leakCookie) {
		t.Fatalf("error leaked pending material: %v", err)
	}
}

func TestRegistryRejectsExpiredTransaction(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{TTL: 2 * time.Minute})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	clock.Advance(2 * time.Minute)

	if _, err := registry.Attempt(id, principalA); !errors.Is(err, auth.ErrTransactionExpired) {
		t.Fatalf("err = %v, want ErrTransactionExpired", err)
	}
	if registry.Len() != 0 {
		t.Fatalf("expired transaction survived: Len() = %d", registry.Len())
	}
	// A second use of an expired capability must not reveal that it once existed.
	if _, err := registry.Attempt(id, principalA); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("err = %v, want ErrUnknownTransaction", err)
	}
}

func TestRegistryBoundsAttempts(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxAttempts: 2})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := range 2 {
		attempt, err := registry.Attempt(id, principalA)
		if err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
		attempt.Release()
	}

	if _, err := registry.Attempt(id, principalA); !errors.Is(err, auth.ErrTransactionAttemptsExhausted) {
		t.Fatalf("err = %v, want ErrTransactionAttemptsExhausted", err)
	}
	if registry.Len() != 0 {
		t.Fatalf("exhausted transaction survived: Len() = %d", registry.Len())
	}
}

// A cross-principal probe burns an attempt, so a capability cannot be used as an
// unbounded principal oracle.
func TestRegistryCrossPrincipalAttemptCountsAgainstTheBudget(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxAttempts: 1})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := registry.Attempt(id, "principal-b"); !errors.Is(err, auth.ErrTransactionPrincipalMismatch) {
		t.Fatalf("err = %v, want ErrTransactionPrincipalMismatch", err)
	}
	if _, err := registry.Attempt(id, principalA); !errors.Is(err, auth.ErrTransactionAttemptsExhausted) {
		t.Fatalf("err = %v, want ErrTransactionAttemptsExhausted", err)
	}
}

func TestRegistryClaimIsSingleUse(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	attempt, err := registry.Attempt(id, principalA)
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}

	if err := attempt.Claim(); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if registry.Len() != 0 {
		t.Fatalf("completed transaction survived: Len() = %d", registry.Len())
	}
	if err := attempt.Claim(); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("replayed Claim: err = %v, want ErrUnknownTransaction", err)
	}
	if _, err := registry.Attempt(id, principalA); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("replayed Attempt: err = %v, want ErrUnknownTransaction", err)
	}
}

// The terminal success may only follow a code submission. That ordering is now
// structural: it can only be reached through the Attempt that charged the budget,
// and a released lease cannot claim anything.
func TestRegistryReleasedAttemptCannotClaim(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	attempt, err := registry.Attempt(id, principalA)
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}

	attempt.Release()

	if err := attempt.Claim(); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("err = %v, want ErrUnknownTransaction", err)
	}
	if registry.Len() != 1 {
		t.Fatalf("a released attempt destroyed the transaction: Len() = %d", registry.Len())
	}
}

func TestRegistryCancel(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := registry.Cancel(id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if registry.Len() != 0 {
		t.Fatalf("cancelled transaction survived: Len() = %d", registry.Len())
	}
	if _, err := registry.Attempt(id, principalA); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("err = %v, want ErrUnknownTransaction", err)
	}
	if err := registry.Cancel(id); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("second Cancel: err = %v, want ErrUnknownTransaction", err)
	}
}

// Fail is the abandon path for a login the server gives up on, as opposed to
// Cancel, which is the caller's own abandonment.
func TestRegistryFailRemovesTheTransaction(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := registry.Fail(id); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if registry.Len() != 0 {
		t.Fatalf("a failed transaction survived: Len() = %d", registry.Len())
	}
	if err := registry.Fail(id); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("second Fail: err = %v, want ErrUnknownTransaction", err)
	}
}

func TestRegistryIsBoundedAndSweepsExpiredEntries(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxEntries: 2, TTL: time.Minute})

	for i := range 3 {
		if _, err := registry.Create(pendingFor(fmt.Sprintf("principal-%d", i), leakCSRF, leakCookie)); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	// The bound holds: the third start evicted the first rather than being refused.
	if registry.Len() != 2 {
		t.Fatalf("Len() = %d, want the configured bound of 2", registry.Len())
	}

	// Once the live entries age out, the space must come back.
	clock.Advance(time.Minute)
	if _, err := registry.Create(pendingFor("principal-late", leakCSRF, leakCookie)); err != nil {
		t.Fatalf("Create after sweep: %v", err)
	}
	if registry.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 after the sweep", registry.Len())
	}
}

func TestRegistryCapabilitiesAreUnique(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxEntries: 64})

	seen := make(map[string]bool, 32)
	for i := range 32 {
		id, err := registry.Create(pendingFor(fmt.Sprintf("principal-%d", i), leakCSRF, leakCookie))
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("capability %d repeated", i)
		}
		seen[id] = true
	}
}

func TestPendingRenderingIsRedacted(t *testing.T) {
	pending := pendingFor(principalA, leakCSRF, leakCookie)

	for form, rendered := range map[string]string{
		formString:   pending.String(),
		formGoString: pending.GoString(),
		formV:        fmt.Sprintf("%v", pending),
		formPlusV:    fmt.Sprintf("%+v", pending),
		formHashV:    fmt.Sprintf("%#v", pending),
		formSlog:     logLine(t, "pending", pending),
	} {
		for _, bad := range []string{leakCSRF, leakCookie} {
			if strings.Contains(rendered, bad) {
				t.Fatalf("%s rendering %q leaked %q", form, rendered, bad)
			}
		}
	}
}

func TestRegistryRejectsNonPositiveConfiguration(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())

	for name, cfg := range map[string]auth.RegistryConfig{
		"negative TTL":           {TTL: -time.Second},
		"negative attempts":      {MaxAttempts: -1},
		"negative max entries":   {MaxEntries: -1},
		"negative pending bytes": {MaxPendingBytes: -1},
	} {
		cfg.Clock = clock
		if _, err := auth.NewRegistry(cfg); err == nil {
			t.Errorf("%s: NewRegistry accepted the configuration", name)
		}
	}
}
