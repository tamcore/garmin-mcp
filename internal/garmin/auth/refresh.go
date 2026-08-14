package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// DefaultRefreshWindow is how long before expiry a DI token is refreshed.
// Source: the 900-second margin in Client._token_expires_soon (client.py,
// 0.3.10).
const DefaultRefreshWindow = 15 * time.Minute

// RefreshConfig configures a Refresher. Hosts, Transport and Store are required.
type RefreshConfig struct {
	// Hosts builds the DI auth URL for one region.
	Hosts protocol.Hosts
	// Transport performs the HTTP requests.
	Transport Doer
	// Store persists the DI token set per principal.
	Store TokenStore
	// Clock is the time source. Nil means the system clock.
	Clock Clock
	// SafetyWindow is how long before expiry a token is refreshed. Zero means
	// DefaultRefreshWindow; a negative value is rejected.
	SafetyWindow time.Duration
	// Logger receives redacted progress records. Nil means slog.Default.
	Logger *slog.Logger
}

// Refresher keeps one principal's DI token set usable.
//
// Refreshes are serialized per principal: concurrent callers collapse into one
// in-flight refresh and all receive its result, so a burst of requests cannot
// rotate the refresh token several times over. Different principals never block
// each other.
//
// The rotated set is persisted with a compare-and-set against the version the
// refresh started from. A conflict means another writer already stored a newer
// set, so this one is discarded and the newer stored set is returned: a slow
// writer never clobbers a newer refresh token.
type Refresher struct {
	tokens tokenClient
	store  TokenStore
	clock  Clock
	window time.Duration
	logger *slog.Logger

	// mu guards flights. Each flight is the single in-progress refresh for one
	// principal, which is the stdlib equivalent of a singleflight group.
	mu      sync.Mutex
	flights map[string]*refreshFlight
}

// refreshFlight is one in-progress refresh. done is closed when set and err are
// final, so every waiter observes the same result.
type refreshFlight struct {
	done chan struct{}
	set  TokenSet
	err  error
}

// NewRefresher validates cfg and returns a Refresher.
func NewRefresher(cfg RefreshConfig) (*Refresher, error) {
	if cfg.Transport == nil || cfg.Store == nil {
		return nil, fmt.Errorf("garmin auth: new refresher: %w", ErrNotConfigured)
	}
	if cfg.Hosts.DITokenURL() == "" {
		return nil, fmt.Errorf("garmin auth: new refresher: unusable hosts: %w", ErrNotConfigured)
	}
	if cfg.SafetyWindow < 0 {
		return nil, fmt.Errorf("garmin auth: new refresher: negative safety window: %w", ErrNotConfigured)
	}

	clock := cfg.Clock
	if clock == nil {
		clock = systemClock{}
	}
	window := cfg.SafetyWindow
	if window == 0 {
		window = DefaultRefreshWindow
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Refresher{
		tokens:  tokenClient{hosts: cfg.Hosts, doer: cfg.Transport, clock: clock},
		store:   cfg.Store,
		clock:   clock,
		window:  window,
		logger:  logger,
		flights: make(map[string]*refreshFlight),
	}, nil
}

// Window reports the configured safety window.
func (r *Refresher) Window() time.Duration { return r.window }

// Fresh returns a usable token set for principal, refreshing it when it expires
// inside the safety window. An unknown expiry counts as expiring, so an opaque
// token is refreshed rather than trusted forever.
func (r *Refresher) Fresh(ctx context.Context, principal string) (TokenSet, error) {
	if principal == "" {
		return TokenSet{}, ErrMissingPrincipal
	}

	set, _, err := r.load(ctx, principal)
	if err != nil {
		return TokenSet{}, err
	}
	if !set.ExpiresWithin(r.clock.Now(), r.window) {
		return set, nil
	}
	return r.Refresh(ctx, principal)
}

// Refresh rotates principal's DI token set unconditionally.
//
// Concurrent calls for the same principal share one HTTP refresh: the first
// caller performs it, the rest wait for its result.
func (r *Refresher) Refresh(ctx context.Context, principal string) (TokenSet, error) {
	if principal == "" {
		return TokenSet{}, ErrMissingPrincipal
	}

	flight, leader := r.joinFlight(principal)
	if !leader {
		select {
		case <-flight.done:
			return flight.set, flight.err
		case <-ctx.Done():
			return TokenSet{}, ctx.Err()
		}
	}

	flight.set, flight.err = r.rotate(ctx, principal)
	r.finishFlight(principal, flight)
	return flight.set, flight.err
}

// joinFlight returns the flight for principal, creating it when none is running.
// The second result reports whether the caller must perform the refresh.
func (r *Refresher) joinFlight(principal string) (*refreshFlight, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.flights[principal]; ok {
		return existing, false
	}
	flight := &refreshFlight{done: make(chan struct{})}
	r.flights[principal] = flight
	return flight, true
}

// finishFlight publishes the result and retires the flight, so the next caller
// starts a new one.
func (r *Refresher) finishFlight(principal string, flight *refreshFlight) {
	r.mu.Lock()
	delete(r.flights, principal)
	r.mu.Unlock()

	close(flight.done)
}

// rotate performs one refresh and persists the result with a compare-and-set.
func (r *Refresher) rotate(ctx context.Context, principal string) (TokenSet, error) {
	current, version, err := r.load(ctx, principal)
	if err != nil {
		return TokenSet{}, err
	}

	rotated, err := r.tokens.refresh(ctx, current)
	if err != nil {
		return TokenSet{}, err
	}

	if _, err := r.store.Save(ctx, principal, rotated, version); err != nil {
		if !errors.Is(err, ErrVersionConflict) {
			return TokenSet{}, fmt.Errorf("garmin auth: save rotated tokens: %w", err)
		}
		return r.newerStoredSet(ctx, principal)
	}
	return rotated, nil
}

// newerStoredSet reports the set another writer stored while this refresh was in
// flight. Returning it rather than forcing the write is the point of the
// compare-and-set: the newer refresh token is the only usable one.
func (r *Refresher) newerStoredSet(ctx context.Context, principal string) (TokenSet, error) {
	r.logger.DebugContext(ctx, "garmin token refresh lost a compare-and-set, keeping the newer stored set")

	newer, _, err := r.load(ctx, principal)
	if err != nil {
		return TokenSet{}, err
	}
	return newer, nil
}

// load reads the stored set and version, wrapping a missing set so a caller can
// match ErrNoTokens.
func (r *Refresher) load(ctx context.Context, principal string) (TokenSet, int64, error) {
	set, version, err := r.store.Load(ctx, principal)
	if err != nil {
		return TokenSet{}, 0, fmt.Errorf("garmin auth: load stored tokens: %w", err)
	}
	return set, version, nil
}
