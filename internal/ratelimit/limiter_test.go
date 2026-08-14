package ratelimit_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

// fakeClock is the injected time source. Every budget test advances it instead of
// sleeping, so the suite stays fast and deterministic under -race.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func mustPrincipal(t *testing.T, id string) identity.Principal {
	t.Helper()

	principal, err := identity.NewPrincipal(id)
	if err != nil {
		t.Fatalf("NewPrincipal(%q) returned error: %v", id, err)
	}
	return principal
}

func testConfig() ratelimit.Config {
	return ratelimit.Config{
		ReadPerMinute:  60,
		ReadBurst:      2,
		WritePerMinute: 60,
		WriteBurst:     1,
		MaxPrincipals:  8,
	}
}

func TestDefaultConfigIsValid(t *testing.T) {
	t.Parallel()

	if _, err := ratelimit.New(ratelimit.DefaultConfig(), nil); err != nil {
		t.Fatalf("New(DefaultConfig()) returned error: %v", err)
	}
}

func TestNewRejectsNonPositiveBudgets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		apply func(*ratelimit.Config)
	}{
		{"zero read rate", func(c *ratelimit.Config) { c.ReadPerMinute = 0 }},
		{"negative read rate", func(c *ratelimit.Config) { c.ReadPerMinute = -1 }},
		{"zero read burst", func(c *ratelimit.Config) { c.ReadBurst = 0 }},
		{"zero write rate", func(c *ratelimit.Config) { c.WritePerMinute = 0 }},
		{"zero write burst", func(c *ratelimit.Config) { c.WriteBurst = 0 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := testConfig()
			tc.apply(&cfg)
			if _, err := ratelimit.New(cfg, nil); !errors.Is(err, ratelimit.ErrInvalidBudget) {
				t.Fatalf("New error = %v, want ErrInvalidBudget", err)
			}
		})
	}
}

func TestNewRejectsAnUnboundedPrincipalTable(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.MaxPrincipals = 0

	if _, err := ratelimit.New(cfg, nil); !errors.Is(err, ratelimit.ErrUnboundedPrincipals) {
		t.Fatalf("New error = %v, want ErrUnboundedPrincipals", err)
	}
}

// A nil limiter passes everything through, so a deployment that configured no
// limits does not have to branch at every call site.
func TestNilLimiterPassesThrough(t *testing.T) {
	t.Parallel()

	var limiter *ratelimit.Limiter
	principal := mustPrincipal(t, "principal-a")

	for range 100 {
		result := limiter.Allow(principal, ratelimit.KindWrite)
		if !result.Allowed {
			t.Fatal("a nil limiter must allow every call")
		}
		if result.RetryAfter != 0 {
			t.Fatalf("RetryAfter = %v, want 0 from a nil limiter", result.RetryAfter)
		}
	}
	if limiter.TrackedPrincipals() != 0 {
		t.Fatal("a nil limiter must track nothing")
	}
}

