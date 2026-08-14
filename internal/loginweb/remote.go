package loginweb

// This file is the remote profile: the browser login a public HTTPS deployment
// exposes, gated by an OAuth authorization transaction.
//
// It differs from the loopback profile in four ways, each of which is a security
// decision rather than a preference:
//
//   - The cookie is __Host- prefixed, Secure, HttpOnly, Path=/, without a Domain,
//     and SameSite=Lax, which is the strictest value a cross-site top-level
//     navigation from the client's application still delivers.
//   - Every response carries HSTS in addition to the shared browser headers.
//   - There are many concurrent transactions rather than one run, so the sessions
//     live in a bounded registry addressed by the digest of the capability.
//   - The flow ends at the client's already-validated redirect URI rather than at a
//     local page.
//
// The capability is only ever in a cookie. It is never in a path, a query, a page
// or a log line, because proxies and access logs capture the first two.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"time"
)

// RemoteCookieName is the transaction cookie. The __Host- prefix is what binds the
// cookie to this exact host with no Domain and Path=/, so a sibling host on the
// registrable domain can neither set nor read it.
const RemoteCookieName = "__Host-garmin_mcp_auth"

// RemoteAuthorizePath is the OAuth authorization endpoint this profile serves. It
// is the only route a client ever links to; every other route is reached by this
// server's own redirects.
const RemoteAuthorizePath = "/authorize"

// Remote defaults.
const (
	// DefaultMaxSessions bounds the in-memory transaction registry, so a flood of
	// authorization requests cannot grow the process without limit.
	DefaultMaxSessions = 256
	// DefaultHSTSMaxAge is the Strict-Transport-Security lifetime.
	DefaultHSTSMaxAge = 365 * 24 * time.Hour
)

// Remote configuration and transaction errors.
var (
	// ErrNoAuthorizations reports a nil RemoteConfig.Authorizations.
	ErrNoAuthorizations = errors.New("loginweb: no authorization server")
	// ErrNoTransaction reports a capability that addresses nothing. A probe is
	// shown a generic 404 for it, whatever the real reason was.
	ErrNoTransaction = errors.New("loginweb: no such transaction")
	// ErrTransactionExpired reports a transaction past its absolute lifetime.
	ErrTransactionExpired = errors.New("loginweb: the transaction expired")
)

// A Disclosure is the non-binding statement shown before any credential is entered:
// who is asking, where the answer goes, and for what.
//
// It is data, not markup, and every field has already been validated by the
// authorization server. This package renders it and adds nothing.
type Disclosure struct {
	// ClientID is the registered client identifier.
	ClientID string
	// ClientName is the operator-registered display name.
	ClientName string
	// RedirectURI is the exact registered redirect URI the code will be sent to.
	RedirectURI string
	// RedirectHost is that URI's host, which is the part a user can judge.
	RedirectHost string
	// Resource is the audience the token will be minted for.
	Resource string
	// Scopes are the scopes being requested.
	Scopes []string
}

// An Authorization is a validated authorization request together with the
// capability that addresses the transaction it opened.
type Authorization struct {
	// Capability is the opaque server-owned transaction capability. It belongs in
	// a cookie and nowhere else.
	Capability string
	// Disclosure is what the first page states.
	Disclosure Disclosure
	// ExpiresAt is the transaction's absolute deadline, which is never extended.
	ExpiresAt time.Time
}

// A Completion is a terminal authorization outcome: the URL the browser is sent to.
//
// It is always the client's already-validated redirect URI, carrying the client's
// original state byte for byte, and this package forwards it unchanged.
type Completion struct {
	// RedirectTo is the location the browser is sent to.
	RedirectTo string
}

// Authorizations is the OAuth authorization server this profile drives.
//
// The interface lives with its consumer and is deliberately narrow. Every method
// takes the capability as an opaque string, so this package never sees a client
// record, a PKCE challenge, a consent row, an authorization code or a Garmin token.
// A capability that addresses nothing, one that has expired and one that has already
// completed must all be reported as [ErrNoTransaction] or [ErrTransactionExpired],
// with no account information in either.
type Authorizations interface {
	// Begin validates an authorization request and opens a transaction. A refusal
	// that implements [Refusal] decides how it may be delivered; anything else is
	// rendered locally.
	Begin(ctx context.Context, query url.Values) (Authorization, error)
	// Disclose reports what the pages must state about a live transaction.
	Disclose(ctx context.Context, capability string) (Disclosure, error)
	// AttachPrincipal records the principal a completed Garmin login resolved to.
	AttachPrincipal(ctx context.Context, capability, principal string) error
	// Grant records consent and issues the authorization code, which makes the
	// transaction terminal.
	Grant(ctx context.Context, capability string) (Completion, error)
	// Deny ends the transaction without persisting anything, discarding whatever
	// the login produced.
	Deny(ctx context.Context, capability string) (Completion, error)
}

