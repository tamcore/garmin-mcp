package mcpserver

import (
	"context"
	"testing"
)

// raceAttempts is how many times the ready-ready race is run. A select over two
// ready cases picks pseudo-randomly, so one attempt proves nothing and a hundred
// make a missing recheck overwhelmingly likely to show. The odds of a broken
// implementation surviving are 2^-raceAttempts.
const raceAttempts = 100

// TestSleepPrefersCancellationWhenBothAreReady pins the recheck after the timer.
//
// This is the one property the session-level tests cannot reach. When the pause has
// elapsed and the caller has cancelled, both select cases are ready at once and Go
// chooses between ready cases at random — so a cancelled call could still be
// dispatched, at a rate no test would reliably observe through the transport.
//
// A zero duration makes the timer ready immediately, which turns a rare production
// race into a certainty here.
func TestSleepPrefersCancellationWhenBothAreReady(t *testing.T) {
	t.Parallel()

	server := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for attempt := range raceAttempts {
		if err := server.sleep(ctx, 0); err == nil {
			t.Fatalf("attempt %d: sleep reported success for a cancelled context, "+
				"so the timer won the race and the call would have been dispatched",
				attempt)
		}
	}
}

// TestSleepReturnsNilWhenItElapsesUninterrupted is the other half: an ordinary pause
// that nobody cancels must let the call through, or the delay would refuse every
// write it was meant merely to slow.
func TestSleepReturnsNilWhenItElapsesUninterrupted(t *testing.T) {
	t.Parallel()

	if err := (&Server{}).sleep(context.Background(), 0); err != nil {
		t.Errorf("sleep() = %v, want nil for a pause that simply elapsed", err)
	}
}
