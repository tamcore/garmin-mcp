package testkit

import (
	"sync"
	"testing"
	"time"
)

func TestFakeClockSleepAdvancesAndRecords(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	clock := NewFakeClock(start)

	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}

	clock.Sleep(12 * time.Second)
	clock.Sleep(3 * time.Second)
	clock.Advance(time.Minute)

	want := start.Add(12*time.Second + 3*time.Second + time.Minute)
	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}

	sleeps := clock.Sleeps()
	if len(sleeps) != 2 || sleeps[0] != 12*time.Second || sleeps[1] != 3*time.Second {
		t.Fatalf("Sleeps() = %v, want [12s 3s]", sleeps)
	}

	sleeps[0] = time.Hour
	if clock.Sleeps()[0] != 12*time.Second {
		t.Fatal("Sleeps() exposed its backing array")
	}
}

func TestFakeClockNegativeSleepIsIgnored(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	clock := NewFakeClock(start)

	clock.Sleep(-time.Second)

	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	if len(clock.Sleeps()) != 0 {
		t.Fatalf("Sleeps() = %v, want empty", clock.Sleeps())
	}
}

func TestFakeClockIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	clock := NewFakeClock(time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC))

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			clock.Sleep(time.Second)
			_ = clock.Now()
			_ = clock.Sleeps()
		})
	}
	wg.Wait()

	if got := len(clock.Sleeps()); got != 50 {
		t.Fatalf("len(Sleeps()) = %d, want 50", got)
	}
}

func TestFakeClockSatisfiesClock(t *testing.T) {
	t.Parallel()

	var clock Clock = NewFakeClock(time.Unix(0, 0).UTC())
	clock.Sleep(time.Second)
	if clock.Now().IsZero() {
		t.Fatal("Now() returned the zero time")
	}
}
