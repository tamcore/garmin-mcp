package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// stagingIDPrefix labels a staging key in a diagnostic. It is not what makes a key
// a staging key — membership of the map is — so a principal identifier that
// happened to start with it could not reach a staged entry.
const stagingIDPrefix = "staging-"

// errStagingFull reports that too many logins are in flight. It refuses a new one
// rather than evicting somebody else's, for the same reason pendingLogins does.
var errStagingFull = errors.New("cmd: too many logins are in flight")

// errNoStagedTokens reports a staging key with nothing behind it.
var errNoStagedTokens = errors.New("cmd: the login staged no token set")

// stagedTokens is the token store a remote login writes through.
//
// # Why it exists
//
// A remote login discovers its account from the credentials, so the principal is
// not known until Garmin has accepted them. The authenticator, however, persists
// the DI token set as the last step of the login, and the SQLite store refuses a
// record for a principal that does not exist. Resolving that by creating the
// principal first is what the deployment used to do, and it means anyone who can
// reach the login page can write principal rows for an email they do not own.
//
// So a login runs against a staging key instead. Everything the authenticator does
// is unchanged; the token set it produces is held here, in memory, until the caller
// has resolved the real principal from the account Garmin confirmed and commits it.
// A login that fails commits nothing, and no row is written for it.
//
// # What it holds
//
// One Garmin DI token set per login in flight, bounded and expiring on the same
// schedule as the MFA continuation it belongs to. The set is an auth.TokenSet, so
// it redacts itself in every rendering, and it never touches the disk: a process
// that dies loses it, which costs an in-progress login a fresh start and nothing
// else.
//
// Every key that is not staged is delegated unchanged, so this is also the store
// the refresher and every completed login use.
type stagedTokens struct {
	delegate auth.TokenStore

	mu      sync.Mutex
	entries map[string]*stagedEntry

	limit int
	ttl   time.Duration
	now   func() time.Time
}

// stagedEntry is one login's staged token set and its absolute deadline, which is
// never extended.
type stagedEntry struct {
	set     auth.TokenSet
	version int64
	expires time.Time
}

// The assertion this type exists for.
var _ auth.TokenStore = (*stagedTokens)(nil)

// newStagedTokens wraps delegate. A nil delegate is a wiring defect and is reported
// rather than dereferenced later.
func newStagedTokens(
	delegate auth.TokenStore, limit int, ttl time.Duration, now func() time.Time,
) (*stagedTokens, error) {
	if delegate == nil {
		return nil, errors.New("cmd: no token store to stage writes for")
	}
	if now == nil {
		now = time.Now
	}
	return &stagedTokens{
		delegate: delegate,
		entries:  make(map[string]*stagedEntry),
		limit:    limit,
		ttl:      ttl,
		now:      now,
	}, nil
}

// begin opens a staging area and returns the key a login runs under.
func (s *stagedTokens) begin() (string, error) {
	id, err := newStagingID()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	if len(s.entries) >= s.limit {
		return "", fmt.Errorf("cmd: %d logins are already in flight: %w",
			len(s.entries), errStagingFull)
	}
	s.entries[id] = &stagedEntry{expires: s.now().Add(s.ttl)}
	return id, nil
}

// take removes the staging area and returns what the login wrote there. It reports
// errNoStagedTokens when the login stored nothing, which is what an expired or
// already-committed staging key looks like.
func (s *stagedTokens) take(id string) (auth.TokenSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[id]
	delete(s.entries, id)
	if !ok || entry.set.IsZero() {
		return auth.TokenSet{}, errNoStagedTokens
	}
	return entry.set, nil
}

// drop discards a staging area. It is safe to call for a key that is already gone,
// so a caller can defer it.
func (s *stagedTokens) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
}

// inFlight reports how many logins hold a staging area. It exists for the tests
// that prove a finished login leaves nothing behind.
func (s *stagedTokens) inFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	return len(s.entries)
}

// Load returns the staged set for a staging key, and delegates for every other
// principal.
func (s *stagedTokens) Load(ctx context.Context, principal string) (auth.TokenSet, int64, error) {
	if entry, ok := s.entry(principal); ok {
		if entry.set.IsZero() {
			return auth.TokenSet{}, 0, fmt.Errorf("cmd: nothing is staged yet: %w", auth.ErrNoTokens)
		}
		return entry.set, entry.version, nil
	}
	return s.delegate.Load(ctx, principal)
}

// Save holds the set for a staging key, and delegates for every other principal.
//
// The compare-and-set is applied to the staged entry as well, so the authenticator
// meets the same contract here that it meets against the database and no code path
// has to know which store it is writing to.
func (s *stagedTokens) Save(
	ctx context.Context, principal string, set auth.TokenSet, expectedVersion int64,
) (int64, error) {
	s.mu.Lock()
	entry, staged := s.entries[principal]
	if staged {
		defer s.mu.Unlock()
		if entry.version != expectedVersion {
			return 0, fmt.Errorf("cmd: the staged version is %d, not %d: %w",
				entry.version, expectedVersion, auth.ErrVersionConflict)
		}
		entry.set, entry.version = set, entry.version+1
		return entry.version, nil
	}
	s.mu.Unlock()

	return s.delegate.Save(ctx, principal, set, expectedVersion)
}

// Delete clears a staged set, and delegates for every other principal.
func (s *stagedTokens) Delete(ctx context.Context, principal string) error {
	s.mu.Lock()
	if entry, staged := s.entries[principal]; staged {
		defer s.mu.Unlock()
		entry.set, entry.version = auth.TokenSet{}, 0
		return nil
	}
	s.mu.Unlock()

	return s.delegate.Delete(ctx, principal)
}

// entry returns a live staging entry, dropping expired ones first.
func (s *stagedTokens) entry(id string) (*stagedEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()

	entry, ok := s.entries[id]
	return entry, ok
}

// expire drops every entry past its deadline. The caller holds the lock.
func (s *stagedTokens) expire() {
	now := s.now()
	for id, entry := range s.entries {
		if !now.Before(entry.expires) {
			delete(s.entries, id)
		}
	}
}

// newStagingID mints an unguessable staging key from crypto/rand. It is not a
// credential — it never leaves the process — but it addresses one login's token
// set, so it is generated the way one would be.
func newStagingID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("cmd: generate a staging key: %w", err)
	}
	return stagingIDPrefix + hex.EncodeToString(raw), nil
}
