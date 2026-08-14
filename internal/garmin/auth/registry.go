package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Registry defaults. They are conservative on purpose: an MFA continuation is a
// short human interaction, not a session.
const (
	// DefaultTransactionTTL is the absolute lifetime of a pending transaction.
	DefaultTransactionTTL = 5 * time.Minute
	// DefaultMaxAttempts is how many verification attempts one transaction
	// allows before it is destroyed.
	DefaultMaxAttempts = 5
	// DefaultMaxTransactions bounds how many transactions may be pending at once,
	// so a flood of login starts cannot exhaust memory.
	DefaultMaxTransactions = 1024
	// DefaultMaxPendingBytes bounds the continuation state of one transaction:
	// cookies, CSRF value, query and URLs. Garmin's SSO session is a few hundred
	// bytes, so this is generous, and it is what makes the registry's memory
	// footprint the product of two bounds rather than unbounded.
	DefaultMaxPendingBytes = 16 << 10
)

// Registry errors. Each is comparable with errors.Is and carries no transaction
// material.
var (
	// ErrUnknownTransaction reports a capability the registry does not hold. A
	// consumed, cancelled, expired, evicted, malformed or forged capability is
	// reported the same way on purpose: the registry is not an oracle for which
	// one it was.
	ErrUnknownTransaction = errors.New("garmin auth: unknown login transaction")
	// ErrTransactionExpired reports a transaction past its absolute TTL. It is
	// reported once, when the expiry is noticed, and the transaction is destroyed.
	ErrTransactionExpired = errors.New("garmin auth: login transaction expired")
	// ErrTransactionAttemptsExhausted reports a transaction that used its
	// verification budget. The transaction is destroyed.
	ErrTransactionAttemptsExhausted = errors.New("garmin auth: login transaction attempts exhausted")
	// ErrTransactionPrincipalMismatch reports a capability presented for another
	// principal. The attempt still counts against the budget.
	ErrTransactionPrincipalMismatch = errors.New("garmin auth: login transaction belongs to another principal")
	// ErrTransactionOutOfOrder reports a transition that does not follow the
	// state machine.
	ErrTransactionOutOfOrder = errors.New("garmin auth: login transaction transition out of order")
	// ErrCompletionInFlight reports a second completion of a transaction that is
	// already being completed. Exactly one completion may run at a time, because a
	// completion performs external effects that only one of them could keep.
	ErrCompletionInFlight = errors.New("garmin auth: login transaction is already being completed")
	// ErrPendingTooLarge reports continuation state over the configured byte
	// bound.
	ErrPendingTooLarge = errors.New("garmin auth: login transaction state exceeds the size bound")
	// ErrRegistryFull reports that the bounded registry has no room and nothing
	// may be evicted, because every resident transaction is being completed.
	ErrRegistryFull = errors.New("garmin auth: login transaction registry full")
	// errNegativeBound is the cause of a rejected RegistryConfig.
	errNegativeBound = errors.New("negative bound")
)

// RegistryConfig configures a Registry. Every zero field takes its documented
// default; a negative field is a programming error and is rejected.
type RegistryConfig struct {
	// TTL is the absolute transaction lifetime. Zero means DefaultTransactionTTL.
	TTL time.Duration
	// MaxAttempts bounds verification attempts. Zero means DefaultMaxAttempts.
	MaxAttempts int
	// MaxEntries bounds live transactions. Zero means DefaultMaxTransactions.
	MaxEntries int
	// MaxPendingBytes bounds one transaction's continuation state. Zero means
	// DefaultMaxPendingBytes.
	MaxPendingBytes int
	// Clock is the time source. Nil means the system clock.
	Clock Clock
	// Rand is the entropy source for capabilities. Nil means crypto/rand.
	Rand io.Reader
}

// withDefaults returns cfg with every zero field filled in, or an error for a
// negative bound.
func (c RegistryConfig) withDefaults() (RegistryConfig, error) {
	if c.TTL < 0 || c.MaxAttempts < 0 || c.MaxEntries < 0 || c.MaxPendingBytes < 0 {
		return RegistryConfig{}, fmt.Errorf("garmin auth: registry config: %w", errNegativeBound)
	}

	out := c
	if out.TTL == 0 {
		out.TTL = DefaultTransactionTTL
	}
	if out.MaxAttempts == 0 {
		out.MaxAttempts = DefaultMaxAttempts
	}
	if out.MaxEntries == 0 {
		out.MaxEntries = DefaultMaxTransactions
	}
	if out.MaxPendingBytes == 0 {
		out.MaxPendingBytes = DefaultMaxPendingBytes
	}
	if out.Clock == nil {
		out.Clock = systemClock{}
	}
	if out.Rand == nil {
		out.Rand = rand.Reader
	}
	return out, nil
}

// transaction is one live entry. It is only ever reached under the registry's
// mutex, and the Pending it holds is immutable.
type transaction struct {
	// digest is the SHA-256 of the capability. The capability itself is never
	// stored.
	digest  []byte
	pending Pending
	// createdAt and expiresAt bound the entry; createdAt orders eviction.
	createdAt time.Time
	expiresAt time.Time
	attempts  int
	// attempted records that at least one verification attempt was accepted, so
	// eviction can prefer a start nobody is using.
	attempted bool
	// completing is the single-completion lease: while it is held, no second
	// completion may verify a code or produce tokens for this entry.
	completing bool
}

