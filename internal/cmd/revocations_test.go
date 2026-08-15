package cmd

import (
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// The synthetic identifiers the bus tests publish under.
const (
	busPrincipal = "11111111-2222-4333-8444-555555555555"
	busClient    = "example-mcp-client"
	busFamily    = "family-0001"
)

// receive reads one translated revocation, or fails.
func receive(t *testing.T, events <-chan mcpserver.Revocation) mcpserver.Revocation {
	t.Helper()

	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("no revocation arrived")
		return mcpserver.Revocation{}
	}
}

// The store speaks in row identifiers and the transport speaks in selectors. The
// bus is the only translation, so every field has to survive it.
func TestRevocationBusTranslatesAStoreEvent(t *testing.T) {
	bus := newRevocationBus()
	events := bus.Revocations(t.Context())

	bus.PublishRevocation(store.RevocationEvent{
		PrincipalID: busPrincipal,
		ClientID:    busClient,
		FamilyID:    busFamily,
		Reason:      store.ReasonConsentRevoked,
	})

	got := receive(t, events)
	if got.Principal.ID() != busPrincipal {
		t.Errorf("principal = %q, want %q", got.Principal.ID(), busPrincipal)
	}
	if got.ClientID != busClient {
		t.Errorf("client = %q, want %q", got.ClientID, busClient)
	}
	if got.Family != busFamily {
		t.Errorf("family = %q, want %q", got.Family, busFamily)
	}
}

// An absent or stalled consumer must never hold up the caller that revoked: a
// revocation that cannot be delivered is dropped and counted, never waited on.
func TestRevocationBusNeverStallsAProducer(t *testing.T) {
	bus := newRevocationBus()
	// Deliberately no consumer: nothing ever reads the channel.

	published := make(chan struct{})
	go func() {
		defer close(published)
		for range revocationBufferSize * 2 {
			bus.PublishRevocation(store.RevocationEvent{
				PrincipalID: busPrincipal,
				Reason:      store.ReasonConsentRevoked,
			})
		}
	}()

	select {
	case <-published:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked on a consumer that never read")
	}
	if dropped := bus.Dropped(); dropped == 0 {
		t.Error("the bus reported no drops although it was published to past its bound")
	}
}

// Every field of a Revocation is a selector and an empty field matches everything,
// so an event that names nothing selects every session. The bus refuses to express
// it: one defective producer must not disconnect a whole deployment.
func TestRevocationBusRefusesAnEventThatNamesNothing(t *testing.T) {
	bus := newRevocationBus()
	events := bus.Revocations(t.Context())

	bus.PublishRevocation(store.RevocationEvent{Reason: store.ReasonConsentRevoked})
	bus.PublishRevocation(store.RevocationEvent{})

	select {
	case event := <-events:
		t.Fatalf("the bus published %+v for an event that named nothing", event)
	case <-time.After(50 * time.Millisecond):
	}
}

// A principal identifier the identity package refuses cannot be carried, and
// dropping only that field would widen the selector from one account to every
// account. The whole event is dropped instead.
func TestRevocationBusDropsAnUnusablePrincipal(t *testing.T) {
	bus := newRevocationBus()
	events := bus.Revocations(t.Context())

	bus.PublishRevocation(store.RevocationEvent{
		PrincipalID: "rider@example.test",
		Reason:      store.ReasonConsentRevoked,
	})

	select {
	case event := <-events:
		t.Fatalf("the bus published %+v for an unusable principal", event)
	case <-time.After(50 * time.Millisecond):
	}
	if bus.Dropped() == 0 {
		t.Error("an unusable principal was discarded without being counted")
	}
}
