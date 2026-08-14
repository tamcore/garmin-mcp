// Package loginweb serves the one-shot browser login for a local deployment.
//
// The pages are server-rendered from templates embedded with go:embed. There is no
// third-party JavaScript, font, tracker, or CDN, and no script at all: the
// Content-Security-Policy permits nothing but this origin's own stylesheet and form
// target, so a page cannot fetch anything even if one were added by mistake.
//
// Three rules are structural rather than advisory:
//
//   - Credentials are used for exactly one Garmin login attempt and then dropped.
//     Nothing here logs them, stores them, or puts them in an error, and the log
//     vocabulary has no field one could reach. Go makes no promise about erasing
//     them from memory, and this package does not claim otherwise.
//   - The transaction lives entirely on the server. The browser holds a per-run
//     host-only cookie and a form token; the Garmin continuation capability never
//     leaves the process, and never appears in a path, a query, or a page.
//   - A fixed route without the run cookie and the form token answers a generic
//     404. Discoverability is not a security boundary.
//
// This is the loopback profile: plain HTTP on 127.0.0.1, so the cookie carries
// neither the __Host- prefix nor Secure, both of which require HTTPS. The remote
// profile is a different one and is not built here.
package loginweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// Request bounds. Every one is enforced before the value is used, because a bound
// checked afterwards has already let the work happen.
const (
	// MaxRequestBytes bounds one submitted form.
	MaxRequestBytes = 8 << 10
	// MaxEmailLen bounds the account field. It is the maximum address length in
	// RFC 5321.
	MaxEmailLen = 254
	// MaxPasswordLen bounds the password field.
	MaxPasswordLen = 256
	// MaxCodeLen bounds the one-time code field.
	MaxCodeLen = 16
)

// Lifetime and attempt bounds.
const (
	// DefaultTTL is the absolute lifetime of one login run. It is never extended.
	DefaultTTL = 10 * time.Minute
	// DefaultMaxAttempts bounds credential and code submissions per run.
	DefaultMaxAttempts = 5
	// DefaultShutdownGrace is how long the listener stays up after the transaction
	// ends, so the browser can load the final page before the process stops
	// answering.
	DefaultShutdownGrace = 2 * time.Second
)

// Configuration errors. Both are start-up failures: a login server that cannot be
// configured coherently must not bind a port.
var (
	// ErrNoAuthenticator reports a nil Config.Authenticator.
	ErrNoAuthenticator = errors.New("loginweb: no authenticator")
	// ErrInvalidConfig reports a negative lifetime, attempt budget, or grace.
	ErrInvalidConfig = errors.New("loginweb: invalid configuration")
)

// An Attempt is the outcome of one Garmin login or continuation call.
//
// It carries no credential. TransactionID is the opaque server-side continuation
// capability: it is a bearer credential for the pending login, it stays inside this
// process, and it never reaches a page or a log line.
type Attempt struct {
	// NeedsMFA reports that Garmin asked for a one-time code.
	NeedsMFA bool
	// TransactionID is the continuation capability, set only when NeedsMFA is.
	TransactionID string
	// MFAMethod is the delivery method Garmin named, for example "email".
	MFAMethod string
	// DeliveryUncertain reports a challenge whose code delivery is not confirmed.
	DeliveryUncertain bool
	// Strategy is the login flow that produced this outcome.
	Strategy string
}

// An Authenticator runs the Garmin login.
//
// The interface lives with its consumer and is deliberately narrow: it takes the
// two credential values and returns an [Attempt], so this package never sees a
// token, a cookie jar, or a principal. The caller binds the principal, which is why
// no page and no form can select an account.
type Authenticator interface {
	// Login submits credentials for the one account this run is linking.
	Login(ctx context.Context, email, password string) (Attempt, error)
	// CompleteMFA submits a one-time code for a pending continuation.
	CompleteMFA(ctx context.Context, transactionID, code string) (Attempt, error)
}

// Config configures a [Server]. Only Authenticator is required.
type Config struct {
	// Authenticator runs the Garmin login. Required.
	Authenticator Authenticator

	// TTL is the absolute lifetime of the run. Zero means DefaultTTL.
	TTL time.Duration

	// MaxAttempts bounds credential and code submissions. Zero means
	// DefaultMaxAttempts.
	MaxAttempts int

	// ShutdownGrace is how long the listener stays up after the transaction ends.
	// Zero means DefaultShutdownGrace.
	ShutdownGrace time.Duration

	// Now is the time source. Nil means time.Now.
	Now func() time.Time

	// Rand is the entropy source for the run cookie and the form token. Nil means
	// crypto/rand.
	Rand io.Reader

	// Logger receives redacted progress records. Nil records nothing, and no
	// record has a field in which a credential could travel.
	Logger *slog.Logger
}

