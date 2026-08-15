package mcpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
)

// DefaultResourceMetadataPath is where RFC 9728 protected resource metadata is
// served when HTTPOptions names no other path.
const DefaultResourceMetadataPath = "/.well-known/oauth-protected-resource"

// The two URL schemes this transport will serve, and the label it reports itself
// under in a lifecycle log record.
const (
	schemeHTTP    = "http"
	schemeHTTPS   = "https"
	transportName = "http"
)

// HTTPOptions configures the Streamable HTTP transport.
//
// Everything the transport knows about its own identity is here, because none of
// it may be discovered from a request. PublicURL in particular is the canonical
// URL of this deployment: it is never derived from Host, from X-Forwarded-Host,
// or from anything else a caller can set.
//
// The zero value is not usable. Validation is total and happens once, in
// [NewHTTPTransport], so no request path has to re-decide whether a bind is safe.
type HTTPOptions struct {
	// PublicURL is the canonical absolute URL of the MCP endpoint. Its path is
	// the path the transport serves MCP on. Required.
	PublicURL string

	// BindAddress is the address the caller will listen on, in host:port form.
	// The transport does not listen — the composition root owns the listener and
	// its TLS configuration — but it must see the address to refuse a cleartext
	// public bind. Required.
	BindAddress string

	// ResourceMetadataPath is where the RFC 9728 document is served. Zero means
	// DefaultResourceMetadataPath.
	ResourceMetadataPath string

	// Authorizer authenticates every request. Required.
	Authorizer HTTPAuthorizer

	// AllowedOrigins is the browser Origin allowlist. Empty means every request
	// carrying an Origin is refused, which is CORS denied by default.
	AllowedOrigins []string

	// TrustedProxyCIDRs are the ranges whose forwarded headers are believed.
	// Empty means none are.
	TrustedProxyCIDRs []string

	// AllowInsecureCleartext permits a cleartext public bind. It is the explicit
	// development override and must never be set in production.
	AllowInsecureCleartext bool

	// Stateless serves each request with a temporary session and no session id.
	Stateless bool

	// EventStore enables event resumption. A resumed stream is still bound to
	// the authorization that created its session.
	EventStore mcp.EventStore

	// SessionTimeout closes idle sessions. Zero means they are never closed for
	// idleness.
	SessionTimeout time.Duration

	// MaxRequestBodyBytes bounds a request body. Zero means the SDK default.
	MaxRequestBodyBytes int64

	// Revocations reports withdrawn authorizations. Optional; without it a
	// revoked authorization is still refused at its next request, but an already
	// open stream is not torn down.
	Revocations RevocationSource

	// Readiness decides what [ReadinessPath] answers. Optional; without it the
	// readiness probe reports the process ready, because there is then no
	// dependency this transport was told to assert.
	Readiness ReadinessCheck
}

// An HTTPTransport serves MCP over Streamable HTTP.
//
// It is an http.Handler the composition root mounts on its own server, so TLS,
// timeouts, and the listener stay where the deployment configures them. It is
// immutable after construction and safe for concurrent use.
//
// Two things it does that the SDK handler alone does not:
//
//   - Every POST, GET and DELETE is authenticated from the Authorization header
//     before it reaches MCP code.
//   - Every session is bound to the authorization that created it, so a session
//     id can route a request but can never authorize one.
type HTTPTransport struct {
	server     *Server
	authorizer HTTPAuthorizer
	sessions   *sessionBindings
	origins    originGuard
	forwarded  forwardedTrust
	probes     probeHandler
	handler    http.Handler

	publicURL    string
	mcpPath      string
	metadataPath string
	revocations  RevocationSource
}

