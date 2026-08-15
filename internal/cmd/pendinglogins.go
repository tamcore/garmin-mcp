package cmd

import (
	"errors"
	"sync"
	"time"
)

// errTooManyPendingLogins reports a full registry. It is a refusal to start
// another login rather than an eviction of somebody else's, because evicting the
// oldest entry would let a flood of new challenges cancel the logins already in
// progress.
var errTooManyPendingLogins = errors.New("cmd: too many pending logins")

// pendingLogins remembers which login a pending MFA continuation belongs to.
//
// It exists because the two halves of an MFA login arrive as two HTTP requests,
// and the Garmin authenticator requires the same principal on both. The browser
// carries only the transaction cookie, so the association has to live on the
// server.
//
// What it holds is one opaque continuation capability, the staging key the login
// runs under, and the login handle it was started with. There is no password
// field, no one-time code field, and no token field, so there is nothing here for a
// memory dump to find that the Garmin registry does not already hold under its own
// bounds.
//
// It is bounded and it expires. A restart loses every entry, which costs an
// in-progress login a fresh start and costs nothing else.
type pendingLogins struct {
	mu      sync.Mutex
	entries map[string]pendingLogin
	limit   int
	ttl     time.Duration
	now     func() time.Time
}

// pendingLogin is one association and its absolute deadline, which is never
// extended.
type pendingLogin struct {
	// principal is the key the login runs under. It is a staging key until the
	// credentials are accepted, because a remote login has no principal before
	// then.
	principal string

	// email is the login handle the attempt was started with. It is carried so the
	// continuation can register the account under the handle its owner typed, and
	// it is display and lookup data only — never the isolation key.
	email string

	expires time.Time
}

// newPendingLogins returns an empty registry. It is a constructor rather than a
// package-level value because two deployments assembled in one process must not
// share one.
func newPendingLogins(limit int, ttl time.Duration, now func() time.Time) *pendingLogins {
	if now == nil {
		now = time.Now
	}
	return &pendingLogins{
		entries: make(map[string]pendingLogin),
		limit:   limit,
		ttl:     ttl,
		now:     now,
	}
}

// put records the association for a continuation capability.
//
// Expired entries are purged first, so a registry that filled with abandoned
// logins recovers on its own rather than staying full until a restart.
func (p *pendingLogins) put(capability, principal, email string) error {
	if capability == "" || principal == "" || email == "" {
		return errors.New("cmd: a pending login needs a continuation, a principal and a handle")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	p.purge(now)
	if _, known := p.entries[capability]; !known && len(p.entries) >= p.limit {
		return errTooManyPendingLogins
	}
	p.entries[capability] = pendingLogin{
		principal: principal,
		email:     email,
		expires:   now.Add(p.ttl),
	}
	return nil
}

// get reports the login a live continuation belongs to. An expired entry is
// removed and reported as absent.
func (p *pendingLogins) get(capability string) (pendingLogin, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.entries[capability]
	if !ok {
		return pendingLogin{}, false
	}
	if !p.now().Before(entry.expires) {
		delete(p.entries, capability)
		return pendingLogin{}, false
	}
	return entry, true
}

// drop removes an association. It is called as soon as the continuation is
// terminal, so the capability stops resolving at that instant.
func (p *pendingLogins) drop(capability string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.entries, capability)
}

// purge removes every expired entry. The caller holds the lock.
func (p *pendingLogins) purge(now time.Time) {
	for capability, entry := range p.entries {
		if !now.Before(entry.expires) {
			delete(p.entries, capability)
		}
	}
}

// size reports how many associations are held. It exists for the tests, which
// assert that a terminal continuation leaves nothing behind.
func (p *pendingLogins) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}
