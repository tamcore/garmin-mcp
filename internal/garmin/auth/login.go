package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Jitter picks the anti-WAF delay inside the protocol's pacing window. It is
// injected so a test can pin the value and observe pacing without waiting.
type Jitter func(minDelay, maxDelay time.Duration) time.Duration

// Config configures an Authenticator. Hosts, Transport, Store and Registry are
// required; the rest take documented defaults.
type Config struct {
	// Hosts builds the Garmin URLs for one region. Obtain it from
	// protocol.NewHosts or protocol.ParseDomain, and never from a raw string.
	Hosts protocol.Hosts
	// Transport performs the HTTP requests.
	Transport Doer
	// Store persists the DI token set per principal.
	Store TokenStore
	// Registry holds pending MFA transactions.
	Registry *Registry
	// Clock is the time source. Nil means the system clock.
	Clock Clock
	// Sleeper waits out the anti-WAF pacing. Nil means the system clock.
	Sleeper Sleeper
	// Jitter picks the delay inside the pacing window. Nil means a
	// crypto/rand-backed uniform pick.
	Jitter Jitter
	// Logger receives redacted progress records. Nil means slog.Default.
	Logger *slog.Logger
}

// Authenticator runs the Garmin login state machine over a pluggable transport.
//
// It holds no per-login mutable state: every login builds its own session and
// state machine, and a pending MFA continuation lives in the Registry under its
// own capability. Two concurrent logins therefore cannot observe or overwrite
// each other, which is the failure upstream 0.3.10 had to fix.
type Authenticator struct {
	hosts    protocol.Hosts
	doer     Doer
	tokens   tokenClient
	store    TokenStore
	registry *Registry
	clock    Clock
	sleeper  Sleeper
	jitter   Jitter
	logger   *slog.Logger
}

// NewAuthenticator validates cfg and returns an Authenticator.
func NewAuthenticator(cfg Config) (*Authenticator, error) {
	if cfg.Transport == nil || cfg.Store == nil || cfg.Registry == nil {
		return nil, fmt.Errorf("garmin auth: new authenticator: %w", ErrNotConfigured)
	}
	if cfg.Hosts.SSOBase() == "" || cfg.Hosts.DITokenURL() == "" {
		// The zero Hosts yields empty URLs, which must fail closed rather than
		// silently address the default region.
		return nil, fmt.Errorf("garmin auth: new authenticator: unusable hosts: %w", ErrNotConfigured)
	}

	authenticator := &Authenticator{
		hosts:    cfg.Hosts,
		doer:     cfg.Transport,
		store:    cfg.Store,
		registry: cfg.Registry,
		clock:    cfg.Clock,
		sleeper:  cfg.Sleeper,
		jitter:   cfg.Jitter,
		logger:   cfg.Logger,
	}
	if authenticator.clock == nil {
		authenticator.clock = systemClock{}
	}
	if authenticator.sleeper == nil {
		authenticator.sleeper = systemClock{}
	}
	if authenticator.jitter == nil {
		authenticator.jitter = uniformDelay
	}
	if authenticator.logger == nil {
		authenticator.logger = slog.Default()
	}
	authenticator.tokens = tokenClient{
		hosts: authenticator.hosts,
		doer:  authenticator.doer,
		clock: authenticator.clock,
	}
	return authenticator, nil
}

// Login runs the strategy fallback chain for principal.
//
// The chain is mobile iOS, then the SSO embed widget, then the portal, and the
// classifier decides each step: a definitive invalid-credential or locked verdict
// stops immediately, while a bot challenge, a rate limit, a temporary failure, an
// unrecognized response and a session the API tier refuses all fall through to
// the next strategy. Source: Client.login (client.py, 0.3.10).
//
// On an MFA challenge it returns a Result whose TransactionID must be handed back
// to CompleteMFA. creds is not retained: no field of this package holds a
// password.
func (a *Authenticator) Login(ctx context.Context, principal string, creds Credentials) (Result, error) {
	if principal == "" {
		return failedResult(""), ErrMissingPrincipal
	}
	if creds.IsZero() {
		return failedResult(""), ErrMissingCredentials
	}

	var lastErr error
	for _, strategy := range Strategies() {
		result, err, decided := a.attemptStrategy(ctx, principal, strategy, creds)
		if decided {
			return result, err
		}
		a.logger.DebugContext(ctx, "garmin login strategy failed, falling through",
			slog.String("strategy", strategy.String()), slog.Any("error", err))
		lastErr = err
	}

	if lastErr == nil {
		lastErr = ErrNotConfigured
	}
	return failedResult(""), fmt.Errorf("garmin auth: login: %w: %w", ErrLoginExhausted, lastErr)
}

