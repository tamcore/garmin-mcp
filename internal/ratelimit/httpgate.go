package ratelimit

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// formContentType is the only body encoding the OAuth endpoints accept, and so
// the only one a client identifier can be read from.
const formContentType = "application/x-www-form-urlencoded"

// clientIDField is the RFC 6749 §2.3.1 form parameter naming the client.
const clientIDField = "client_id"

// maxFormPrefix bounds how much of a request body this gate will buffer to find a
// client identifier. It matches the authorization server's own form bound, so a
// body this gate declines to inspect is one the endpoint behind it will reject
// anyway. A larger body is passed through unbuffered and limited by address only.
const maxFormPrefix = 8 << 10

// rateLimitedError is the error code a refusal reports.
//
// RFC 6749 registers no code for "too fast", so the RFC 8628 §3.5 slow_down code
// is used: it is the one registered OAuth code that means exactly this, and a
// client that understands it already knows to back off. The HTTP status is the
// authoritative signal for everything else.
const rateLimitedError = "slow_down"

// rateLimitedBody is the whole refusal body. It is a constant, not a computed
// document, because there is nothing about the caller that may appear in it: not
// the client identifier, not the address, and not which of the two layered limits
// fired. Any of those would tell a prober something it did not already know.
const rateLimitedBody = `{"error":"` + rateLimitedError +
	`","error_description":"Too many requests. Retry later."}`

// An AddressFunc reports the address to attribute a request to.
//
// It is injected rather than derived here, and that is the security point. The
// caller supplies the transport's trusted-proxy logic, which believes a forwarded
// header only from a configured proxy range. A gate that read X-Forwarded-For
// itself would hand a fresh budget to anyone who can set a header, which is
// everyone.
type AddressFunc func(*http.Request) string

// HTTPGateConfig is the layered budget set.
type HTTPGateConfig struct {
	// PerAddress bounds attempts from one client address. It is the per-IP
	// login limit: the layer that bounds credential stuffing.
	PerAddress GateConfig

	// PerClient bounds attempts naming one client identifier, across every
	// address they arrive from.
	PerClient GateConfig
}

// DefaultHTTPGateConfig returns the shipped budgets.
func DefaultHTTPGateConfig() HTTPGateConfig {
	return HTTPGateConfig{
		PerAddress: GateConfig{
			PerMinute: DefaultAddressPerMinute,
			Burst:     DefaultAddressBurst,
			MaxKeys:   DefaultMaxKeys,
		},
		PerClient: GateConfig{
			PerMinute: DefaultClientPerMinute,
			Burst:     DefaultClientBurst,
			MaxKeys:   DefaultMaxKeys,
		},
	}
}

// An HTTPGate applies the layered limits in front of an HTTP handler.
//
// It exists for the authorization endpoints — token, revocation and the metadata
// documents — which are reachable without a credential and are therefore where an
// attacker gets to guess. Everything below it is unchanged: a refused request is
// answered here and never reaches the handler.
//
// It holds no client identifier and no address: both are charged through [Gate],
// which keeps only their digest.
type HTTPGate struct {
	addresses *Gate
	clients   *Gate
	addressOf AddressFunc
}

// NewHTTPGate validates cfg and returns the gate it describes.
//
// addressOf is required. Defaulting it to the peer address would be wrong behind
// a proxy and defaulting it to a header would be wrong everywhere, so a missing
// address source is a start-up failure rather than a guess.
//
// now may be nil, in which case time.Now is used.
func NewHTTPGate(
	cfg HTTPGateConfig, addressOf AddressFunc, now func() time.Time,
) (*HTTPGate, error) {
	if addressOf == nil {
		return nil, fmt.Errorf("no client address source: %w", ErrMissingAddressSource)
	}
	addresses, err := NewGate(cfg.PerAddress, now)
	if err != nil {
		return nil, fmt.Errorf("the per-address budget: %w", err)
	}
	clients, err := NewGate(cfg.PerClient, now)
	if err != nil {
		return nil, fmt.Errorf("the per-client budget: %w", err)
	}
	return &HTTPGate{addresses: addresses, clients: clients, addressOf: addressOf}, nil
}

// Middleware returns HTTP middleware that charges every request to both layers.
//
// A nil gate makes it a transparent pass-through, so a deployment that configured
// no limits needs no branch at the mount point.
func (g *HTTPGate) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if g == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request, clientID := g.identify(r)
			decision := g.charge(request, clientID)
			if !decision.Allowed {
				writeRateLimited(w, decision.RetryAfter)
				return
			}
			next.ServeHTTP(w, request)
		})
	}
}

// charge spends one token in each applicable layer.
//
// The address layer is charged first and always: it is the layer that still holds
// when a caller omits or invents a client identifier. The client layer is charged
// only when the request named a client, because charging an empty key would pool
// every anonymous request into one bucket that any single caller could empty.
func (g *HTTPGate) charge(r *http.Request, clientID string) Decision {
	if decision := g.addresses.Allow(g.addressOf(r)); !decision.Allowed {
		return decision
	}
	if clientID == "" {
		return Decision{Allowed: true}
	}
	return g.clients.Allow(clientID)
}

// identify returns the request to serve and the client identifier it names.
//
// The returned request is a copy whenever the body had to be read, so the
// original is never left drained: the handler behind this gate parses the same
// bytes it would have seen without the gate.
func (g *HTTPGate) identify(r *http.Request) (*http.Request, string) {
	if id, _, ok := r.BasicAuth(); ok && id != "" {
		return r, id
	}
	if r.Method != http.MethodPost || r.Body == nil || !isFormRequest(r) {
		return r, ""
	}
	return replayForm(r)
}

// replayForm buffers the head of a form body, reads the client identifier from
// it, and hands back a request whose body replays those bytes followed by the
// rest of the stream.
//
// A body longer than the bound is not buffered whole and yields no identifier.
// Buffering it would let an unauthenticated caller decide how much memory this
// process spends, which is the attack the bound exists to prevent.
func replayForm(r *http.Request) (*http.Request, string) {
	prefix, err := io.ReadAll(io.LimitReader(r.Body, maxFormPrefix+1))

	replayed := r.Clone(r.Context())
	replayed.Body = replayBody{
		Reader: io.MultiReader(bytes.NewReader(prefix), r.Body),
		Closer: r.Body,
	}

	if err != nil || len(prefix) > maxFormPrefix {
		return replayed, ""
	}
	values, parseErr := url.ParseQuery(string(prefix))
	if parseErr != nil {
		return replayed, ""
	}
	return replayed, values.Get(clientIDField)
}

// replayBody reads the buffered prefix and then the remaining stream, and closes
// the original body.
type replayBody struct {
	io.Reader
	io.Closer
}

// isFormRequest checks the media type, ignoring parameters such as a charset.
func isFormRequest(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == formContentType
}

// writeRateLimited answers a limited request.
//
// It is an RFC 6749 error object with a Retry-After header, not a generic error
// page: a client that gets JSON from this endpoint on every other outcome must
// not get HTML on this one. Nothing about the caller appears in it.
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	header := w.Header()
	header.Set("Content-Type", "application/json")
	header.Set("Cache-Control", "no-store")
	header.Set("Pragma", "no-cache")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = io.WriteString(w, rateLimitedBody)
}

// retryAfterSeconds rounds a delay up to whole seconds, never below one. Rounding
// down would advertise a retry that is still too early and invite a second
// refusal.
func retryAfterSeconds(retryAfter time.Duration) int {
	return max(1, int(math.Ceil(retryAfter.Seconds())))
}
