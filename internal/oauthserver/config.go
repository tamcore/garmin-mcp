package oauthserver

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Lifetime defaults and ceilings.
//
// The ceiling on a code lifetime is normative: the security brief and the MCP
// authorization guidance both cap an authorization code at five minutes, so a
// configuration that asks for more is a configuration error rather than something
// to clamp silently.
const (
	// DefaultCodeTTL is how long an authorization code lives. It is far below the
	// ceiling because a code is redeemed by a program, immediately, over a
	// connection the client already has open.
	DefaultCodeTTL = 60 * time.Second
	// MaxCodeTTL is the hard ceiling on a code lifetime.
	MaxCodeTTL = 5 * time.Minute
	// DefaultAccessTokenTTL keeps an access token short-lived, so revocation takes
	// effect quickly even though verification is a local lookup.
	DefaultAccessTokenTTL = 15 * time.Minute
	// DefaultRefreshTokenTTL bounds a token family's life. Rotation on every use
	// means an active client keeps working; an idle one authorizes again.
	DefaultRefreshTokenTTL = 30 * 24 * time.Hour
	// DefaultTransactionTTL bounds an authorization transaction, which has to last
	// a human login including MFA, and no longer.
	DefaultTransactionTTL = 10 * time.Minute
	// MaxTransactionTTL is the hard ceiling on a transaction lifetime.
	MaxTransactionTTL = 30 * time.Minute
)

// A Config is the deployment-specific half of the authorization server.
//
// Every URL is explicit. None is ever derived from a request's Host header or an
// X-Forwarded-* header, because a metadata document or a token audience built from
// an attacker-controlled header is a token-confusion vulnerability.
type Config struct {
	// Issuer is the exact issuer identifier, as it appears in the authorization
	// server metadata. It must be an https origin with no query and no fragment.
	Issuer string
	// Resource is the canonical RFC 8707 resource indicator of the MCP endpoint:
	// the audience every token this server issues is minted for.
	Resource string
	// AuthorizationEndpoint and TokenEndpoint are the absolute URLs advertised in
	// the authorization server metadata.
	AuthorizationEndpoint string
	TokenEndpoint         string
	// RevocationEndpoint is the absolute URL of the RFC 7009 endpoint. It is
	// optional; when empty, no revocation endpoint is advertised.
	RevocationEndpoint string
	// ResourceMetadataURL is the absolute URL of the RFC 9728 protected resource
	// metadata document, named in every WWW-Authenticate challenge.
	ResourceMetadataURL string
	// ResourceName is the human-readable resource name in that document.
	ResourceName string
	// ScopesSupported is the space-delimited set of scopes this deployment
	// advertises. It is the outer bound on what any client can be granted.
	ScopesSupported string

	// The lifetimes. Zero means the default; negative is an error.
	CodeTTL         time.Duration
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	TransactionTTL  time.Duration
}

// Deps are the collaborators the server needs.
type Deps struct {
	// Store is persistence. It is required.
	Store Store
	// Now is the clock. It is optional and defaults to time.Now; a test injects a
	// movable one. It exists so no expiry check in this package reads the wall
	// clock directly.
	Now func() time.Time
}

// A Server is the OAuth protocol surface: the authorization-server role that
// issues codes and tokens, and the protected-resource role that verifies them.
//
// It is immutable after construction and safe for concurrent use. All mutable state
// lives in the Store, which is where the compare-and-set guarantees are.
type Server struct {
	issuer                string
	resource              Resource
	authorizationEndpoint string
	tokenEndpoint         string
	revocationEndpoint    string
	resourceMetadataURL   string
	resourceName          string
	scopesSupported       ScopeSet

	codeTTL         time.Duration
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	transactionTTL  time.Duration

	store Store
	clock func() time.Time
}

// New validates cfg and deps and returns the server they describe.
//
// Validation is total and happens once, at start-up: after New returns, no request
// path has to wonder whether a URL is safe or a lifetime is sane.
func New(cfg Config, deps Deps) (*Server, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("no store: %w", ErrInvalidConfig)
	}
	issuer, err := validateIssuer(cfg.Issuer)
	if err != nil {
		return nil, err
	}
	resource, err := ParseResource(cfg.Resource)
	if err != nil {
		return nil, err
	}
	endpoints, err := validateEndpoints(cfg)
	if err != nil {
		return nil, err
	}
	scopes, err := validateSupportedScopes(cfg.ScopesSupported)
	if err != nil {
		return nil, err
	}
	ttls, err := validateTTLs(cfg)
	if err != nil {
		return nil, err
	}
	clock := deps.Now
	if clock == nil {
		clock = time.Now
	}
	return &Server{
		issuer:                issuer,
		resource:              resource,
		authorizationEndpoint: endpoints.authorization,
		tokenEndpoint:         endpoints.token,
		revocationEndpoint:    endpoints.revocation,
		resourceMetadataURL:   endpoints.resourceMetadata,
		resourceName:          cfg.ResourceName,
		scopesSupported:       scopes,
		codeTTL:               ttls.code,
		accessTokenTTL:        ttls.access,
		refreshTokenTTL:       ttls.refresh,
		transactionTTL:        ttls.transaction,
		store:                 deps.Store,
		clock:                 clock,
	}, nil
}