// Registry is the bounded, in-memory store of pending MFA logins.
//
// Interleaved logins cannot see each other's state by construction: every login
// gets its own entry under its own capability, entries are keyed by the hash of
// that capability, and a lookup returns an immutable Pending snapshot rather than
// a shared pointer. There is no per-client "current login" field for a second
// login to overwrite, which is the upstream 0.3.10 bug this design removes.
//
// A completion is leased: Attempt hands out an *Attempt that holds the entry's
// single completion lease, and only the lease holder may Claim the terminal
// success. That makes the OTP verification, the token exchange and the save one
// serialized sequence per transaction instead of a race several callers can enter.
//
// The capability itself is never stored: the map key is the hex of its SHA-256, the
// entry keeps the same digest, and the digest is verified with a constant-time
// compare. A capability must be canonical (43 base64url characters) or it is
// refused before it is hashed. It is high-entropy and opaque, so it must travel in
// a host-only cookie or a request body, never in a path or query string.
//
// Documented gap (M2 OAuth transaction work): an entry is bound to a principal
// only. It is not bound to the browser session, the OAuth client, the redirect URI,
// the requested resource or a PKCE challenge, so a capability that leaks to another
// client of the same principal is usable there. That binding belongs with the OAuth
// transaction and is deliberately not attempted here.
type Registry struct {
	cfg RegistryConfig

	mu      sync.Mutex
	entries map[string]*transaction
}

// NewRegistry returns a Registry configured by cfg.
func NewRegistry(cfg RegistryConfig) (*Registry, error) {
	resolved, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Registry{cfg: resolved, entries: make(map[string]*transaction)}, nil
}

// Create stores pending and returns its opaque capability. The capability is the
// only handle to the transaction and it is returned exactly once.
//
// Continuation state over the configured byte bound is refused. Under capacity
// pressure the least valuable entry is evicted — an abandoned start before one a
// user is working on — so a flood of abandoned starts cannot deny service to every
// new login for a whole TTL.
func (r *Registry) Create(pending Pending) (string, error) {
	if size := pending.storedBytes(); size > r.cfg.MaxPendingBytes {
		return "", fmt.Errorf("garmin auth: create login transaction: %w", ErrPendingTooLarge)
	}

	capability, err := newCapability(r.cfg.Rand)
	if err != nil {
		return "", err
	}
	digest, err := capabilityDigest(capability)
	if err != nil {
		return "", err
	}

	now := r.cfg.Clock.Now()
	entry := &transaction{
		digest:    digest,
		pending:   pending,
		createdAt: now,
		expiresAt: now.Add(r.cfg.TTL),
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweepLocked(now)
	if err := r.reserveLocked(); err != nil {
		return "", err
	}
	r.entries[capabilityKeyFor(digest)] = entry
	return capability, nil
}

// Attempt validates a capability for one verification attempt and takes the
// transaction's completion lease.
//
// It rejects a malformed, unknown, expired, exhausted or cross-principal
// capability, and every rejection except "unknown" destroys or charges the
// transaction, so a capability cannot be probed indefinitely. A transaction that is
// already being completed is reported as ErrCompletionInFlight without charging the
// budget, so a replay cannot consume the legitimate user's remaining attempts.
//
// The caller must finish the returned Attempt: Claim on a verified code, Release
// otherwise. A deferred Release is safe, because it does nothing after a Claim.
func (r *Registry) Attempt(capability, principal string) (*Attempt, error) {
	digest, err := capabilityDigest(capability)
	if err != nil {
		return nil, err
	}

	key := capabilityKeyFor(digest)
	now := r.cfg.Clock.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, err := r.liveEntryLocked(key, digest, now)
	if err != nil {
		return nil, err
	}
	if entry.completing {
		return nil, ErrCompletionInFlight
	}
	if err := r.chargeLocked(key, entry, principal); err != nil {
		return nil, err
	}

	entry.attempted = true
	entry.completing = true
	return &Attempt{registry: r, key: key, digest: digest, pending: entry.pending}, nil
}

// Fail marks a transaction failed and removes it.
func (r *Registry) Fail(capability string) error {
	return r.terminate(capability, Machine.Fail)
}

// Cancel abandons a transaction and removes it. It is allowed while a completion is
// in flight: the completion then loses its claim, which is the safe direction.
func (r *Registry) Cancel(capability string) error {
	return r.terminate(capability, Machine.Cancel)
}

// Len reports how many transactions are live. Expired entries that have not been
// swept yet are still counted, because they are still resident.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// terminate applies transition to the entry's machine and removes the entry on
// success.
func (r *Registry) terminate(capability string, transition func(Machine) (Machine, error)) error {
	digest, err := capabilityDigest(capability)
	if err != nil {
		return err
	}

	key := capabilityKeyFor(digest)
	now := r.cfg.Clock.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, err := r.liveEntryLocked(key, digest, now)
	if err != nil {
		return err
	}

	next, err := transition(entry.pending.machineValue())
	if err != nil {
		return fmt.Errorf("garmin auth: login transaction: %w: %w", ErrTransactionOutOfOrder, err)
	}

	entry.pending = entry.pending.withMachine(next)
	delete(r.entries, key)
	return nil
}

// liveEntryLocked resolves key to a live entry, verifying the digest in constant
// time and destroying an expired entry as it is noticed.
func (r *Registry) liveEntryLocked(key string, digest []byte, now time.Time) (*transaction, error) {
	entry, ok := r.entries[key]
	if !ok || !sameDigest(entry.digest, digest) {
		return nil, ErrUnknownTransaction
	}
	if !now.Before(entry.expiresAt) {
		r.expireLocked(key, entry)
		return nil, ErrTransactionExpired
	}
	return entry, nil
}

// chargeLocked spends one attempt from the entry's budget and checks the principal.
func (r *Registry) chargeLocked(key string, entry *transaction, principal string) error {
	entry.attempts++
	if entry.attempts > r.cfg.MaxAttempts {
		r.failLocked(key, entry)
		return ErrTransactionAttemptsExhausted
	}
	if entry.pending.Principal() != principal {
		return ErrTransactionPrincipalMismatch
	}
	return nil
}
