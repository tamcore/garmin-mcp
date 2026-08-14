package mcpserver

import (
	"crypto/sha256"
	"net/http"
	"sync"
)

// sessionIDHeader carries the transport session id.
//
// It is a routing label, never a credential: every request that presents one is
// authenticated by its bearer token first, and the session is only then checked
// to belong to that token's authorization.
const sessionIDHeader = "Mcp-Session-Id"

// A sessionBinding is the authorization a transport session belongs to.
//
// It is a comparable value, so "is this the same authorization" is one
// comparison with no room for a partial match. The scopes are the canonical
// joined form grantBinding produces.
type sessionBinding struct {
	principal string
	clientID  string
	resource  string
	scopes    string
}

// A sessionRecord is a binding plus the token family that created the session,
// which is what a revocation is matched against.
type sessionRecord struct {
	binding sessionBinding
	family  string
}

// sessionBindings maps live sessions to their authorization.
//
// Sessions are keyed by the SHA-256 digest of their id rather than by the id
// itself. The digest answers every question this type is asked, and it means no
// session id is held in a form that could reach a log line, an error, or a
// panic dump. The key is an array, so it stays comparable.
type sessionBindings struct {
	mu      sync.RWMutex
	records map[[sha256.Size]byte]sessionRecord
}

func newSessionBindings() *sessionBindings {
	return &sessionBindings{records: make(map[[sha256.Size]byte]sessionRecord)}
}

// sessionKey digests a session id.
func sessionKey(sessionID string) [sha256.Size]byte {
	return sha256.Sum256([]byte(sessionID))
}

// bind records the authorization a freshly created session belongs to.
func (b *sessionBindings) bind(sessionID string, record sessionRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records[sessionKey(sessionID)] = record
}

// lookup returns the record for a session id, and whether one exists.
func (b *sessionBindings) lookup(sessionID string) (sessionRecord, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	record, ok := b.records[sessionKey(sessionID)]
	return record, ok
}

// release forgets a session. It is idempotent.
func (b *sessionBindings) release(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.records, sessionKey(sessionID))
}

// bindingWriter watches a response for a newly assigned session id and binds it.
//
// The SDK sets the session id header on the response to the initialize POST, so
// that header is the only place the transport learns which session its grant
// created. Binding happens when the status is written, which is strictly before
// the client can learn the id, so no follow-up request can reach an unbound
// session.
type bindingWriter struct {
	http.ResponseWriter
	record  sessionRecord
	bind    func(sessionID string, record sessionRecord)
	written bool
}

// WriteHeader binds the assigned session, then writes the status through.
func (w *bindingWriter) WriteHeader(status int) {
	if !w.written {
		w.written = true
		if sessionID := w.Header().Get(sessionIDHeader); sessionID != "" && status < 400 {
			w.bind(sessionID, w.record)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write implies a 200 status, exactly as net/http does, so a handler that never
// calls WriteHeader still gets its session bound.
func (w *bindingWriter) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

// Flush forwards to the wrapped writer when it can flush. A streamable response
// is server-sent events, and swallowing Flush would hold every event in a buffer
// until the stream ended.
func (w *bindingWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer, which is how
// the SDK sets write deadlines on a long-lived stream.
func (w *bindingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// guardSession binds a new session and refuses a request addressing another
// authorization's session.
//
// It runs inside the authenticating middleware, so a grant is always present. A
// missing grant means the chain was assembled wrong, and the request is refused
// rather than served, because the alternative is serving it unattributed.
//
// A request naming an unknown session is passed through untouched: whether that
// is a 404 or a fresh session is the SDK's decision, and answering it here would
// turn the guard into an oracle for which session ids exist.
func (t *HTTPTransport) guardSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grant, err := t.authorizer.Grant(r.Context())
		if err != nil {
			http.Error(w, "the request carries no verified authorization",
				http.StatusInternalServerError)
			return
		}

		record := sessionRecord{binding: grantBinding(grant), family: grant.Family}
		sessionID := r.Header.Get(sessionIDHeader)
		if sessionID == "" {
			next.ServeHTTP(&bindingWriter{
				ResponseWriter: w, record: record, bind: t.sessions.bind,
			}, r)
			return
		}

		bound, known := t.sessions.lookup(sessionID)
		if known && bound.binding != record.binding {
			// The message names nothing. A caller that learned which of the four
			// bound fields differed would learn something about another grant.
			http.Error(w, "this session belongs to another authorization",
				http.StatusForbidden)
			return
		}
		if known && r.Method == http.MethodDelete {
			defer t.sessions.release(sessionID)
		}
		next.ServeHTTP(w, r)
	})
}

// terminate closes every live session the revocation covers.
//
// An empty revocation selects everything and is discarded; see [Revocation].
// Closing the session ends its streams, which is the requirement: refusing the
// next request would leave a held-open event stream delivering to a caller whose
// authorization is already gone.
func (t *HTTPTransport) terminate(event Revocation) {
	if event.isEmpty() {
		return
	}
	for session := range t.server.MCPServer().Sessions() {
		sessionID := session.ID()
		if sessionID == "" {
			continue
		}
		record, ok := t.sessions.lookup(sessionID)
		if !ok || !event.matches(record) {
			continue
		}
		_ = session.Close()
		t.sessions.release(sessionID)
	}
}
