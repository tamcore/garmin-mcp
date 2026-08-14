package auth_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// A capability is a 256-bit value in canonical base64url without padding, which is
// exactly 43 characters. Anything else is refused before it is hashed, so an
// arbitrarily long or differently encoded string never reaches SHA-256.
func TestRegistryRequiresACanonicalCapabilityEncoding(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(id) != 43 {
		t.Fatalf("capability length = %d, want 43 characters of base64url", len(id))
	}

	candidates := map[string]string{
		"empty":           "",
		"short":           id[:42],
		"long":            id + "A",
		"padded":          id[:40] + "A==",
		"standard base64": strings.Repeat("+", 43),
		"not base64":      id[:42] + "?",
		"absurdly large":  strings.Repeat("A", 1<<20),
	}
	for name, candidate := range candidates {
		_, err := registry.Attempt(candidate, principalA)
		if !errors.Is(err, auth.ErrUnknownTransaction) {
			t.Errorf("%s: err = %v, want ErrUnknownTransaction", name, err)
		}
		if !errors.Is(err, auth.ErrMalformedCapability) {
			t.Errorf("%s: err = %v, want ErrMalformedCapability", name, err)
		}
	}
}

// Capacity pressure must not deny service to every new login. An abandoned start
// is evicted so a fresh login gets a slot.
func TestRegistryEvictsAbandonedStartsUnderCapacityPressure(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxEntries: 2, TTL: time.Hour})

	abandoned, err := registry.Create(pendingFor("principal-abandoned", leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create abandoned: %v", err)
	}
	clock.Advance(time.Second)
	active, err := registry.Create(pendingFor("principal-active", leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create active: %v", err)
	}
	// The second transaction is in use: a code was submitted for it.
	attempt, err := registry.Attempt(active, "principal-active")
	if err != nil {
		t.Fatalf("Attempt active: %v", err)
	}
	attempt.Release()

	fresh, err := registry.Create(pendingFor("principal-fresh", leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create under pressure: %v", err)
	}
	if registry.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", registry.Len())
	}
	if _, err := registry.Attempt(abandoned, "principal-abandoned"); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Errorf("the abandoned start survived: err = %v", err)
	}
	if _, err := registry.Attempt(active, "principal-active"); err != nil {
		t.Errorf("the in-use transaction was evicted: %v", err)
	}
	if _, err := registry.Attempt(fresh, "principal-fresh"); err != nil {
		t.Errorf("the fresh transaction is not usable: %v", err)
	}
}

// Only when every resident transaction is being completed is there nothing fair to
// evict, and only then is a new login refused.
func TestRegistryIsFullOnlyWhenEveryTransactionIsBeingCompleted(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxEntries: 1})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := registry.Attempt(id, principalA); err != nil {
		t.Fatalf("Attempt: %v", err)
	}

	_, err = registry.Create(pendingFor("principal-b", leakCSRF, leakCookie))
	if !errors.Is(err, auth.ErrRegistryFull) {
		t.Fatalf("err = %v, want ErrRegistryFull", err)
	}
}

// Pending bytes are bounded, so a hostile or broken SSO response cannot park
// megabytes of cookies per login.
func TestRegistryRejectsOversizedPending(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{})

	huge := auth.NewPending(auth.PendingParams{
		Principal: principalA,
		Strategy:  auth.StrategyMobileIOS,
		MFAMethod: protocol.MFAMethodEmail,
		Cookies:   []*http.Cookie{{Name: "GARMIN-SSO", Value: strings.Repeat("x", 1<<20)}},
		Query:     url.Values{"clientId": {protocol.ClientIDIOS}},
	})

	if _, err := registry.Create(huge); !errors.Is(err, auth.ErrPendingTooLarge) {
		t.Fatalf("err = %v, want ErrPendingTooLarge", err)
	}
	if registry.Len() != 0 {
		t.Fatalf("an oversized transaction was stored: Len() = %d", registry.Len())
	}
}

// The completion lease is exclusive and single-use: a second concurrent completion
// is refused while one is in flight, a released lease can be taken again, and a
// claimed transaction is gone.
func TestRegistryCompletionLeaseIsExclusive(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxAttempts: 8})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	held, err := registry.Attempt(id, principalA)
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	if _, err := registry.Attempt(id, principalA); !errors.Is(err, auth.ErrCompletionInFlight) {
		t.Fatalf("err = %v, want ErrCompletionInFlight", err)
	}

	held.Release()
	again, err := registry.Attempt(id, principalA)
	if err != nil {
		t.Fatalf("Attempt after Release: %v", err)
	}
	if got := again.Pending().CSRFToken(); got != leakCSRF {
		t.Errorf("Pending().CSRFToken() = %q", got)
	}

	if err := again.Claim(); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if registry.Len() != 0 {
		t.Fatalf("a claimed transaction survived: Len() = %d", registry.Len())
	}
	if err := again.Claim(); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("second Claim: err = %v, want ErrUnknownTransaction", err)
	}
	if _, err := registry.Attempt(id, principalA); !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("Attempt after Claim: err = %v, want ErrUnknownTransaction", err)
	}
}

// Claim re-checks the absolute TTL, because the verification it follows is a
// network call that can outlive the transaction.
func TestRegistryClaimRejectsATransactionThatExpiredMeanwhile(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{TTL: time.Minute})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	attempt, err := registry.Attempt(id, principalA)
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}

	clock.Advance(time.Minute)

	err = attempt.Claim()
	if !errors.Is(err, auth.ErrTransactionExpired) {
		t.Fatalf("err = %v, want ErrTransactionExpired", err)
	}
	if registry.Len() != 0 {
		t.Fatalf("the expired transaction survived: Len() = %d", registry.Len())
	}
	if strings.Contains(err.Error(), leakCSRF) || strings.Contains(err.Error(), leakCookie) {
		t.Fatal("the error leaked pending material")
	}
}
