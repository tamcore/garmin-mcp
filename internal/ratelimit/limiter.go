// Package ratelimit bounds how fast one principal may call Garmin.
//
// The limiter is keyed per principal. This is the deliberate difference from the
// house single-user limiter, which keeps one global budget: in a multi-user
// deployment a global budget lets any caller deny service to every other caller,
// and it lets one account's Garmin rate-limit penalty become everyone's.
//
// Two further properties matter:
//
//   - The read and write budgets are independent, so a burst of writes cannot
//     starve reads.
//   - The principal table is bounded and evicts least-recently-used entries, so a
//     stream of distinct principals cannot grow it without limit.
//
// A nil *Limiter passes every call through, which is how a deployment that
// configured no limits avoids a branch at each call site.
package ratelimit

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// A Kind selects which of a principal's two budgets a call is charged to.
type Kind int

// The budget kinds. Write and destructive tools both charge the write budget:
// they are the calls that mutate the account, and giving the destructive tier its
// own bucket would hand it a fresh allowance after the write budget ran out.
const (
	// KindRead charges the read budget.
	KindRead Kind = iota + 1

	// KindWrite charges the write budget.
	KindWrite
)

// String returns the coarse label used in a refusal and in a log line.
func (k Kind) String() string {
	switch k {
	case KindRead:
		return "read"
	case KindWrite:
		return "write"
	default:
		return "unknown"
	}
}

// Default budgets. They are generous enough for interactive use and small enough
// that a runaway agent loop hits them before Garmin does.
const (
	DefaultReadPerMinute  = 120
	DefaultReadBurst      = 20
	DefaultWritePerMinute = 30
	DefaultWriteBurst     = 5
	DefaultMaxPrincipals  = 1024
)

// Config is the limiter configuration. Every field must be positive; New refuses
// anything else rather than substituting a default for a value the operator set
// deliberately.
type Config struct {
	// ReadPerMinute and ReadBurst are the sustained rate and the instantaneous
	// allowance of the read budget.
	ReadPerMinute int
	ReadBurst     int

	// WritePerMinute and WriteBurst are the same for the write budget.
	WritePerMinute int
	WriteBurst     int

	// MaxPrincipals bounds how many principals hold a budget at once. When the
	// table is full the least recently used entry is evicted.
	MaxPrincipals int
}

// DefaultConfig returns the shipped defaults.
func DefaultConfig() Config {
	return Config{
		ReadPerMinute:  DefaultReadPerMinute,
		ReadBurst:      DefaultReadBurst,
		WritePerMinute: DefaultWritePerMinute,
		WriteBurst:     DefaultWriteBurst,
		MaxPrincipals:  DefaultMaxPrincipals,
	}
}

// A Result is the outcome of one Allow call.
//
// Reason is caller-facing text and deliberately names the budget rather than the
// principal or the tool: the caller already knows which tool it called, and the
// principal identifier is personal data that has no place in a tool result.
type Result struct {
	Allowed    bool
	Kind       Kind
	RetryAfter time.Duration
	Reason     string
}

// budget is one token bucket.
type budget struct {
	tokens     float64
	perSecond  float64
	burst      float64
	lastRefill time.Time
}

func newBudget(perMinute, burst int, now time.Time) *budget {
	return &budget{
		tokens:     float64(burst),
		perSecond:  float64(perMinute) / 60,
		burst:      float64(burst),
		lastRefill: now,
	}
}

// take refills for elapsed time and consumes one token if any remain. When none
// remain it reports how long until the next one arrives.
func (b *budget) take(now time.Time) (bool, time.Duration) {
	if elapsed := now.Sub(b.lastRefill); elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed.Seconds()*b.perSecond)
		b.lastRefill = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	missing := 1 - b.tokens
	return false, time.Duration(missing / b.perSecond * float64(time.Second))
}

// entry holds one principal's two budgets plus its position in the LRU list.
type entry struct {
	read     *budget
	write    *budget
	lruEntry *list.Element
}

