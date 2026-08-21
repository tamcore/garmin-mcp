package mcpserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

const (
	// testSafetyDelay is the delay every test here configures. Nothing waits it
	// out: the sleeper is injected, so the value only has to be recognisable.
	testSafetyDelay = 3 * time.Second

	// actionAccept is the elicitation action a confirming client answers with, and
	// fieldConfirm is the field the confirmation schema asks for.
	actionAccept = "accept"
	fieldConfirm = "confirm"
)

// recordingSleeper stands in for the wall clock. It records what it was asked to
// wait and can refuse, which is how a cancelled wait is simulated without a race.
type recordingSleeper struct {
	waited []time.Duration
	err    error
}

func (s *recordingSleeper) sleep(ctx context.Context, d time.Duration) error {
	s.waited = append(s.waited, d)
	if s.err != nil {
		return s.err
	}
	return ctx.Err()
}

// confirmingClient accepts every elicitation, so a destructive call reaches the
// delay instead of stopping at the confirmation gate.
func confirmingClient() *mcp.ClientOptions {
	return &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: actionAccept, Content: map[string]any{fieldConfirm: true}}, nil
		},
	}
}

// withSafetyDelay configures the delay and the injected sleeper on a test server.
func withSafetyDelay(
	t *testing.T, sleeper *recordingSleeper, delay time.Duration,
) func(*mcpserver.Deps) {
	t.Helper()

	enable := destructiveEnabled(t)
	return func(d *mcpserver.Deps) {
		enable(d)
		d.SafetyDelay = delay
		d.Sleep = sleeper.sleep
	}
}

// TestSafetyDelayPausesWritesAndDestructivesButNeverReads pins the tiers the delay
// applies to. A read is not slowed: it changes nothing, so a cancellation window
// buys nothing and the pause would be pure latency.
func TestSafetyDelayPausesWritesAndDestructivesButNeverReads(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		tool       string
		wantWaited bool
	}{
		{"a read is never delayed", readTool, false},
		{"a write is delayed", writeTool, true},
		{"a destructive call is delayed", destructiveTool, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sleeper := &recordingSleeper{}
			server, probes, _ := tieredServer(t, withSafetyDelay(t, sleeper, testSafetyDelay))
			ctx := context.Background()
			session := connectClient(t, ctx, server, confirmingClient())

			if _, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      tc.tool,
				Arguments: map[string]any{textArg: testText},
			}); err != nil {
				t.Fatalf("CallTool returned error: %v", err)
			}

			if calls, _, _ := probes[tc.tool].snapshot(); calls != 1 {
				t.Fatalf("the handler ran %d times, want 1", calls)
			}
			switch {
			case tc.wantWaited && len(sleeper.waited) != 1:
				t.Fatalf("waited %v, want exactly one pause", sleeper.waited)
			case tc.wantWaited && sleeper.waited[0] != testSafetyDelay:
				t.Errorf("waited %v, want the configured %v", sleeper.waited[0], testSafetyDelay)
			case !tc.wantWaited && len(sleeper.waited) != 0:
				t.Errorf("a read waited %v", sleeper.waited)
			}
		})
	}
}

// TestSafetyDelayOfZeroNeverWaits proves the setting can be turned off, which is
// what every deployment that did not ask for it gets.
func TestSafetyDelayOfZeroNeverWaits(t *testing.T) {
	t.Parallel()

	sleeper := &recordingSleeper{}
	server, probes, _ := tieredServer(t, withSafetyDelay(t, sleeper, 0))
	ctx := context.Background()
	session := connectClient(t, ctx, server, confirmingClient())

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      writeTool,
		Arguments: map[string]any{textArg: testText},
	}); err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	if len(sleeper.waited) != 0 {
		t.Errorf("a zero delay still waited %v", sleeper.waited)
	}
	if calls, _, _ := probes[writeTool].snapshot(); calls != 1 {
		t.Errorf("the handler ran %d times, want 1", calls)
	}
}

func TestLocalWriteObservesSafetyDelay(t *testing.T) {
	t.Parallel()

	sleeper := &recordingSleeper{}
	enable := localDestructiveEnabled(t)
	server, probes, _ := tieredServer(t, func(d *mcpserver.Deps) {
		enable(d)
		d.SafetyDelay = testSafetyDelay
		d.Sleep = sleeper.sleep
	})
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      writeTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("local write was refused: %v", result.Content)
	}
	if len(sleeper.waited) != 1 || sleeper.waited[0] != testSafetyDelay {
		t.Fatalf("local write waited %v, want exactly %v", sleeper.waited, testSafetyDelay)
	}
	if calls, _, _ := probes[writeTool].snapshot(); calls != 1 {
		t.Fatalf("the local write handler ran %d times, want 1", calls)
	}
}