// NewHTTPTransport validates opts and assembles the transport.
//
// Assembly order is the security order, outermost first: origin, then routing,
// then authentication, then session binding, then the SDK handler. Nothing below
// authentication is reachable without a verified token, and nothing below the
// session guard can address a session belonging to another authorization.
func NewHTTPTransport(server *Server, opts HTTPOptions) (*HTTPTransport, error) {
	if server == nil {
		return nil, fmt.Errorf("server is nil: %w", ErrMissingDependency)
	}
	if opts.Authorizer == nil {
		return nil, fmt.Errorf("authorizer is nil: %w", ErrMissingDependency)
	}

	publicURL, err := validatePublicURL(opts.PublicURL)
	if err != nil {
		return nil, err
	}
	if err := validateBind(opts, publicURL); err != nil {
		return nil, err
	}
	origins, err := newOriginGuard(opts.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	forwarded, err := newForwardedTrust(opts.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}

	transport := &HTTPTransport{
		server:       server,
		authorizer:   opts.Authorizer,
		sessions:     newSessionBindings(),
		origins:      origins,
		forwarded:    forwarded,
		probes:       probeHandler{ready: opts.Readiness},
		publicURL:    publicURL.String(),
		mcpPath:      endpointPath(publicURL),
		metadataPath: metadataPath(opts.ResourceMetadataPath),
		revocations:  opts.Revocations,
	}
	transport.handler = origins.wrap(transport.route(opts))
	return transport, nil
}

// route dispatches on the exact path. An http.ServeMux is not used, because its
// subtree patterns would make the MCP endpoint answer for paths below it, and an
// endpoint that answers for more than its own URL is a routing surprise.
//
// The probes are last, and that ordering is the rule: a configured path always
// wins. A deployment that publishes MCP or its metadata document on /livez keeps
// serving it there, so adding these two routes cannot take an endpoint away from
// an operator who already chose that path.
func (t *HTTPTransport) route(opts HTTPOptions) http.Handler {
	metadata := t.authorizer.ProtectedResourceMetadataHandler()
	protected := t.authorizer.Middleware()(t.guardSession(newSDKHandler(t.server, opts)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case t.metadataPath:
			metadata.ServeHTTP(w, r)
		case t.mcpPath:
			protected.ServeHTTP(w, r)
		case LivenessPath, ReadinessPath:
			t.probes.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// newSDKHandler builds the official SDK's Streamable HTTP handler.
//
// The same server instance serves every session; getServer ignores its request
// argument deliberately, because choosing a server from request data would be
// exactly the kind of header-derived decision this transport refuses to make.
//
// CrossOriginProtection is left nil: it is deprecated at SDK v1.7.0, and this
// transport applies its own configured allowlist further out, where a refusal
// also covers the metadata route.
func newSDKHandler(server *Server, opts HTTPOptions) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server.MCPServer() },
		&mcp.StreamableHTTPOptions{
			Stateless:           opts.Stateless,
			EventStore:          opts.EventStore,
			SessionTimeout:      opts.SessionTimeout,
			MaxRequestBodyBytes: opts.MaxRequestBodyBytes,
		})
}

// ServeHTTP serves one Streamable HTTP request.
func (t *HTTPTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.handler.ServeHTTP(w, r)
}

// PublicURL returns the configured canonical URL of this deployment.
//
// It takes no request, and that is the point: there is no code path by which a
// Host or X-Forwarded-* header could influence the answer.
func (t *HTTPTransport) PublicURL() string { return t.publicURL }

// ClientIP returns the address to attribute a request to, honoring forwarded
// headers only from a configured proxy range.
func (t *HTTPTransport) ClientIP(r *http.Request) string { return t.forwarded.clientIP(r) }

// ProbeHandler returns the liveness and readiness probes as a standalone handler.
//
// The probes are already served on this transport's own handler, at
// [LivenessPath] and [ReadinessPath], and that is the deliberate default: they
// disclose nothing — a status code and a fixed word — so there is no secret for a
// second listener to protect, and a probe that needs its own listener is a probe
// most deployments will not get.
//
// This accessor exists for the deployment that wants them somewhere else anyway,
// typically an administrative listener bound to a private interface alongside
// whatever else that listener carries. It is the same handler and the same
// injected readiness check, so the two mount points can never disagree.
func (t *HTTPTransport) ProbeHandler() http.Handler { return t.probes }

// Run watches for revocations and terminates the sessions they cover, until ctx
// is done or the revocation channel closes.
//
// It is a plain wait when no RevocationSource is configured, so a caller can
// always run it and shut down on the same signal as the rest of the process.
func (t *HTTPTransport) Run(ctx context.Context) error {
	t.server.deps.Logger.Lifecycle(mcplog.LifecycleEvent{
		Phase:           "serving",
		Transport:       transportName,
		Mode:            t.server.deps.Policy.Mode().String(),
		ProtocolVersion: ProtocolVersion,
		ToolCount:       t.server.registry.Len(),
	})
	defer t.server.deps.Logger.Lifecycle(mcplog.LifecycleEvent{Phase: "shutdown", Transport: transportName})

	if t.revocations == nil {
		<-ctx.Done()
		return nil
	}

	events := t.revocations.Revocations(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}
			t.terminate(event)
		}
	}
}

