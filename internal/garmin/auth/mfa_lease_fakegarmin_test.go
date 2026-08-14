//go:build fakegarmin

package auth_test

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// pathGate holds the first request to one path open until the test releases it,
// which places a second caller inside a chosen window deterministically. Every
// later request to that path passes straight through, so a caller that must be
// refused still reaches the transport when the code is wrong.
type pathGate struct {
	inner   auth.Doer
	path    string
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
	before  func()
}

func newPathGate(path string) *pathGate {
	return &pathGate{
		path:    path,
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *pathGate) wrap(inner auth.Doer) auth.Doer {
	g.inner = inner
	return g
}

func (g *pathGate) Do(req *http.Request) (*http.Response, error) {
	if req.URL.Path == g.path {
		first := false
		g.once.Do(func() { first = true })
		if first {
			if g.before != nil {
				g.before()
			}
			close(g.arrived)
			<-g.release
		}
	}
	return g.inner.Do(req)
}

// Exactly one completion may be in flight per transaction. Two concurrent
// submissions of one capability must not both verify the OTP, exchange a ticket
// and save a token set, because only one of them can win the terminal transition
// afterwards.
func TestCompleteMFAAdmitsOneInFlightCompletionPerTransaction(t *testing.T) {
	gate := newPathGate(protocol.PathMobileMFAVerifyCode)
	h := newHarnessWithTransport(t, mobileMFAScript(), gate.wrap)
	capability := startMFA(t, h)

	first := make(chan error, 1)
	go func() {
		_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode)
		first <- err
	}()
	// The first completion now holds the transaction and sits in the verify call.
	<-gate.arrived

	if _, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode); err == nil {
		t.Error("a second concurrent completion of one capability was admitted")
	}

	close(gate.release)
	if err := <-first; err != nil {
		t.Fatalf("first CompleteMFA: %v", err)
	}

	if got := h.requestCount(protocol.PathMobileMFAVerifyCode); got != 1 {
		t.Errorf("%d verify calls, want exactly 1: the refused completion performed external effects", got)
	}
	if got := h.store.saveCount(); got != 1 {
		t.Errorf("%d saves, want exactly 1", got)
	}
	if _, version, ok := h.store.get(testPrincipal); !ok || version != 1 {
		t.Errorf("stored version = %d, ok = %v, want 1, true", version, ok)
	}
	if h.registry.Len() != 0 {
		t.Errorf("registry holds %d transactions, want 0", h.registry.Len())
	}
}

// An entry that expires while the OTP is being verified must not produce a stored
// token set: the expiry is re-checked, and the terminal success is claimed, before
// any token is exchanged or saved.
func TestCompleteMFAExpiringDuringVerificationSavesNothing(t *testing.T) {
	gate := newPathGate(protocol.PathMobileMFAVerifyCode)
	h := newHarnessWithTransport(t, mobileMFAScript(), gate.wrap)
	capability := startMFA(t, h)

	// Age the transaction past its TTL while the verify call is in progress.
	gate.before = func() { h.clock.Advance(auth.DefaultTransactionTTL) }
	close(gate.release)

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode)
	if !errors.Is(err, auth.ErrTransactionExpired) {
		t.Fatalf("err = %v, want ErrTransactionExpired", err)
	}
	if _, _, ok := h.store.get(testPrincipal); ok {
		t.Error("an expired transaction stored a token set")
	}
	if h.requestCount(protocol.PathDIToken) != 0 {
		t.Error("an expired transaction exchanged the service ticket")
	}
	if h.registry.Len() != 0 {
		t.Errorf("the expired transaction survived: Len() = %d", h.registry.Len())
	}
}

// A failed persistence leaves no usable half-completed transaction: the capability
// is consumed, so the login must restart rather than retry the OTP against a
// transaction whose tokens may or may not have been written.
func TestCompleteMFAFailedPersistenceForcesTheLoginToRestart(t *testing.T) {
	h := newHarness(t, mobileMFAScript())
	capability := startMFA(t, h)

	h.store.saveErr = errors.New("fake store: the write was refused")

	if _, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode); err == nil {
		t.Fatal("CompleteMFA succeeded although the store refused the write")
	}
	if _, _, ok := h.store.get(testPrincipal); ok {
		t.Error("a failed save stored a token set")
	}
	if h.registry.Len() != 0 {
		t.Errorf("a half-completed transaction survived: Len() = %d", h.registry.Len())
	}

	_, err := h.auth.CompleteMFA(t.Context(), capability, testPrincipal, testMFACode)
	if !errors.Is(err, auth.ErrUnknownTransaction) {
		t.Fatalf("replay after a failed save: err = %v, want ErrUnknownTransaction", err)
	}
}

// strippedResult is a method-stripping alias of auth.Result: it has none of the
// redacting methods, so fmt reflects the value instead.
type strippedResult auth.Result

// The transaction capability is a bearer credential. Stripping the redaction must
// not reveal it under any verb, including %s and %q, which take fmt's bad-verb path
// and re-print the value at depth zero.
func TestStrippedResultLeaksNoCapabilityUnderAnyVerb(t *testing.T) {
	h := newHarness(t, mobileMFAScript())

	result, err := h.login()
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	capability := result.TransactionID()
	if capability == "" {
		t.Fatal("no transaction capability was issued")
	}

	for verb, rendered := range strippedForms(strippedResult(result)) {
		if strings.Contains(rendered, capability) {
			t.Fatalf("stripped Result %s rendering %q leaked the capability", verb, rendered)
		}
	}

	// The counter-test: the normal rendering still reports the shape.
	if got := result.String(); !strings.Contains(got, "transaction:present") {
		t.Errorf("normal rendering %q does not report the pending transaction", got)
	}
}