// A Refusal is an authorization request the server refused, together with the
// decision about how the refusal may be delivered.
//
// That decision is the point of the type: an error may be sent to a redirect URI
// only once the client and the exact registered redirect URI are both validated.
// Location is empty until then, and this package renders locally rather than
// guessing at a destination. Description must be fixed, sanitized text.
type Refusal interface {
	error
	// Status is the HTTP status a local render should use.
	Status() int
	// Description is the sanitized text that may be shown.
	Description() string
	// Location is the redirect target, or "" when the refusal must be rendered.
	Location() string
}

// RemoteConfig configures a [RemoteServer]. Authorizations and Authenticator are
// both required: a login server that cannot be configured coherently must not serve.
type RemoteConfig struct {
	// Authorizations is the OAuth authorization server. Required.
	Authorizations Authorizations
	// Authenticator runs the Garmin login. Required.
	Authenticator Authenticator

	// TTL caps one browser session's lifetime. The effective deadline is the
	// earlier of this and the authorization transaction's own expiry. Zero means
	// DefaultTTL.
	TTL time.Duration
	// MaxAttempts bounds credential and code submissions per transaction. Zero
	// means DefaultMaxAttempts.
	MaxAttempts int
	// MaxSessions bounds the in-memory registry. Zero means DefaultMaxSessions.
	MaxSessions int
	// HSTSMaxAge is the Strict-Transport-Security lifetime. Zero means
	// DefaultHSTSMaxAge.
	HSTSMaxAge time.Duration

	// Now is the time source. Nil means time.Now.
	Now func() time.Time
	// Rand is the entropy source for form tokens. Nil means crypto/rand.
	Rand io.Reader
	// Logger receives redacted progress records. Nil records nothing, and no
	// record has a field in which a credential could travel.
	Logger *slog.Logger
}

// A RemoteServer serves the transaction-gated browser login for a public HTTPS
// deployment.
//
// It is safe for concurrent use: transactions are independent, and the bounded
// registry is the only state they share.
type RemoteServer struct {
	authorizations Authorizations
	authenticator  Authenticator
	sessions       *sessionRegistry
	pages          *pageSet
	logger         *slog.Logger
	now            func() time.Time
	entropy        io.Reader
	ttl            time.Duration
	maxAttempts    int
	hsts           string
}

// NewRemote validates cfg and returns the remote login server it describes.
func NewRemote(cfg RemoteConfig) (*RemoteServer, error) {
	if cfg.Authorizations == nil {
		return nil, ErrNoAuthorizations
	}
	if cfg.Authenticator == nil {
		return nil, ErrNoAuthenticator
	}
	if cfg.TTL < 0 || cfg.MaxAttempts < 0 || cfg.MaxSessions < 0 || cfg.HSTSMaxAge < 0 {
		return nil, fmt.Errorf(
			"%w: a lifetime, attempt budget, session bound, or HSTS age is negative",
			ErrInvalidConfig)
	}

	pages, err := loadRemotePages()
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &RemoteServer{
		authorizations: cfg.Authorizations,
		authenticator:  cfg.Authenticator,
		sessions:       newSessionRegistry(orInt(cfg.MaxSessions, DefaultMaxSessions)),
		pages:          pages,
		logger:         cfg.Logger,
		now:            now,
		entropy:        cfg.Rand,
		ttl:            orDuration(cfg.TTL, DefaultTTL),
		maxAttempts:    orInt(cfg.MaxAttempts, DefaultMaxAttempts),
		hsts:           hstsHeader(orDuration(cfg.HSTSMaxAge, DefaultHSTSMaxAge)),
	}, nil
}

// log records one coarse progress line. The vocabulary is fixed text: there is no
// argument through which a credential, an account, or a capability could travel.
func (s *RemoteServer) log(ctx context.Context, message string) {
	if s.logger == nil {
		return
	}
	s.logger.InfoContext(ctx, "remote login transaction", slog.String("event", message))
}
