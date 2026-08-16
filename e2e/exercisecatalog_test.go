//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// catalogHost is the authority the start-up read connects to. The proxy below sees
// it as the target of a CONNECT, which is how this test observes the read without
// letting one byte reach the real service.
const catalogHost = "connect.garmin.com:443"

// connectProxy records the tunnels a process asks for and refuses every one of
// them.
type connectProxy struct {
	mu      sync.Mutex
	targets []string
	headers []http.Header
}

func (p *connectProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.targets = append(p.targets, r.Host)
	p.headers = append(p.headers, r.Header.Clone())
	p.mu.Unlock()

	// Refused, always. The point of the test is what the server does when the
	// catalog cannot be read.
	http.Error(w, "no tunnel", http.StatusBadGateway)
}

func (p *connectProxy) seen() ([]string, []http.Header) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...), append([]http.Header(nil), p.headers...)
}

// TestStartUpReadsTheExerciseCatalogAnonymouslyAndSurvivesItsFailure drives the
// real binary against a proxy that refuses every tunnel: the server must still
// start, keep stdout clean, and have asked for nothing but an anonymous connection
// to the one permitted host.
func TestStartUpReadsTheExerciseCatalogAnonymouslyAndSurvivesItsFailure(t *testing.T) {
	proxy := &connectProxy{}
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)

	bin := buildBinary(t)
	stdout, stderr, code := runWithEnv(t, bin,
		[]string{
			"GARMIN_MCP_STATE_DIR=" + stateDir(t),
			"HTTPS_PROXY=" + server.URL,
			"https_proxy=" + server.URL,
		},
		"serve", "--transport=stdio")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0: an unreadable catalog must not fail a start-up "+
			"(stderr %q)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty: it is reserved for MCP frames", stdout)
	}

	targets, headers := proxy.seen()
	found := false
	for index, target := range targets {
		if target != catalogHost {
			t.Errorf("the process asked to reach %q; the only permitted host is %q",
				target, catalogHost)
			continue
		}
		found = true
		for _, header := range []string{"Proxy-Authorization", "Authorization", "Cookie"} {
			if value := headers[index].Get(header); value != "" {
				t.Errorf("the catalog connection carried %s: %q", header, value)
			}
		}
		for name, values := range headers[index] {
			if strings.Contains(strings.Join(values, " "), "Bearer ") {
				t.Errorf("the catalog connection header %s carries a bearer token", name)
			}
		}
	}
	if !found {
		t.Errorf("the process never tried to read the published catalog: it asked for %v",
			targets)
	}
}