// An Outcome is how one login run ended.
//
// It carries no credential and no token: the DI token set a successful login
// produced is written to the encrypted store by the authenticator, never handed
// back here.
type Outcome struct {
	// State is the login state the transaction ended in.
	State auth.State
	// Strategy is the login flow that reached that state.
	Strategy string
	// MFAMethod is the delivery method Garmin named, when it asked for a code.
	MFAMethod string
	// Err is why a run failed, already sanitized by the package that produced it.
	Err error
}

// Succeeded reports whether the run linked the account.
func (o Outcome) Succeeded() bool { return o.State == auth.StateAuthenticated }

// A Server serves one login run.
//
// It is single-use by construction: the transaction inside it reaches a terminal
// state exactly once, and every route refuses afterwards.
type Server struct {
	txn           *transaction
	authenticator Authenticator
	pages         *pageSet
	logger        *slog.Logger
	grace         time.Duration
	now           func() time.Time
	maxAttempts   int
}

// New validates cfg and returns the login server it describes.
func New(cfg Config) (*Server, error) {
	if cfg.Authenticator == nil {
		return nil, ErrNoAuthenticator
	}
	if cfg.TTL < 0 || cfg.MaxAttempts < 0 || cfg.ShutdownGrace < 0 {
		return nil, fmt.Errorf("%w: a lifetime, attempt budget, or grace period is negative",
			ErrInvalidConfig)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	pages, err := loadPages()
	if err != nil {
		return nil, err
	}
	txn, err := newTransaction(now(), orDuration(cfg.TTL, DefaultTTL), cfg.Rand)
	if err != nil {
		return nil, err
	}

	return &Server{
		txn:           txn,
		authenticator: cfg.Authenticator,
		pages:         pages,
		logger:        cfg.Logger,
		grace:         orDuration(cfg.ShutdownGrace, DefaultShutdownGrace),
		now:           now,
		maxAttempts:   orInt(cfg.MaxAttempts, DefaultMaxAttempts),
	}, nil
}

func orDuration(value, def time.Duration) time.Duration {
	if value == 0 {
		return def
	}
	return value
}

func orInt(value, def int) int {
	if value == 0 {
		return def
	}
	return value
}

// Done is closed when the transaction reaches a terminal state, whether it
// succeeded, failed, expired, or was cancelled.
func (s *Server) Done() <-chan struct{} { return s.txn.done }

// Outcome reports how the run ended. Before a terminal state it reports the state
// the transaction is in, so a caller never sees a zero value it could mistake for
// success.
func (s *Server) Outcome() Outcome { return s.txn.snapshot() }

// ListenLoopback binds a listener on 127.0.0.1 with a kernel-chosen port.
//
// The address is a literal loopback address rather than "localhost", which resolves
// to whatever the host's name service returns, and never a wildcard: a login form
// on 0.0.0.0 would accept credentials from the whole network.
func ListenLoopback() (net.Listener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("loginweb: binding the loopback listener: %w", err)
	}
	return listener, nil
}

// LoopbackURL renders the origin a browser must open for listener.
func LoopbackURL(listener net.Listener) string {
	return "http://" + listener.Addr().String()
}

// Serve runs the login server on listener until the transaction ends, the context
// is cancelled, or the run expires.
//
// The listener is always closed when Serve returns: a one-shot login flow that
// leaves a port open is a standing invitation to submit credentials to it. The
// grace period exists so the browser can load the final page before the process
// stops answering.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	server := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()

	s.awaitEnd(ctx)

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.grace)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)

	if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("loginweb: serving the login pages: %w", err)
	}
	return nil
}

// awaitEnd blocks until the transaction ends, the context is cancelled, or the run
// expires, recording the terminal state for the two cases no handler can see.
//
// The grace period is observed after a completed transaction only: after a
// cancellation there is no browser waiting for a final page.
func (s *Server) awaitEnd(ctx context.Context) {
	expiry := time.NewTimer(max(time.Until(s.txn.expires), 0))
	defer expiry.Stop()

	select {
	case <-s.txn.done:
		time.Sleep(s.grace)
	case <-ctx.Done():
		s.txn.cancel()
	case <-expiry.C:
		s.txn.expire()
	}
}