// validateIssuer applies RFC 8414 §2: the issuer identifier is an https URL with no
// query and no fragment. A trailing slash on the bare root is trimmed, so the value
// compares exactly against a client's expectation either way.
func validateIssuer(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("no issuer: %w", ErrInvalidConfig)
	}
	if err := checkURIShape(raw, ErrInvalidConfig); err != nil {
		return "", err
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("issuer does not parse: %w", ErrInvalidConfig)
	}
	if !strings.HasPrefix(raw, schemeHTTPS+"://") {
		return "", fmt.Errorf("issuer must be an https URL: %w", ErrInvalidConfig)
	}
	if parsed.User != nil || parsed.Host == "" {
		return "", fmt.Errorf("issuer must be a bare https origin: %w", ErrInvalidConfig)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", fmt.Errorf("issuer must not carry a query: %w", ErrInvalidConfig)
	}
	return strings.TrimSuffix(raw, "/"), nil
}

type endpointSet struct {
	authorization    string
	token            string
	revocation       string
	resourceMetadata string
}

func validateEndpoints(cfg Config) (endpointSet, error) {
	required := map[string]string{
		"authorization endpoint":     cfg.AuthorizationEndpoint,
		"token endpoint":             cfg.TokenEndpoint,
		"resource metadata document": cfg.ResourceMetadataURL,
	}
	for label, raw := range required {
		if err := validateEndpointURL(raw, label, true); err != nil {
			return endpointSet{}, err
		}
	}
	if err := validateEndpointURL(cfg.RevocationEndpoint, "revocation endpoint", false); err != nil {
		return endpointSet{}, err
	}
	return endpointSet{
		authorization:    cfg.AuthorizationEndpoint,
		token:            cfg.TokenEndpoint,
		revocation:       cfg.RevocationEndpoint,
		resourceMetadata: cfg.ResourceMetadataURL,
	}, nil
}

// validateEndpointURL requires an absolute https URL, or a loopback http URL so a
// local single-user deployment works without a certificate.
func validateEndpointURL(raw, label string, required bool) error {
	if raw == "" {
		if required {
			return fmt.Errorf("no %s: %w", label, ErrInvalidConfig)
		}
		return nil
	}
	if err := checkURIShape(raw, ErrInvalidConfig); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s does not parse: %w", label, ErrInvalidConfig)
	}
	if err := checkOrigin(raw, parsed, ErrInvalidConfig); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

func validateSupportedScopes(raw string) (ScopeSet, error) {
	scopes, err := ParseScopeSet(raw)
	if err != nil {
		return ScopeSet{}, err
	}
	if scopes.IsEmpty() {
		return ScopeSet{}, fmt.Errorf("no supported scopes: %w", ErrInvalidConfig)
	}
	return scopes, nil
}

type ttlSet struct {
	code        time.Duration
	access      time.Duration
	refresh     time.Duration
	transaction time.Duration
}

func validateTTLs(cfg Config) (ttlSet, error) {
	for label, ttl := range map[string]time.Duration{
		"code lifetime":         cfg.CodeTTL,
		"access token lifetime": cfg.AccessTokenTTL,
		"refresh token":         cfg.RefreshTokenTTL,
		"transaction lifetime":  cfg.TransactionTTL,
	} {
		if ttl < 0 {
			return ttlSet{}, fmt.Errorf("%s is negative: %w", label, ErrInvalidConfig)
		}
	}
	set := ttlSet{
		code:        orDefault(cfg.CodeTTL, DefaultCodeTTL),
		access:      orDefault(cfg.AccessTokenTTL, DefaultAccessTokenTTL),
		refresh:     orDefault(cfg.RefreshTokenTTL, DefaultRefreshTokenTTL),
		transaction: orDefault(cfg.TransactionTTL, DefaultTransactionTTL),
	}
	if set.code > MaxCodeTTL {
		return ttlSet{}, fmt.Errorf("code lifetime %v exceeds the %v ceiling: %w",
			set.code, MaxCodeTTL, ErrInvalidConfig)
	}
	if set.transaction > MaxTransactionTTL {
		return ttlSet{}, fmt.Errorf("transaction lifetime %v exceeds the %v ceiling: %w",
			set.transaction, MaxTransactionTTL, ErrInvalidConfig)
	}
	if set.access > set.refresh {
		return ttlSet{}, fmt.Errorf(
			"access token lifetime %v outlives the refresh token lifetime %v: %w",
			set.access, set.refresh, ErrInvalidConfig)
	}
	return set, nil
}

func orDefault(configured, fallback time.Duration) time.Duration {
	if configured == 0 {
		return fallback
	}
	return configured
}

// Issuer returns the exact issuer identifier.
func (s *Server) Issuer() string { return s.issuer }

// Resource returns the canonical resource indicator every token is minted for.
func (s *Server) Resource() Resource { return s.resource }

// ScopesSupported returns the scopes this deployment advertises.
func (s *Server) ScopesSupported() ScopeSet { return s.scopesSupported }

// CodeTTL returns the authorization code lifetime.
func (s *Server) CodeTTL() time.Duration { return s.codeTTL }

// AccessTokenTTL returns the access token lifetime.
func (s *Server) AccessTokenTTL() time.Duration { return s.accessTokenTTL }

// RefreshTokenTTL returns the refresh token lifetime.
func (s *Server) RefreshTokenTTL() time.Duration { return s.refreshTokenTTL }

// TransactionTTL returns the authorization transaction lifetime.
func (s *Server) TransactionTTL() time.Duration { return s.transactionTTL }

// now reads the injected clock. Every expiry decision in this package goes through
// it, so a test can move time without sleeping.
func (s *Server) now() time.Time { return s.clock() }
