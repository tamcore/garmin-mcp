//go:build fakegarmin

package auth_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

const (
	rotatedToken   = "di-token-rotated-0800"
	rotatedRefresh = "di-refresh-rotated-0801"
)

// rotatedSet is the set a concurrent refresher stored while the login was still
// exchanging its ticket.
func rotatedSet() auth.TokenSet {
	return auth.NewTokenSet(rotatedToken, rotatedRefresh, testClientID, fakeStart().Add(time.Hour))
}

// A token rotated while the login was producing its candidate must survive. The
// compare-and-set baseline is read before the exchange, so the login loses the
// race it actually lost instead of overwriting the newer refresh token.
func TestLoginDoesNotClobberATokenRotatedDuringTheExchange(t *testing.T) {
	gate := newPathGate(protocol.PathDIToken)
	h := newHarnessWithTransport(t, mobileSuccessScript(), gate.wrap)

	// The refresher rotates and stores a new set while the exchange is in flight.
	gate.before = func() { h.store.bump(testPrincipal, rotatedSet()) }
	close(gate.release)

	_, err := h.login()
	if !errors.Is(err, auth.ErrVersionConflict) {
		t.Fatalf("err = %v, want a reported version conflict", err)
	}

	stored, _, ok := h.store.get(testPrincipal)
	if !ok {
		t.Fatal("the stored set disappeared")
	}
	if stored.Token() != rotatedToken {
		t.Errorf("stored token = %q, want the rotated token kept", stored.Token())
	}
	if got := h.store.saveCount(); got != 1 {
		t.Errorf("%d saves, want exactly one refused attempt", got)
	}
}

// A conflict must not be answered by re-reading the version and rewriting the same
// stale candidate: that turns a detected lost update into a silent one.
func TestLoginDoesNotRetryAStaleCandidateAfterAConflict(t *testing.T) {
	h := newHarness(t, mobileSuccessScript())

	var once sync.Once
	h.store.beforeSave = func(principal string) {
		once.Do(func() { h.store.bump(principal, rotatedSet()) })
	}

	if _, err := h.login(); !errors.Is(err, auth.ErrVersionConflict) {
		t.Fatalf("err = %v, want a reported version conflict", err)
	}
	if got := h.store.saveCount(); got != 1 {
		t.Errorf("%d saves, want exactly one: the stale candidate was retried", got)
	}

	stored, _, _ := h.store.get(testPrincipal)
	if stored.Token() != rotatedToken {
		t.Errorf("stored token = %q, want the rotated token kept", stored.Token())
	}
}
