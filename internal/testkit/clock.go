package testkit

import (
	"sync"
	"time"
)

// Clock is the time source the anti-WAF pacing code depends on, so tests can
// verify pacing without really waiting.
type Clock interface {
	// Now returns the current instant.
	Now() time.Time
	// Sleep blocks for d. A non-positive duration returns immediately.
	Sleep(d time.Duration)
}

// FakeClock is a Clock that never blocks: Sleep advances a virtual now and
// records the requested duration. It is safe for concurrent use.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

// NewFakeClock returns a FakeClock positioned at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now returns the virtual current instant.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep records d and advances the virtual clock by it. Non-positive durations
// are ignored, matching time.Sleep semantics.
func (c *FakeClock) Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
}

// Advance moves the virtual clock forward without recording a sleep.
func (c *FakeClock) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Sleeps returns a copy of the recorded sleep durations, in order.
func (c *FakeClock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]time.Duration, len(c.sleeps))
	copy(out, c.sleeps)
	return out
}
