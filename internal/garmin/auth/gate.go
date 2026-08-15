package auth

import (
	"context"
	"sync"
)

// TokenGate serializes token-producing operations per principal.
//
// A login and a refresh both end in a compare-and-set write of one principal's DI
// token set. Left unserialized they interleave: one reads a baseline version, the
// other rotates and stores a new set, and the first then writes over a refresh
// token it never saw. The compare-and-set detects that, but detection costs the
// user a failed login, so both paths take this gate and queue instead.
//
// Pass one TokenGate to both Config.TokenGate and RefreshConfig.TokenGate whenever
// an Authenticator and a Refresher share a store. A nil field means the component
// builds a private gate, which serializes that component against itself only.
//
// A TokenGate is safe for concurrent use and keeps no entry for an idle principal,
// so it cannot grow without bound.
type TokenGate struct {
	mu      sync.Mutex
	entries map[string]*gateEntry
}

// gateEntry is one principal's slot. tokens is a semaphore of capacity one; refs
// counts the callers that hold or await it, so the entry can be dropped when the
// last one leaves.
type gateEntry struct {
	tokens chan struct{}
	refs   int
}

// NewTokenGate returns an empty TokenGate.
func NewTokenGate() *TokenGate {
	return &TokenGate{entries: make(map[string]*gateEntry)}
}

// Acquire takes principal's slot and returns the release function, which the
// caller must call.
//
// It exists for the one caller outside this package that also writes a principal's
// token set: a composition root that persists a set a login produced has to queue
// behind the same gate, or its write races the refresh this gate exists to
// serialize against. It reports ctx.Err() when the context ends before the slot is
// free, and the returned function is nil in that case.
func (g *TokenGate) Acquire(ctx context.Context, principal string) (func(), error) {
	return g.acquire(ctx, principal)
}

// acquire takes principal's slot and returns the release function. It reports
// ctx.Err() when the context ends before the slot is free, and the returned
// function is nil in that case.
func (g *TokenGate) acquire(ctx context.Context, principal string) (func(), error) {
	entry := g.enter(principal)

	select {
	case entry.tokens <- struct{}{}:
		return func() { g.leave(principal, entry, true) }, nil
	case <-ctx.Done():
		g.leave(principal, entry, false)
		return nil, ctx.Err()
	}
}

// enter registers a caller on principal's slot, creating it when needed.
func (g *TokenGate) enter(principal string) *gateEntry {
	g.mu.Lock()
	defer g.mu.Unlock()

	entry, ok := g.entries[principal]
	if !ok {
		entry = &gateEntry{tokens: make(chan struct{}, 1)}
		g.entries[principal] = entry
	}
	entry.refs++
	return entry
}

// leave deregisters a caller, giving the slot back when it was held, and drops the
// entry once nobody is left.
func (g *TokenGate) leave(principal string, entry *gateEntry, held bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if held {
		<-entry.tokens
	}
	entry.refs--
	if entry.refs == 0 {
		delete(g.entries, principal)
	}
}

// waiters reports how many callers hold or await principal's slot. It exists so a
// test can observe that a caller is queued on the gate, which is what makes the
// serialization test deterministic rather than timing-dependent.
func (g *TokenGate) waiters(principal string) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	if entry, ok := g.entries[principal]; ok {
		return entry.refs
	}
	return 0
}