// attemptStrategy runs one strategy and interprets its verdict. The third result
// reports whether the chain is decided: true ends the chain with the returned
// result and error, false means fall through to the next strategy.
func (a *Authenticator) attemptStrategy(
	ctx context.Context,
	principal string,
	strategy StrategyName,
	creds Credentials,
) (Result, error, bool) {
	step, err := a.runStrategy(ctx, strategy, creds)
	if err != nil {
		return failedResult(strategy), err, false
	}

	switch step.class.Outcome() {
	case protocol.OutcomeSuccess:
		if err := a.completeLogin(ctx, principal, step.class, step.serviceURL); err != nil {
			// A rejected or unverifiable session says nothing about the
			// password, so the next strategy still gets a turn.
			return failedResult(strategy), err, false
		}
		return authenticatedResult(strategy), nil, true

	case protocol.OutcomeMFARequired:
		result, err := a.beginMFA(principal, strategy, step)
		return result, err, true

	default:
		verdict := step.class.Err(strategy.loginOp(), strategy.loginEndpoint(), nil)
		if step.class.Outcome().StopsFallback() {
			return failedResult(strategy), verdict, true
		}
		return failedResult(strategy), verdict, false
	}
}

// completeLogin turns a success verdict into a stored, validated token set: the
// CAS ticket is exchanged for DI tokens, the candidate session is validated
// against the API tier, and only then is it persisted.
func (a *Authenticator) completeLogin(
	ctx context.Context,
	principal string,
	class protocol.Classification,
	serviceURL string,
) error {
	ticket := class.ServiceTicket()
	if ticket == "" {
		return ErrMissingServiceTicket
	}

	set, err := a.tokens.exchangeTicket(ctx, ticket, serviceURL)
	if err != nil {
		return err
	}
	if err := a.tokens.validateSession(ctx, set); err != nil {
		return err
	}
	return a.saveTokens(ctx, principal, set)
}

// saveTokens stores set with a compare-and-set against the version it just read.
//
// A conflict means another writer stored a set in between. A fresh interactive
// login is the newer fact, so the version is re-read once and the write retried;
// a second conflict is reported rather than forced.
func (a *Authenticator) saveTokens(ctx context.Context, principal string, set TokenSet) error {
	version, err := a.storedVersion(ctx, principal)
	if err != nil {
		return err
	}

	if _, err := a.store.Save(ctx, principal, set, version); err != nil {
		if !errors.Is(err, ErrVersionConflict) {
			return fmt.Errorf("garmin auth: save tokens: %w", err)
		}
		if version, err = a.storedVersion(ctx, principal); err != nil {
			return err
		}
		if _, err := a.store.Save(ctx, principal, set, version); err != nil {
			return fmt.Errorf("garmin auth: save tokens: %w", err)
		}
	}
	return nil
}

// storedVersion reports the current stored version, or zero when nothing is
// stored.
func (a *Authenticator) storedVersion(ctx context.Context, principal string) (int64, error) {
	_, version, err := a.store.Load(ctx, principal)
	switch {
	case err == nil:
		return version, nil
	case errors.Is(err, ErrNoTokens):
		return 0, nil
	default:
		return 0, fmt.Errorf("garmin auth: load stored tokens: %w", err)
	}
}

// uniformDelay picks a delay uniformly from [minDelay, maxDelay]. It draws from
// crypto/rand and, if that fails, waits the maximum: pacing exists to look less
// like a bot, so the safe fallback is slower, not faster.
func uniformDelay(minDelay, maxDelay time.Duration) time.Duration {
	if maxDelay <= minDelay {
		return minDelay
	}

	span := int64(maxDelay-minDelay) + 1
	picked, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return maxDelay
	}
	return minDelay + time.Duration(picked.Int64())
}
