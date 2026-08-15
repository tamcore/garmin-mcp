package mcpserver

import (
	"context"
	"io"
	"net/http"
	"time"
)

// The probe paths, which are the conventional Kubernetes spellings.
//
// They are constants rather than options. A probe path an operator can rename is
// a probe path that gets renamed into a collision with a real endpoint, and the
// only thing renaming buys is obscurity over a response that already discloses
// nothing.
const (
	// LivenessPath answers whether this process is up.
	LivenessPath = "/livez"

	// ReadinessPath answers whether this process can serve.
	ReadinessPath = "/readyz"
)

// The two probe bodies. They are the whole response: a probe is a status code,
// and the body exists only so a human running curl sees something.
const (
	liveBody     = "ok"
	notReadyBody = "not ready"
)

// readinessTimeout bounds the readiness check.
//
// Without it a wedged store makes the probe hang instead of failing, and an
// orchestrator that never gets an answer keeps routing traffic to a process that
// cannot serve it. A timeout turns a hang into the honest answer.
const readinessTimeout = 2 * time.Second

// A ReadinessCheck reports whether this process can serve requests.
//
// It is injected, never discovered: the transport does not know what a store is
// and must not reach for a global to find one. Returning an error means not
// ready. The error is used as a boolean and nothing more — it is never rendered
// into the response, because a dependency error names hosts, ports, and
// sometimes credentials, and a probe endpoint is unauthenticated.
type ReadinessCheck func(context.Context) error

// A probeHandler serves the liveness and readiness probes.
//
// The two answer different questions on purpose. Liveness is a statement about
// the process: if it can run this handler, it is alive, and restarting it would
// destroy in-flight work for nothing. Readiness is a statement about the
// dependencies: a process whose store is unreachable is alive but useless, and
// the correct response is to stop routing traffic to it until it recovers.
// Collapsing them into one endpoint means either restarting a healthy process
// because a database blipped, or keeping a broken one in rotation.
type probeHandler struct {
	ready ReadinessCheck
}

// ServeHTTP answers one probe.
func (p probeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case LivenessPath:
		writeProbe(w, r, http.StatusOK, liveBody)
	case ReadinessPath:
		p.serveReadiness(w, r)
	default:
		http.NotFound(w, r)
	}
}

// serveReadiness runs the injected check under a bounded context.
//
// A nil check reports ready. The alternative — refusing readiness until someone
// injects a check — would leave a correctly configured deployment permanently out
// of rotation, which is an outage caused by the probe rather than detected by it.
// A deployment that wants a dependency asserted injects the check that asserts it.
func (p probeHandler) serveReadiness(w http.ResponseWriter, r *http.Request) {
	if p.ready == nil {
		writeProbe(w, r, http.StatusOK, liveBody)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()

	if err := p.ready(ctx); err != nil {
		writeProbe(w, r, http.StatusServiceUnavailable, notReadyBody)
		return
	}
	writeProbe(w, r, http.StatusOK, liveBody)
}

// writeProbe emits the status and the fixed body.
//
// no-store is not a nicety. A cached probe answer is a stale claim about a
// process's health, and an intermediary that served one would keep an unready
// process in rotation for as long as it held the entry.
func writeProbe(w http.ResponseWriter, r *http.Request, status int, body string) {
	header := w.Header()
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, body)
}
