package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	// capabilityBytes is the entropy of a transaction capability: 256 bits.
	capabilityBytes = 32
)

// Registry errors. Each is comparable with errors.Is and carries no transaction
// material.
var (
	// ErrUnknownTransaction reports a capability the registry does not hold. A
	// consumed, cancelled, expired or forged capability is reported the same way
	// on purpose: the registry is not an oracle for which one it was.
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
	// state machine, for example completing before a code was submitted.
	ErrTransactionOutOfOrder = errors.New("garmin auth: login transaction transition out of order")
	// ErrRegistryFull reports that the bounded registry has no room.
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
	// Clock is the time source. Nil means the system clock.
	Clock Clock
	// Rand is the entropy source for capabilities. Nil means crypto/rand.
	Rand io.Reader
}

// withDefaults returns cfg with every zero field filled in, or an error for a
// negative bound.
func (c RegistryConfig) withDefaults() (RegistryConfig, error) {
	if c.TTL < 0 || c.MaxAttempts < 0 || c.MaxEntries < 0 {
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
	pending   Pending
	expiresAt time.Time
	attempts  int
	// attempted records that at least one verification attempt was accepted, so
	// the terminal transition cannot run out of order.
	attempted bool
}

// Registry is the bounded, in-memory store of pending MFA logins.
//
// Interleaved logins cannot see each other's state by construction: every login
// gets its own entry under its own capability, entries are keyed by the hash of
// that capability, and a lookup returns an immutable Pending snapshot rather than
// a shared pointer. There is no per-client "current login" field for a second
// login to overwrite, which is the upstream 0.3.10 bug this design removes.
//
// The capability itself is never stored: the map key is its SHA-256, so a memory
// disclosure does not hand out usable capabilities. It is high-entropy and
// opaque, so it must travel in a host-only cookie or a request body, never in a
// path or query string.
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
func (r *Registry) Create(pending Pending) (string, error) {
	capability, err := newCapability(r.cfg.Rand)
	if err != nil {
		return "", err
	}

	now := r.cfg.Clock.Now()
	entry := &transaction{pending: pending, expiresAt: now.Add(r.cfg.TTL)}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweepLocked(now)
	if len(r.entries) >= r.cfg.MaxEntries {
		return "", ErrRegistryFull
	}
	r.entries[capabilityKey(capability)] = entry
	return capability, nil
}

// Attempt validates a capability for one verification attempt and returns the
// transaction's pending state.
//
// It rejects an unknown, expired, exhausted or cross-principal capability, and
// every rejection except "unknown" destroys or charges the transaction, so a
// capability cannot be probed indefinitely.
func (r *Registry) Attempt(capability, principal string) (Pending, error) {
	now := r.cfg.Clock.Now()
	key := capabilityKey(capability)

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[key]
	if !ok {
		return Pending{}, ErrUnknownTransaction
	}
	if !now.Before(entry.expiresAt) {
		r.expireLocked(key, entry)
		return Pending{}, ErrTransactionExpired
	}

	entry.attempts++
	if entry.attempts > r.cfg.MaxAttempts {
		r.failLocked(key, entry)
		return Pending{}, ErrTransactionAttemptsExhausted
	}
	if entry.pending.Principal() != principal {
		return Pending{}, ErrTransactionPrincipalMismatch
	}

	entry.attempted = true
	return entry.pending, nil
}

// Complete runs the single-use terminal transition: the transaction moves to
// authenticated and is removed, so the capability can never be replayed. It
// requires a preceding accepted Attempt, because the OTP must be submitted
// before the login can succeed.
func (r *Registry) Complete(capability string) error {
	return r.terminate(capability, Machine.VerifyMFA, true)
}

// Fail marks a transaction failed and removes it.
func (r *Registry) Fail(capability string) error {
	return r.terminate(capability, Machine.Fail, false)
}

// Cancel abandons a transaction and removes it.
func (r *Registry) Cancel(capability string) error {
	return r.terminate(capability, Machine.Cancel, false)
}

// Len reports how many transactions are live. Expired entries that have not been
// swept yet are still counted, because they are still resident.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// terminate applies transition to the entry's machine and removes the entry on
// success. requireAttempt enforces the ordering rule for Complete.
func (r *Registry) terminate(
	capability string,
	transition func(Machine) (Machine, error),
	requireAttempt bool,
) error {
	key := capabilityKey(capability)
	now := r.cfg.Clock.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[key]
	if !ok {
		return ErrUnknownTransaction
	}
	if !now.Before(entry.expiresAt) {
		r.expireLocked(key, entry)
		return ErrTransactionExpired
	}
	if requireAttempt && !entry.attempted {
		return ErrTransactionOutOfOrder
	}

	next, err := transition(entry.pending.machineValue())
	if err != nil {
		return fmt.Errorf("garmin auth: login transaction: %w: %w", ErrTransactionOutOfOrder, err)
	}

	entry.pending = entry.pending.withMachine(next)
	delete(r.entries, key)
	return nil
}

// expireLocked records the expiry on the entry's machine and drops it.
func (r *Registry) expireLocked(key string, entry *transaction) {
	if next, err := entry.pending.machineValue().Expire(); err == nil {
		entry.pending = entry.pending.withMachine(next)
	}
	delete(r.entries, key)
}

// failLocked records the failure on the entry's machine and drops it.
func (r *Registry) failLocked(key string, entry *transaction) {
	if next, err := entry.pending.machineValue().Fail(); err == nil {
		entry.pending = entry.pending.withMachine(next)
	}
	delete(r.entries, key)
}

// sweepLocked drops every entry whose absolute TTL elapsed, so an abandoned
// login cannot occupy a slot until the process restarts.
func (r *Registry) sweepLocked(now time.Time) {
	for key, entry := range r.entries {
		if !now.Before(entry.expiresAt) {
			r.expireLocked(key, entry)
		}
	}
}

// newCapability returns a fresh 256-bit opaque capability, base64url encoded
// without padding so it is safe in a cookie value.
func newCapability(source io.Reader) (string, error) {
	raw := make([]byte, capabilityBytes)
	if _, err := io.ReadFull(source, raw); err != nil {
		return "", fmt.Errorf("garmin auth: generate login transaction capability: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// capabilityKey is the lookup value stored for a capability. Hashing means the
// registry never holds a usable capability, and an empty capability still maps to
// a key that is simply absent.
func capabilityKey(capability string) string {
	sum := sha256.Sum256([]byte(capability))
	return hex.EncodeToString(sum[:])
}
