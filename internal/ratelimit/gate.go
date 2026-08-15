package ratelimit

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// Default gate budgets.
//
// They are the layered limits the security brief asks for, and the two layers are
// sized for different attacks. The address budget is the tighter one, because a
// credential-stuffing run comes from a small number of addresses and every attempt
// costs it one. The client budget is looser, because one registered client
// legitimately serves many users through the same identifier, and a limit that
// bites there is an outage rather than a defence.
const (
	DefaultAddressPerMinute = 30
	DefaultAddressBurst     = 10
	DefaultClientPerMinute  = 120
	DefaultClientBurst      = 30
	DefaultMaxKeys          = 4096
)

// GateConfig is one gate's budget. Every field must be positive; [NewGate]
// refuses anything else rather than substituting a default for a value the
// operator set deliberately.
type GateConfig struct {
	// PerMinute is the sustained rate one key may spend.
	PerMinute int

	// Burst is the instantaneous allowance one key holds.
	Burst int

	// MaxKeys bounds how many keys hold a budget at once. When the table is
	// full the least recently used key is evicted.
	MaxKeys int
}

// A Decision is the outcome of one [Gate.Allow] call.
//
// It carries no reason text and names no key. A refusal that said which of the
// layered limits fired, or which key it fired for, would be an oracle: it would
// let a caller probe the limiter to learn whether an identifier exists.
type Decision struct {
	// Allowed reports whether the call may proceed.
	Allowed bool

	// RetryAfter is how long until the next token arrives. It is zero on an
	// allowed call and on a call that had no key to charge.
	RetryAfter time.Duration
}

// A Gate holds one budget per opaque string key.
//
// It is the sibling of [Limiter]: same token bucket, same bounded table, but
// keyed on a caller-supplied identifier — a client id or a client address —
// rather than on a resolved principal. That is what lets it sit in front of the
// authorization endpoints, where no principal exists yet because authenticating
// the caller is the very thing being attempted.
//
// Keys are held as their SHA-256 digest and never in their original form. The
// digest answers the only question the gate is asked — is this the same key —
// and it means no client identifier and no client address is held anywhere a
// panic dump, a log line, or a formatted error could reach.
//
// It is safe for concurrent use. A nil *Gate allows every call, which is how a
// deployment that configured no limit avoids a branch at each call site.
type Gate struct {
	cfg GateConfig
	now func() time.Time

	mu    sync.Mutex
	table *lruTable[[sha256.Size]byte, *budget]
}

// NewGate validates cfg and returns the Gate it describes.
//
// now may be nil, in which case time.Now is used. Injecting it keeps the budget
// tests deterministic and free of sleeps.
func NewGate(cfg GateConfig, now func() time.Time) (*Gate, error) {
	if cfg.PerMinute <= 0 {
		return nil, fmt.Errorf("gate rate %d per minute: %w", cfg.PerMinute, ErrInvalidBudget)
	}
	if cfg.Burst <= 0 {
		return nil, fmt.Errorf("gate burst %d: %w", cfg.Burst, ErrInvalidBudget)
	}
	if cfg.MaxKeys <= 0 {
		return nil, fmt.Errorf("gate key table size %d: %w", cfg.MaxKeys, ErrUnboundedKeys)
	}
	if now == nil {
		now = time.Now
	}

	return &Gate{
		cfg:   cfg,
		now:   now,
		table: newLRUTable[[sha256.Size]byte, *budget](cfg.MaxKeys),
	}, nil
}

// Allow charges one call to key's budget.
//
// An empty key is refused rather than given a shared anonymous budget. A shared
// bucket across unattributable callers is a cross-caller channel, and in front of
// an authorization endpoint it would also be a free denial of service: one caller
// could spend the anonymous budget and lock out every request the gate could not
// attribute.
func (g *Gate) Allow(key string) Decision {
	if g == nil {
		return Decision{Allowed: true}
	}
	if key == "" {
		return Decision{}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	bucket := g.table.touch(sha256.Sum256([]byte(key)), func() *budget {
		return newBudget(g.cfg.PerMinute, g.cfg.Burst, now)
	})

	allowed, retryAfter := bucket.take(now)
	return Decision{Allowed: allowed, RetryAfter: retryAfter}
}

// TrackedKeys reports how many keys currently hold a budget. It exists so a test
// and an operator can both see the table stays bounded.
func (g *Gate) TrackedKeys() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.table.len()
}
