package oauthserver

import (
	"sync"
	"testing"
	"time"
)

// harness wires a Server to a fake store and a movable clock.
type harness struct {
	t     *testing.T
	srv   *Server
	store *fakeStore
	mu    sync.Mutex
	clock time.Time

	// afterRead, when set, runs after every read of the clock, outside the lock,
	// and is told how many reads have happened so far (1-indexed). It exists so a
	// test can move the clock between what would be two separate s.now() calls
	// inside one function invocation, to prove the function reads the clock only
	// once.
	afterRead func(reads int, h *harness)
	nowReads  int
}

func newHarness(t *testing.T, specs ...ClientSpec) *harness {
	t.Helper()
	if len(specs) == 0 {
		specs = []ClientSpec{publicClientSpec()}
	}
	h := &harness{t: t, store: newFakeStore(), clock: testNow}
	for _, spec := range specs {
		h.store.addClient(mustClient(t, spec))
	}
	srv, err := New(testConfig(), Deps{Store: h.store, Now: h.now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.srv = srv
	return h
}

func (h *harness) now() time.Time {
	h.mu.Lock()
	result := h.clock
	h.nowReads++
	reads := h.nowReads
	hook := h.afterRead
	h.mu.Unlock()
	if hook != nil {
		hook(reads, h)
	}
	return result
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clock = h.clock.Add(d)
}