// TestCancellingDuringTheDelayStopsTheCall is the point of the whole feature.
//
// A pause that cannot be interrupted is latency, not safety. When the wait ends in
// a cancellation the tool must never run, and the caller must be told why.
func TestCancellingDuringTheDelayStopsTheCall(t *testing.T) {
	t.Parallel()

	sleeper := &recordingSleeper{err: context.Canceled}
	server, probes, _ := tieredServer(t, withSafetyDelay(t, sleeper, testSafetyDelay))
	ctx := context.Background()
	session := connectClient(t, ctx, server, confirmingClient())

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      writeTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("CallTool returned an unexpected error: %v", err)
	}
	if calls, _, _ := probes[writeTool].snapshot(); calls != 0 {
		t.Fatalf("the handler ran %d times after the wait was cancelled", calls)
	}
	if err == nil && !result.IsError {
		t.Error("a cancelled write reported success")
	}
}

// TestARefusedCallNeverWaits proves the delay sits after every gate. Waiting three
// seconds to refuse a call teaches a prober how long the gate takes and costs the
// server the wait, which is the opposite of what the setting is for.
func TestARefusedCallNeverWaits(t *testing.T) {
	t.Parallel()

	// No destructiveEnabled here: the write tier stays disabled, so the call is
	// refused by the policy gate.
	sleeper := &recordingSleeper{}
	server, probes, _ := tieredServer(t, func(d *mcpserver.Deps) {
		d.SafetyDelay = testSafetyDelay
		d.Sleep = sleeper.sleep
	})
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      writeTool,
		Arguments: map[string]any{textArg: testText},
	})
	if err != nil {
		t.Fatalf("a policy refusal must not be a transport error, got %v", err)
	}
	if !result.IsError {
		t.Fatal("the write tool must be refused while its tier is disabled")
	}
	if len(sleeper.waited) != 0 {
		t.Errorf("a refused call waited %v before being refused", sleeper.waited)
	}
	if calls, _, _ := probes[writeTool].snapshot(); calls != 0 {
		t.Errorf("the handler ran %d times despite the refusal", calls)
	}
}

// realDelay is the production pause this test configures. It is short, because the
// assertion is that the handler never runs after a cancellation, and a broken
// implementation must be given time to prove it by running.
const realDelay = 500 * time.Millisecond

// settleAfterCancel is how long the test watches after cancelling. It is comfortably
// longer than realDelay, so an uninterruptible wait finishes inside the window and is
// caught rather than outlived.
const settleAfterCancel = 2 * time.Second

// TestTheRealWaitIsInterruptible exercises the production timer, with no sleeper
// injected.
//
// The other tests here replace the wait with a recording seam, which proves the
// middleware asks for a pause but proves nothing about the pause itself: swap the
// real implementation for an uninterruptible time.Sleep and every one of them still
// passes.
//
// The assertion is deliberately about the handler and not about how long the call
// took. Cancelling the context makes CallTool return at once whatever the server is
// doing, so elapsed time measures the client and says nothing about whether the
// write was later sent to Garmin. What matters is that the handler never runs, and
// that is checked after waiting past the point where an uninterrupted pause would
// have finished.
//
// It also covers the race the recheck after the timer closes: cancel and elapse can
// both be ready, and a select picks between ready cases at random.
func TestTheRealWaitIsInterruptible(t *testing.T) {
	t.Parallel()

	enable := destructiveEnabled(t)
	server, probes, _ := tieredServer(t, func(d *mcpserver.Deps) {
		enable(d)
		d.SafetyDelay = realDelay
		// Deps.Sleep stays nil on purpose: this must be the real wait.
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := connectClient(t, ctx, server, confirmingClient())

	go func() {
		// Cancel once the call is certainly inside the pause.
		time.Sleep(realDelay / 10)
		cancel()
	}()

	//nolint:errcheck // the call is cancelled on purpose; the handler is the assertion
	_, _ = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      writeTool,
		Arguments: map[string]any{textArg: testText},
	})

	// Watch past the moment an uninterrupted pause would have elapsed.
	time.Sleep(settleAfterCancel)

	if calls, _, _ := probes[writeTool].snapshot(); calls != 0 {
		t.Errorf("the handler ran %d times after the caller cancelled mid-pause: "+
			"the wait finished instead of being interrupted", calls)
	}
}
