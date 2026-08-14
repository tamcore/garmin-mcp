package auth

import "fmt"

// Attempt is one leased verification attempt on a pending MFA transaction.
//
// It is handed out by Registry.Attempt and it holds that transaction's single
// completion lease, so no second caller can verify a code, exchange a ticket or
// save tokens for the same transaction while it is alive. It is not safe for
// concurrent use by several goroutines: it belongs to the one completion that took
// it.
//
// Exactly one of Claim and Release decides the transaction, and calling both is
// safe: whichever runs first settles the attempt and the other does nothing. The
// usual shape is a deferred Release plus a Claim on the verified path.
type Attempt struct {
	registry *Registry
	key      string
	digest   []byte
	pending  Pending
	// settled marks the lease as given up. It is read and written under the
	// registry's mutex.
	settled bool
}

// Pending is the immutable continuation state of the leased transaction.
func (a *Attempt) Pending() Pending { return a.pending }

// Claim consumes the transaction as an authenticated success.
//
// It is the terminal transition and it is atomic: the absolute TTL is re-checked,
// the state machine moves to authenticated and the entry is removed under one hold
// of the registry's mutex. A caller must Claim before it performs any effect it
// cannot take back — the token exchange and the save — so a transaction can never
// be left half-completed: either the capability is consumed and this completion owns
// the outcome, or nothing external happened.
//
// It reports ErrTransactionExpired when the transaction aged out during the
// verification, and ErrUnknownTransaction when it was cancelled, swept or already
// claimed.
func (a *Attempt) Claim() error {
	r := a.registry
	now := r.cfg.Clock.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	if a.settled {
		return ErrUnknownTransaction
	}
	a.settled = true

	entry, err := r.liveEntryLocked(a.key, a.digest, now)
	if err != nil {
		return err
	}

	next, err := entry.pending.machineValue().VerifyMFA()
	if err != nil {
		r.failLocked(a.key, entry)
		return fmt.Errorf("garmin auth: login transaction: %w: %w", ErrTransactionOutOfOrder, err)
	}

	entry.pending = entry.pending.withMachine(next)
	delete(r.entries, a.key)
	return nil
}

// Release gives the completion lease back without consuming the transaction, which
// is what a wrong code or a failed verify call must do: the attempt budget is what
// bounds retries, so the transaction stays usable until that budget runs out.
//
// It does nothing after a Claim.
func (a *Attempt) Release() {
	r := a.registry

	r.mu.Lock()
	defer r.mu.Unlock()

	if a.settled {
		return
	}
	a.settled = true

	if entry, ok := r.entries[a.key]; ok && sameDigest(entry.digest, a.digest) {
		entry.completing = false
	}
}
