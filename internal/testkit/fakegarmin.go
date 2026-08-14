// Package testkit provides a scripted fake Garmin Connect server for tests.
//
// Every host the protocol package knows about is mapped onto one
// httptest.Server: obtain the base URLs with Server.Hosts or Server.Overrides
// and inject them into the client under test. All fixtures are synthetic.
//
// Reaching the real service is not merely unlikely, it is refused. Server.Doer
// is the package's only request path and it returns a Doer, an interface with a
// single Do method and no exported fields. Every request and every redirect hop
// whose scheme and host differ from Server.BaseURL fails with an
// *OffOriginError before DNS resolution or dial, and no caller can reach past
// the interface to remove that check.
package testkit

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// MaxRecordedBodyBytes bounds how much of a request body is recorded.
const MaxRecordedBodyBytes = 1 << 16

// Behavior is one scripted response.
type Behavior struct {
	// Status is the HTTP status code. Zero means 200.
	Status int
	// ContentType is the response media type. Empty means text/plain.
	ContentType string
	// Header holds extra response headers, for example Retry-After.
	Header http.Header
	// Body is the response body, written verbatim.
	Body string
	// Delay stalls the handler before responding, for timeout tests.
	Delay time.Duration
}

// RecordedRequest is one request the fake received.
type RecordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// Script maps a request path to the queue of behaviors to serve for it. It is an
// immutable value: With returns a new Script.
type Script struct {
	steps map[string][]Behavior
}

// NewScript returns an empty Script.
func NewScript() Script {
	return Script{steps: map[string][]Behavior{}}
}

// With returns a copy of s where path serves the given behaviors in order,
// replacing any behaviors previously scripted for that path. Once the queue is
// drained the last behavior repeats.
func (s Script) With(path string, behaviors ...Behavior) Script {
	out := Script{steps: make(map[string][]Behavior, len(s.steps)+1)}
	for key, queue := range s.steps {
		out.steps[key] = append([]Behavior(nil), queue...)
	}
	out.steps[path] = append([]Behavior(nil), behaviors...)
	return out
}

// Server is a scripted fake Garmin Connect endpoint set.
type Server struct {
	tb    testing.TB
	inner *httptest.Server

	mu       sync.Mutex
	steps    map[string][]Behavior
	cursor   map[string]int
	requests []RecordedRequest
}

// NewServer starts a fake Garmin server serving script and registers cleanup.
func NewServer(tb testing.TB, script Script) *Server {
	tb.Helper()

	srv := &Server{
		tb:     tb,
		steps:  make(map[string][]Behavior, len(script.steps)),
		cursor: make(map[string]int, len(script.steps)),
	}
	for path, queue := range script.steps {
		srv.steps[path] = append([]Behavior(nil), queue...)
	}

	srv.inner = httptest.NewServer(http.HandlerFunc(srv.serve))
	tb.Cleanup(srv.Close)
	return srv
}

// BaseURL is the fake server's origin, without a trailing slash.
func (s *Server) BaseURL() string { return s.inner.URL }

// Doer returns a fresh request path that can reach this fake server and nothing
// else.
//
// Guaranteed, not merely documented: every request whose scheme and host differ
// from BaseURL, and every redirect hop that leaves that origin, fails with an
// *OffOriginError before any DNS lookup or dial. The guard is enforced at three
// points a caller cannot reach, because the returned Doer is an interface over
// an unexported struct with no exported fields and no method but Do: the
// pre-dispatch check in Do, the round tripper, and the redirect policy.
//
// Not guaranteed: the returned Doer constrains only requests made through it.
// Code that builds its own http.Client is unaffected, and the guard compares
// scheme and host only, so it says nothing about paths or request contents.
//
// Each call returns an independent Doer configured by opts, so per-test
// settings such as WithTimeout never leak between tests.
func (s *Server) Doer(opts ...DoerOption) Doer {
	cfg := doerConfig{}
	for _, opt := range opts {
		cfg = opt(cfg)
	}

	inner := s.inner.Client()
	origin := s.BaseURL()
	return guardedDoer{origin: origin, client: &http.Client{
		Transport:     originGuard{origin: origin, next: inner.Transport},
		CheckRedirect: checkRedirect(origin),
		Jar:           inner.Jar,
		Timeout:       cfg.timeout,
	}}
}

// Overrides maps every Garmin host onto the fake server.
func (s *Server) Overrides() protocol.Overrides {
	base := s.BaseURL()
	return protocol.Overrides{
		SSO:               base,
		Connect:           base,
		ConnectAPI:        base,
		DIAuth:            base,
		MobileIntegration: base,
	}
}

// Hosts returns protocol hosts for domain with every base URL redirected to the
// fake server. An unsupported domain fails the test rather than falling back to
// a real Garmin region.
func (s *Server) Hosts(domain protocol.Domain) protocol.Hosts {
	s.tb.Helper()

	hosts, err := protocol.NewHosts(domain)
	if err != nil {
		s.tb.Fatalf("testkit: unsupported domain %q: %v", domain, err)
	}

	return hosts.WithOverrides(s.Overrides())
}

// Requests returns a deep copy of the recorded requests, in arrival order.
func (s *Server) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]RecordedRequest, 0, len(s.requests))
	for _, req := range s.requests {
		out = append(out, RecordedRequest{
			Method: req.Method,
			Path:   req.Path,
			Query:  url.Values(maps.Clone(map[string][]string(req.Query))),
			Header: http.Header(maps.Clone(map[string][]string(req.Header))),
			Body:   append([]byte(nil), req.Body...),
		})
	}
	return out
}

// Close shuts the fake server down. It is safe to call more than once.
func (s *Server) Close() { s.inner.Close() }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, MaxRecordedBodyBytes))

	behavior, scripted := s.record(r, body)
	if !scripted {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"status-code":"404","message":"unscripted path"}}`)
		return
	}

	if behavior.Delay > 0 {
		time.Sleep(behavior.Delay)
	}
	writeBehavior(w, behavior)
}

// record stores the request and pops the next behavior for its path.
func (s *Server) record(r *http.Request, body []byte) (Behavior, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, RecordedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  url.Values(maps.Clone(map[string][]string(r.URL.Query()))),
		Header: http.Header(maps.Clone(map[string][]string(r.Header))),
		Body:   body,
	})

	queue, ok := s.steps[r.URL.Path]
	if !ok || len(queue) == 0 {
		return Behavior{}, false
	}

	index := min(s.cursor[r.URL.Path], len(queue)-1)
	s.cursor[r.URL.Path] = index + 1
	return queue[index], true
}

func writeBehavior(w http.ResponseWriter, b Behavior) {
	for key, values := range b.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	contentType := b.ContentType
	if contentType == "" {
		contentType = "text/plain;charset=UTF-8"
	}
	w.Header().Set("Content-Type", contentType)

	status := b.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, b.Body)
}
