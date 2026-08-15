package cmd

import (
	"context"
	"sync/atomic"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// revocationBufferSize bounds the undelivered revocations the bus holds.
//
// It is generous relative to what a deployment produces — a revocation is a human
// or operator action — and finite, so a consumer that stopped reading costs a fixed
// amount of memory rather than an unbounded one.
const revocationBufferSize = 256

// A revocationBus carries revocations from the store to the HTTP transport.
//
// It is the composition root's join between two packages that must not know each
// other: internal/store announces what it revoked, internal/mcpserver terminates
// the sessions a revocation covers, and neither imports the other. That is the same
// rule sqliteTokens follows, and this is the one place that may know both sides.
//
// # Delivery
//
// Publishing never blocks. The store calls PublishRevocation on the goroutine that
// ran the cascade, immediately after it committed, so a slow or absent consumer
// must not be able to hold up a revocation: a full buffer drops the event and
// counts it instead. A dropped event costs the affected session its early
// termination, never the revocation itself — the database is still the authority,
// and the next request on that session is refused by the token check.
//
// # Blast radius
//
// Two events are refused outright, because every field of a Revocation is a
// selector and an empty field matches everything:
//
//   - one that names nothing, which would select every session in the deployment;
//   - one whose principal identifier the identity package refuses, because
//     carrying it as the zero Principal would silently widen the selector from one
//     account to all of them.
//
// A revocationBus is safe for concurrent use and holds no package-level state.
type revocationBus struct {
	events  chan mcpserver.Revocation
	dropped atomic.Uint64
}

// The two assertions this type exists for. They stop compiling if either side moves.
var (
	_ store.RevocationSink       = (*revocationBus)(nil)
	_ mcpserver.RevocationSource = (*revocationBus)(nil)
)

// newRevocationBus returns an empty bus with the documented bound.
func newRevocationBus() *revocationBus {
	return &revocationBus{events: make(chan mcpserver.Revocation, revocationBufferSize)}
}

// PublishRevocation translates one committed store event and offers it to the
// consumer. It never blocks.
func (b *revocationBus) PublishRevocation(event store.RevocationEvent) {
	revocation, ok := revocationOf(event)
	if !ok {
		b.dropped.Add(1)
		return
	}
	select {
	case b.events <- revocation:
	default:
		b.dropped.Add(1)
	}
}

// Revocations returns the channel the transport watches.
//
// The context bounds the watch and not the bus: the same channel is returned to
// every caller, and it is never closed, because a closed channel would end the
// transport's watch for the rest of the process's life.
func (b *revocationBus) Revocations(context.Context) <-chan mcpserver.Revocation {
	return b.events
}

// Dropped reports how many events were discarded, either because the buffer was
// full or because the event was one the bus refuses to express. It exists for
// diagnostics, and for the test that proves publishing does not block.
func (b *revocationBus) Dropped() uint64 { return b.dropped.Load() }

// revocationOf translates a store event into a transport selector, reporting false
// for an event that must not be published.
func revocationOf(event store.RevocationEvent) (mcpserver.Revocation, bool) {
	revocation := mcpserver.Revocation{ClientID: event.ClientID, Family: event.FamilyID}
	if event.PrincipalID != "" {
		principal, err := identity.NewPrincipal(event.PrincipalID)
		if err != nil {
			return mcpserver.Revocation{}, false
		}
		revocation.Principal = principal
	}
	if !revocation.Principal.IsValid() && revocation.ClientID == "" && revocation.Family == "" {
		return mcpserver.Revocation{}, false
	}
	return revocation, true
}