func TestBurstIsAllowedThenTheBudgetIsExhausted(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	limiter, err := ratelimit.New(testConfig(), clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	principal := mustPrincipal(t, "principal-a")

	for i := range 2 {
		if result := limiter.Allow(principal, ratelimit.KindRead); !result.Allowed {
			t.Fatalf("call %d within the burst of 2 was limited", i+1)
		}
	}

	result := limiter.Allow(principal, ratelimit.KindRead)
	if result.Allowed {
		t.Fatal("the third call must exceed a burst of 2")
	}
	if result.RetryAfter <= 0 {
		t.Fatal("a limited result must carry an actionable RetryAfter")
	}
	if result.Kind != ratelimit.KindRead {
		t.Fatalf("Kind = %v, want read", result.Kind)
	}
	if result.Reason == "" {
		t.Fatal("a limited result must carry a reason")
	}
}

func TestBudgetRefillsOverTime(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	limiter, err := ratelimit.New(testConfig(), clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	principal := mustPrincipal(t, "principal-a")

	for range 2 {
		limiter.Allow(principal, ratelimit.KindRead)
	}
	if limiter.Allow(principal, ratelimit.KindRead).Allowed {
		t.Fatal("the budget should be exhausted")
	}

	// 60 per minute is one per second.
	clock.Advance(time.Second)
	if !limiter.Allow(principal, ratelimit.KindRead).Allowed {
		t.Fatal("one second must refill one read token")
	}
}

func TestRefillIsCappedAtTheBurst(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	limiter, err := ratelimit.New(testConfig(), clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	principal := mustPrincipal(t, "principal-a")

	limiter.Allow(principal, ratelimit.KindRead)
	clock.Advance(time.Hour)

	for i := range 2 {
		if result := limiter.Allow(principal, ratelimit.KindRead); !result.Allowed {
			t.Fatalf("call %d after a long idle period was limited", i+1)
		}
	}
	if limiter.Allow(principal, ratelimit.KindRead).Allowed {
		t.Fatal("an idle hour must not accumulate more than the burst")
	}
}

// The read and write budgets are separate, so exhausting reads must not block a
// write and the reverse.
func TestReadAndWriteBudgetsAreIndependent(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	limiter, err := ratelimit.New(testConfig(), clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	principal := mustPrincipal(t, "principal-a")

	for range 2 {
		limiter.Allow(principal, ratelimit.KindRead)
	}
	if limiter.Allow(principal, ratelimit.KindRead).Allowed {
		t.Fatal("the read budget should be exhausted")
	}

	if !limiter.Allow(principal, ratelimit.KindWrite).Allowed {
		t.Fatal("an exhausted read budget must not block a write")
	}
	if limiter.Allow(principal, ratelimit.KindWrite).Allowed {
		t.Fatal("the write burst of 1 should now be exhausted")
	}
	clock.Advance(time.Second)
	if !limiter.Allow(principal, ratelimit.KindRead).Allowed {
		t.Fatal("an exhausted write budget must not block a read")
	}
}

// This is the property the house single-user limiter lacks: one principal
// exhausting its budget must not affect another.
func TestBudgetsAreKeyedPerPrincipal(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	limiter, err := ratelimit.New(testConfig(), clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	first := mustPrincipal(t, "principal-a")
	second := mustPrincipal(t, "principal-b")

	for range 2 {
		limiter.Allow(first, ratelimit.KindRead)
	}
	if limiter.Allow(first, ratelimit.KindRead).Allowed {
		t.Fatal("the first principal's budget should be exhausted")
	}

	for i := range 2 {
		if result := limiter.Allow(second, ratelimit.KindRead); !result.Allowed {
			t.Fatalf("the second principal's call %d was limited by the first's usage", i+1)
		}
	}
	if limiter.TrackedPrincipals() != 2 {
		t.Fatalf("TrackedPrincipals() = %d, want 2", limiter.TrackedPrincipals())
	}
}

// An invalid principal must never share a budget with a valid one, and must never
// be silently allowed either.
func TestAnInvalidPrincipalIsRefused(t *testing.T) {
	t.Parallel()

	limiter, err := ratelimit.New(testConfig(), nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	var zero identity.Principal
	result := limiter.Allow(zero, ratelimit.KindRead)
	if result.Allowed {
		t.Fatal("an unresolved principal must not be granted a budget")
	}
	if limiter.TrackedPrincipals() != 0 {
		t.Fatal("an unresolved principal must not occupy a table slot")
	}
}

// The table is bounded, so a hostile stream of distinct principals cannot grow it
// without limit.
func TestPrincipalTableIsBounded(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	cfg := testConfig()
	cfg.MaxPrincipals = 2
	limiter, err := ratelimit.New(cfg, clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	for _, id := range []string{"principal-a", "principal-b", "principal-c", "principal-d"} {
		limiter.Allow(mustPrincipal(t, id), ratelimit.KindRead)
		clock.Advance(time.Millisecond)
	}

	if got := limiter.TrackedPrincipals(); got != 2 {
		t.Fatalf("TrackedPrincipals() = %d, want at most 2", got)
	}
}

// Eviction is least-recently-used, so the principal that is actively calling
// keeps its budget and an idle one is dropped first.
func TestEvictionDropsTheLeastRecentlyUsedPrincipal(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	cfg := testConfig()
	cfg.MaxPrincipals = 2
	limiter, err := ratelimit.New(cfg, clock.Now)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	idle := mustPrincipal(t, "principal-idle")
	active := mustPrincipal(t, "principal-active")
	newcomer := mustPrincipal(t, "principal-new")

	// Exhaust both principals' read burst of 2.
	for range 2 {
		limiter.Allow(idle, ratelimit.KindRead)
	}
	for range 2 {
		limiter.Allow(active, ratelimit.KindRead)
	}

	// Touch active again so idle is the least recently used, then admit a third
	// principal, which must evict idle rather than active.
	limiter.Allow(active, ratelimit.KindRead)
	limiter.Allow(newcomer, ratelimit.KindRead)

	if limiter.Allow(active, ratelimit.KindRead).Allowed {
		t.Fatal("the active principal's exhausted budget was evicted")
	}
	if !limiter.Allow(idle, ratelimit.KindRead).Allowed {
		t.Fatal("the least-recently-used principal should have been evicted and reset")
	}
}

func TestLimiterIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.ReadBurst = 100
	limiter, err := ratelimit.New(cfg, nil)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := range 8 {
		principal := mustPrincipal(t, "principal-"+string(rune('a'+i)))
		for range 16 {
			wg.Go(func() {
				if limiter.Allow(principal, ratelimit.KindRead).Allowed {
					allowed.Add(1)
				}
			})
		}
	}
	wg.Wait()

	// Every call is within each principal's burst of 100, so all must pass.
	if got := allowed.Load(); got != 128 {
		t.Fatalf("allowed %d of 128 calls", got)
	}
}

func TestKindStringIsCoarse(t *testing.T) {
	t.Parallel()

	cases := map[ratelimit.Kind]string{
		ratelimit.KindRead:  "read",
		ratelimit.KindWrite: "write",
		ratelimit.Kind(99):  "unknown",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(kind), got, want)
		}
	}
}
