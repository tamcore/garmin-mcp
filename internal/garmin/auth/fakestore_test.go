package auth_test

import (
	"context"
	"fmt"
	"sync"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// fakeStore is the in-memory TokenStore the tests run against. It implements the
// same compare-and-set contract the real store must: a Save whose
// expectedVersion does not match the stored version fails with
// auth.ErrVersionConflict.
type fakeStore struct {
	mu       sync.Mutex
	sets     map[string]auth.TokenSet
	versions map[string]int64
	saves    int
	loads    int

	// loadErr, when set, is returned by every Load instead of a stored set.
	loadErr error
	// saveErr, when set, is returned by every Save instead of persisting, so a
	// test can drive the failed-persistence path.
	saveErr error
	// beforeSave runs inside Save, before the version check, so a test can
	// simulate another writer winning the race.
	beforeSave func(principal string)
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sets:     make(map[string]auth.TokenSet),
		versions: make(map[string]int64),
	}
}

func (s *fakeStore) Load(_ context.Context, principal string) (auth.TokenSet, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loads++
	if s.loadErr != nil {
		return auth.TokenSet{}, 0, s.loadErr
	}

	set, ok := s.sets[principal]
	if !ok {
		return auth.TokenSet{}, 0, fmt.Errorf("fake store: %q: %w", principal, auth.ErrNoTokens)
	}
	return set, s.versions[principal], nil
}

func (s *fakeStore) Save(
	_ context.Context,
	principal string,
	set auth.TokenSet,
	expectedVersion int64,
) (int64, error) {
	if s.beforeSave != nil {
		s.beforeSave(principal)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.saves++
	if s.saveErr != nil {
		return 0, s.saveErr
	}
	if s.versions[principal] != expectedVersion {
		return 0, fmt.Errorf("fake store: %q: %w", principal, auth.ErrVersionConflict)
	}

	next := expectedVersion + 1
	s.sets[principal] = set
	s.versions[principal] = next
	return next, nil
}

func (s *fakeStore) Delete(_ context.Context, principal string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sets, principal)
	delete(s.versions, principal)
	return nil
}

// put seeds a stored set outside the CAS path.
func (s *fakeStore) put(principal string, set auth.TokenSet, version int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sets[principal] = set
	s.versions[principal] = version
}

// get reports the stored set and version.
func (s *fakeStore) get(principal string) (auth.TokenSet, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	set, ok := s.sets[principal]
	return set, s.versions[principal], ok
}

// saveCount reports how many Save calls were made.
func (s *fakeStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

// loadCount reports how many Load calls were made.
func (s *fakeStore) loadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads
}

// bump raises the stored version without the caller's knowledge, as a competing
// writer would.
func (s *fakeStore) bump(principal string, set auth.TokenSet) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sets[principal] = set
	s.versions[principal]++
}
