package auth

import "time"

// Clock is the time source this package reads. It is injected so a test can
// place a login at a fixed instant. testkit.FakeClock satisfies it.
type Clock interface {
	// Now returns the current instant.
	Now() time.Time
}

// Sleeper is the delay this package uses for anti-WAF pacing. It is injected
// separately from Clock so a test never really waits; testkit.FakeClock
// satisfies both, so one fake can serve as both.
type Sleeper interface {
	// Sleep blocks for d. A non-positive duration returns immediately.
	Sleep(d time.Duration)
}

// systemClock is the production Clock and Sleeper.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	time.Sleep(d)
}
