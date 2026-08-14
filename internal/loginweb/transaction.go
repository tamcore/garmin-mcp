package loginweb

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// capabilityBytes is the entropy behind the run cookie and the form token. 256 bits
// matches the transaction capability elsewhere in this project, and neither value is
// ever stretched or reused.
const capabilityBytes = 32

// maxAttemptsSentinel is the hard ceiling on submissions per run, independent of the
// configured budget, so a misconfiguration cannot remove the bound entirely.
const maxAttemptsSentinel = 20

// transaction is the whole server-side state of one login run.
//
// It holds no password and no one-time code: both are request-scoped arguments that
// travel from the form to the Garmin call and are dropped when it returns. What it
// does hold — the run capability, the form token, and the Garmin continuation
// capability — never reaches a page, a path, a query, or a log line.
type transaction struct {
	mu sync.Mutex

	// machine is the login state machine. Every transition goes through it, so a
	// forbidden order is refused by the code the state machine tests already
	// cover rather than by an ad-hoc check here.
	machine auth.Machine

	// capability is the per-run cookie value and csrf the form token. The form
	// token is server-generated and rotated on every accepted submission, so it is
	// independent of anything the client chose.
	capability string
	csrf       string

	// garminTxn is the continuation capability for a pending MFA login.
	garminTxn string

	// attempts counts submissions and expires is the absolute deadline. Neither
	// bound can be extended by a caller.
	attempts int
	expires  time.Time

	// mfaMethod and deliveryUncertain describe the challenge for the OTP page.
	mfaMethod         string
	deliveryUncertain bool

	// strategy is the login flow that reached the current state, and err is why a
	// run failed.
	strategy string
	err      error

	done   chan struct{}
	closed bool
}

// newTransaction builds the run state with fresh, independent secrets.
func newTransaction(now time.Time, ttl time.Duration, entropy io.Reader) (*transaction, error) {
	capability, err := randomToken(entropy)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(entropy)
	if err != nil {
		return nil, err
	}

	return &transaction{
		machine:    auth.NewMachine(),
		capability: capability,
		csrf:       csrf,
		expires:    now.Add(ttl),
		done:       make(chan struct{}),
	}, nil
}

// randomToken returns a URL-safe token with capabilityBytes of entropy.
func randomToken(entropy io.Reader) (string, error) {
	if entropy == nil {
		entropy = rand.Reader
	}

	buf := make([]byte, capabilityBytes)
	if _, err := io.ReadFull(entropy, buf); err != nil {
		return "", fmt.Errorf("loginweb: reading entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// authorized reports whether presented is the run capability. The comparison is
// constant time, because the value is a bearer credential.
func (t *transaction) authorized(presented string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return subtle.ConstantTimeCompare([]byte(presented), []byte(t.capability)) == 1
}

// formToken reports the token the next form must carry.
func (t *transaction) formToken() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.csrf
}

// state reports the current login state.
func (t *transaction) state() auth.State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.machine.State()
}

// challenge reports what the OTP page needs to say about the challenge.
func (t *transaction) challenge() (method string, uncertain bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mfaMethod, t.deliveryUncertain
}

// snapshot reports the current outcome.
func (t *transaction) snapshot() Outcome {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Outcome{
		State:     t.machine.State(),
		Strategy:  t.strategy,
		MFAMethod: t.mfaMethod,
		Err:       t.err,
	}
}

// accept consumes one submission: it verifies the form token, checks the deadline,
// the state, and the attempt budget, and rotates the token so the same form cannot
// be replayed. It reports why it refused, or nil.
func (t *transaction) accept(now time.Time, token string, expected auth.State, budget int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch {
	case t.closed:
		return errRefused
	case subtle.ConstantTimeCompare([]byte(token), []byte(t.csrf)) != 1:
		return errRefused
	case now.After(t.expires):
		return errTransactionExpired
	case t.machine.State() != expected:
		return errRefused
	case t.attempts >= min(budget, maxAttemptsSentinel):
		return errAttemptsExhausted
	}

	rotated, err := randomToken(nil)
	if err != nil {
		return err
	}
	t.attempts++
	t.csrf = rotated
	return nil
}

// challenged records an MFA challenge and moves the machine to mfa_pending.
func (t *transaction) challenged(attempt Attempt) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	next, err := t.machine.SubmitCredentials(auth.StrategyName(attempt.Strategy))
	if err != nil {
		return err
	}
	next, err = next.RequireMFA(attempt.MFAMethod)
	if err != nil {
		return err
	}

	t.machine = next
	t.garminTxn = attempt.TransactionID
	t.mfaMethod = attempt.MFAMethod
	t.deliveryUncertain = attempt.DeliveryUncertain
	t.strategy = attempt.Strategy
	return nil
}

// continuation reports the Garmin continuation capability for a pending login.
func (t *transaction) continuation() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.garminTxn
}

// authenticated records a completed login and closes the run. It covers both routes
// into the authenticated state: a login with no challenge, and a verified one-time
// code.
func (t *transaction) authenticated(attempt Attempt) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	next, err := advance(t.machine, attempt)
	if err != nil {
		return err
	}

	t.machine = next
	if attempt.Strategy != "" {
		t.strategy = attempt.Strategy
	}
	t.garminTxn = ""
	t.closeLocked()
	return nil
}

// advance moves machine into the authenticated state by the route its current state
// permits.
func advance(machine auth.Machine, attempt Attempt) (auth.Machine, error) {
	if machine.State() != auth.StateCreated {
		return machine.VerifyMFA()
	}

	submitted, err := machine.SubmitCredentials(auth.StrategyName(attempt.Strategy))
	if err != nil {
		return machine, err
	}
	return submitted.Authenticate()
}

// fail records a terminal failure. The cause is kept as the package that produced it
// wrote it: those packages sanitize their own messages, and no credential ever
// reaches one.
func (t *transaction) fail(cause error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if next, err := t.machine.Fail(); err == nil {
		t.machine = next
	}
	t.err = cause
	t.garminTxn = ""
	t.closeLocked()
}

// cancel ends the run because the caller asked it to.
func (t *transaction) cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if next, err := t.machine.Cancel(); err == nil {
		t.machine = next
	}
	t.garminTxn = ""
	t.closeLocked()
}

// expire ends the run because its absolute lifetime elapsed.
func (t *transaction) expire() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if next, err := t.machine.Expire(); err == nil {
		t.machine = next
	}
	t.garminTxn = ""
	t.closeLocked()
}

// closeLocked closes the done channel exactly once. The caller holds the mutex.
func (t *transaction) closeLocked() {
	if t.closed {
		return
	}
	t.closed = true
	close(t.done)
}
