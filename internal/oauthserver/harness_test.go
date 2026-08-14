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
	defer h.mu.Unlock()
	return h.clock
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clock = h.clock.Add(d)
}
