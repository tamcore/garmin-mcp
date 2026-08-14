package loginweb

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// errTooManySessions reports a full registry. It is a refusal to start a new
// transaction, never a reason to evict a live one: evicting would let a flood of
// requests cancel other people's logins.
var errTooManySessions = errors.New("loginweb: too many concurrent login transactions")

// A remoteSession is the browser-side state of one authorization transaction.
//
// It holds no password and no one-time code: both are request-scoped arguments that
// travel from the form to the Garmin call and are dropped when it returns. What it
// does hold — the transaction capability, the form token and the Garmin continuation
// capability — never reaches a page, a path, a query or a log line.
type remoteSession struct {
	mu sync.Mutex

	// machine is the login state machine, so a forbidden order is refused by the
	// code the state machine's own tests cover rather than by an ad-hoc check.
	machine auth.Machine

	// capability addresses the transaction at the authorization server, and csrf
	// is the form token. The form token is server-generated and rotated on every
	// accepted submission, so it is independent of the client's OAuth state.
	capability string
	csrf       string

	// garminTxn is the continuation capability for a pending MFA login.
	garminTxn string

	// mfaMethod and deliveryUncertain describe the challenge for the OTP page.
	mfaMethod         string
	deliveryUncertain bool

	// attempts counts submissions and expires is the absolute deadline. Neither
	// bound can be extended by a caller.
	attempts int
	expires  time.Time

	// consumed marks a terminal transaction, which is unusable from that instant.
	consumed bool
}

// newRemoteSession builds the browser-side state for one transaction.
func newRemoteSession(
	capability string, expires time.Time, entropy io.Reader,
) (*remoteSession, error) {
	csrf, err := randomToken(entropy)
	if err != nil {
		return nil, err
	}
	return &remoteSession{
		machine:    auth.NewMachine(),
		capability: capability,
		csrf:       csrf,
		expires:    expires,
	}, nil
}

// formToken reports the token the next form must carry.
func (s *remoteSession) formToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.csrf
}

// state reports the current login state.
func (s *remoteSession) state() auth.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine.State()
}

// challenge reports what the OTP page needs to say about the challenge.
func (s *remoteSession) challenge() (method string, uncertain bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mfaMethod, s.deliveryUncertain
}

// continuation reports the Garmin continuation capability for a pending login.
func (s *remoteSession) continuation() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.garminTxn
}

// confirm consumes one navigation step: it verifies the form token, the deadline and
// the state, and rotates the token so the same page cannot be replayed. It does not
// spend the attempt budget, because nothing was guessed.
func (s *remoteSession) confirm(now time.Time, token string, expected auth.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkLocked(now, token, expected)
}

// accept consumes one credential or code submission: everything confirm checks, plus
// the attempt budget.
func (s *remoteSession) accept(
	now time.Time, token string, expected auth.State, budget int,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.attempts >= min(budget, maxAttemptsSentinel) {
		return errAttemptsExhausted
	}
	if err := s.checkLocked(now, token, expected); err != nil {
		return err
	}
	s.attempts++
	return nil
}

// checkLocked verifies the token, the deadline and the state, and rotates the token.
// The caller holds the mutex.
func (s *remoteSession) checkLocked(now time.Time, token string, expected auth.State) error {
	switch {
	case s.consumed:
		return ErrNoTransaction
	case subtle.ConstantTimeCompare([]byte(token), []byte(s.csrf)) != 1:
		return ErrNoTransaction
	case !now.Before(s.expires):
		return ErrTransactionExpired
	case s.machine.State() != expected:
		return ErrNoTransaction
	}

	rotated, err := randomToken(nil)
	if err != nil {
		return err
	}
	s.csrf = rotated
	return nil
}

// challenged records an MFA challenge and moves the machine to mfa_pending.
func (s *remoteSession) challenged(attempt Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next, err := s.machine.SubmitCredentials(auth.StrategyName(attempt.Strategy))
	if err != nil {
		return err
	}
	next, err = next.RequireMFA(attempt.MFAMethod)
	if err != nil {
		return err
	}

	s.machine = next
	s.garminTxn = attempt.TransactionID
	s.mfaMethod = attempt.MFAMethod
	s.deliveryUncertain = attempt.DeliveryUncertain
	return nil
}

// authenticated records a completed Garmin login. It covers both routes into the
// authenticated state: a login with no challenge, and a verified one-time code.
//
// The transaction is not terminal yet: consent has still to be confirmed, and the
// session stays addressable until it is.
func (s *remoteSession) authenticated(attempt Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next, err := advance(s.machine, attempt)
	if err != nil {
		return err
	}
	s.machine = next
	s.garminTxn = ""
	return nil
}

// consume makes the session unusable from this instant and drops the continuation
// capability, whether the transaction ended in consent, denial or failure.
func (s *remoteSession) consume() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.consumed = true
	s.garminTxn = ""
	s.mfaMethod = ""
}

// isExpired reports whether the session is past its absolute deadline.
func (s *remoteSession) isExpired(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !now.Before(s.expires)
}

// isConsumed reports whether the session is terminal.
func (s *remoteSession) isConsumed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumed
}

// A sessionRegistry is the bounded in-memory store of live transactions.
//
// Sessions are addressed by the digest of the capability rather than by the
// capability itself, so the keys are of no use to anything that reads the map.
type sessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]*remoteSession
	max      int
}

func newSessionRegistry(max int) *sessionRegistry {
	return &sessionRegistry{sessions: make(map[string]*remoteSession), max: max}
}

// sessionKey is the digest a session is filed under.
func sessionKey(capability string) string {
	digest := sha256.Sum256([]byte(capability))
	return hex.EncodeToString(digest[:])
}

// add files a new session, after discarding whatever has expired. A full registry is
// refused rather than made room in.
func (r *sessionRegistry) add(now time.Time, session *remoteSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.purgeLocked(now)
	if len(r.sessions) >= r.max {
		return errTooManySessions
	}
	r.sessions[sessionKey(session.capability)] = session
	return nil
}

// get returns the live session a capability addresses. An unknown capability, a
// consumed one and an expired one are all reported as errors the handlers render
// identically.
func (r *sessionRegistry) get(now time.Time, capability string) (*remoteSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := sessionKey(capability)
	session, ok := r.sessions[key]
	switch {
	case !ok:
		return nil, ErrNoTransaction
	case session.isExpired(now):
		delete(r.sessions, key)
		return nil, ErrTransactionExpired
	case session.isConsumed():
		delete(r.sessions, key)
		return nil, ErrNoTransaction
	}
	return session, nil
}

// drop removes a session immediately, which is what makes a terminal transaction
// unusable at once rather than at its expiry.
func (r *sessionRegistry) drop(capability string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.sessions, sessionKey(capability))
}

// purgeLocked discards expired and terminal sessions. The caller holds the mutex.
func (r *sessionRegistry) purgeLocked(now time.Time) {
	for key, session := range r.sessions {
		if session.isExpired(now) || session.isConsumed() {
			delete(r.sessions, key)
		}
	}
}