// validatePublicURL parses the canonical URL and refuses anything that could not
// be an audience: a relative reference, a non-HTTP scheme, embedded credentials,
// a query, or a fragment.
//
// No error here renders the URL as it was given. A URL that carries userinfo
// carries a password, and a configuration error that prints it puts the password
// in the operator's terminal, their scrollback, and their process supervisor's
// log. url.URL.Redacted masks the password; the userinfo case reports the fault
// without the URL at all, because there the credential is the whole finding.
func validatePublicURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("no public URL is configured: %w", ErrInvalidHTTPOptions)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("the public URL is not a URL: %w", ErrInvalidHTTPOptions)
	}
	switch {
	case parsed.User != nil:
		return nil, fmt.Errorf("the public URL carries userinfo: %w", ErrInvalidHTTPOptions)
	case parsed.Scheme != schemeHTTP && parsed.Scheme != schemeHTTPS:
		return nil, fmt.Errorf("public URL %q has scheme %q, want http or https: %w",
			parsed.Redacted(), parsed.Scheme, ErrInvalidHTTPOptions)
	case parsed.Host == "":
		return nil, fmt.Errorf("public URL %q names no host: %w",
			parsed.Redacted(), ErrInvalidHTTPOptions)
	case parsed.RawQuery != "" || parsed.Fragment != "":
		return nil, fmt.Errorf("public URL %q carries a query or fragment: %w",
			parsed.Redacted(), ErrInvalidHTTPOptions)
	}
	return parsed, nil
}

// validateBind refuses a cleartext public deployment.
//
// Cleartext is permitted only where a network cannot observe it: a loopback
// public URL bound to a loopback address, which is the local development case.
// Anything else needs the explicit override, and setting that override in
// production is the operator publishing bearer tokens in plaintext.
func validateBind(opts HTTPOptions, publicURL *url.URL) error {
	if strings.TrimSpace(opts.BindAddress) == "" {
		return fmt.Errorf("no bind address is configured: %w", ErrInvalidHTTPOptions)
	}
	if publicURL.Scheme == schemeHTTPS || opts.AllowInsecureCleartext {
		return nil
	}
	if isLoopbackHost(publicURL.Hostname()) && isLoopbackBind(opts.BindAddress) {
		return nil
	}
	return fmt.Errorf(
		"public URL %q is cleartext and the deployment is not loopback-only: %w",
		publicURL.Redacted(), ErrInsecureBind)
}

// isLoopbackBind reports whether a bind address reaches only the loopback
// interface. A missing host — ":8080" — is a wildcard bind and is public.
func isLoopbackBind(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if strings.TrimSpace(host) == "" {
		return false
	}
	return isLoopbackHost(host)
}

// isLoopbackHost reports whether a host names the loopback interface. The
// unspecified addresses are explicitly not loopback: binding 0.0.0.0 or [::] is
// how a service ends up on every interface it has.
func isLoopbackHost(host string) bool {
	trimmed := strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(trimmed, "localhost") {
		return true
	}
	address := net.ParseIP(trimmed)
	return address != nil && address.IsLoopback()
}

// endpointPath is the path component of the public URL, defaulting to the root.
func endpointPath(publicURL *url.URL) string {
	if publicURL.Path == "" {
		return "/"
	}
	return publicURL.Path
}

// metadataPath normalizes the configured metadata path.
func metadataPath(configured string) string {
	trimmed := strings.TrimSpace(configured)
	if trimmed == "" {
		return DefaultResourceMetadataPath
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}