// A Limiter holds one pair of budgets per principal.
//
// It is safe for concurrent use. A nil *Limiter is a valid pass-through.
type Limiter struct {
	cfg Config
	now func() time.Time

	mu      sync.Mutex
	entries map[identity.Principal]*entry
	// lru orders principals by last use, least recent at the front.
	lru *list.List
}

// New validates cfg and returns the Limiter it describes.
//
// now may be nil, in which case time.Now is used. Injecting it keeps the budget
// tests deterministic and free of sleeps.
func New(cfg Config, now func() time.Time) (*Limiter, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}

	return &Limiter{
		cfg:     cfg,
		now:     now,
		entries: make(map[identity.Principal]*entry),
		lru:     list.New(),
	}, nil
}

func validateConfig(cfg Config) error {
	budgets := []struct {
		label     string
		perMinute int
		burst     int
	}{
		{"read", cfg.ReadPerMinute, cfg.ReadBurst},
		{"write", cfg.WritePerMinute, cfg.WriteBurst},
	}
	for _, b := range budgets {
		if b.perMinute <= 0 {
			return fmt.Errorf("%s rate %d per minute: %w", b.label, b.perMinute, ErrInvalidBudget)
		}
		if b.burst <= 0 {
			return fmt.Errorf("%s burst %d: %w", b.label, b.burst, ErrInvalidBudget)
		}
	}
	if cfg.MaxPrincipals <= 0 {
		return fmt.Errorf("max principals %d: %w", cfg.MaxPrincipals, ErrUnboundedPrincipals)
	}
	return nil
}

// Allow charges one call of kind to principal's budget.
//
// An unresolved principal is refused rather than given a shared anonymous budget:
// a shared bucket across unidentified callers would be a cross-principal channel,
// which is exactly what this package exists to prevent.
func (l *Limiter) Allow(principal identity.Principal, kind Kind) Result {
	if l == nil {
		return Result{Allowed: true, Kind: kind}
	}
	if !principal.IsValid() {
		return Result{
			Kind:   kind,
			Reason: "the request has no resolved principal, so no rate-limit budget applies to it",
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	tracked := l.touch(principal, now)

	bucket := tracked.read
	if kind == KindWrite {
		bucket = tracked.write
	}

	allowed, retryAfter := bucket.take(now)
	if allowed {
		return Result{Allowed: true, Kind: kind}
	}
	return Result{
		Kind:       kind,
		RetryAfter: retryAfter,
		Reason:     "the " + kind.String() + " rate-limit budget for this account is exhausted",
	}
}

// touch returns principal's entry, creating it if needed, and moves it to the
// most-recently-used end. Creating an entry may evict the least recently used one.
// The caller holds l.mu.
func (l *Limiter) touch(principal identity.Principal, now time.Time) *entry {
	if existing, ok := l.entries[principal]; ok {
		l.lru.MoveToBack(existing.lruEntry)
		return existing
	}

	for len(l.entries) >= l.cfg.MaxPrincipals {
		l.evictOldest()
	}

	created := &entry{
		read:  newBudget(l.cfg.ReadPerMinute, l.cfg.ReadBurst, now),
		write: newBudget(l.cfg.WritePerMinute, l.cfg.WriteBurst, now),
	}
	created.lruEntry = l.lru.PushBack(principal)
	l.entries[principal] = created
	return created
}

// evictOldest drops the least recently used entry. The caller holds l.mu.
//
// Eviction resets the evicted principal's budget, which is a deliberate accepted
// trade: the alternative — refusing a new principal because the table is full —
// would let one caller lock every other caller out entirely.
func (l *Limiter) evictOldest() {
	oldest := l.lru.Front()
	if oldest == nil {
		return
	}
	l.lru.Remove(oldest)
	principal, ok := oldest.Value.(identity.Principal)
	if !ok {
		return
	}
	delete(l.entries, principal)
}

// TrackedPrincipals reports how many principals currently hold a budget. It exists
// so a test and an operator can both see the table stays bounded.
func (l *Limiter) TrackedPrincipals() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
