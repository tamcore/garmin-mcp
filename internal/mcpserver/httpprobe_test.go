package mcpserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// errStoreUnreachable stands in for the store being down. Its text is deliberately
// distinctive, so a disclosure test can look for it in the response.
var errStoreUnreachable = errors.New("dial tcp 10.9.9.9:5432: connection refused")

// probeGET issues an unauthenticated probe request, which is what a kubelet does.
func probeGET(t *testing.T, transport *mcpserver.HTTPTransport, path string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

// probeTransport builds a transport with the given readiness check.
func probeTransport(t *testing.T, ready mcpserver.ReadinessCheck) *mcpserver.HTTPTransport {
	t.Helper()

	opts := testHTTPOptions(newFakeAuthorizer(t))
	opts.Readiness = ready
	return newTestTransport(t, opts)
}

func TestLivenessAnswersWithoutACredential(t *testing.T) {
	// Arrange
	transport := probeTransport(t, nil)

	// Act
	recorder := probeGET(t, transport, mcpserver.LivenessPath)

	// Assert
	if recorder.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want 200", recorder.Code)
	}
	if body := strings.TrimSpace(recorder.Body.String()); body != "ok" {
		t.Fatalf("liveness body = %q, want %q", body, "ok")
	}
}

func TestReadinessIsReadyWhenTheCheckSucceeds(t *testing.T) {
	// Arrange
	transport := probeTransport(t, func(context.Context) error { return nil })

	// Act
	recorder := probeGET(t, transport, mcpserver.ReadinessPath)

	// Assert
	if recorder.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want 200", recorder.Code)
	}
}

func TestReadinessIsUnavailableWhenTheStoreIsUnreachable(t *testing.T) {
	// Arrange
	transport := probeTransport(t, func(context.Context) error { return errStoreUnreachable })

	// Act
	recorder := probeGET(t, transport, mcpserver.ReadinessPath)

	// Assert
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "10.9.9.9") {
		t.Fatalf("readiness disclosed the failing dependency: %q", recorder.Body.String())
	}
}

// TestLivenessStaysUpWhileReadinessFails is the whole reason the two probes
// exist separately: an unready process must not be restarted, only drained.
func TestLivenessStaysUpWhileReadinessFails(t *testing.T) {
	// Arrange
	transport := probeTransport(t, func(context.Context) error { return errStoreUnreachable })

	// Act
	live := probeGET(t, transport, mcpserver.LivenessPath)
	ready := probeGET(t, transport, mcpserver.ReadinessPath)

	// Assert
	if live.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want 200 while readiness fails", live.Code)
	}
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", ready.Code)
	}
}

func TestProbesDiscloseNoConfigurationDetail(t *testing.T) {
	// Arrange
	transport := probeTransport(t, func(context.Context) error { return nil })
	forbidden := map[string]string{
		"version":           testVersion,
		"server name":       testServerName,
		"public URL":        testPublicURL,
		"client identifier": clientA,
		"principal":         principalAlice,
		"protocol version":  mcpserver.ProtocolVersion,
	}

	for _, path := range []string{mcpserver.LivenessPath, mcpserver.ReadinessPath} {
		t.Run(path, func(t *testing.T) {
			// Act
			recorder := probeGET(t, transport, path)
			var rendered strings.Builder
			rendered.WriteString(recorder.Body.String())
			for name, values := range recorder.Header() {
				rendered.WriteString(name + strings.Join(values, " "))
			}

			// Assert
			for name, detail := range forbidden {
				if strings.Contains(rendered.String(), detail) {
					t.Fatalf("the probe disclosed the %s: %q", name, rendered.String())
				}
			}
		})
	}
}

func TestProbesRefuseMethodsOtherThanGetAndHead(t *testing.T) {
	// Arrange
	transport := probeTransport(t, nil)

	// Act
	recorder := httptest.NewRecorder()
	transport.ServeHTTP(recorder,
		httptest.NewRequest(http.MethodPost, mcpserver.ReadinessPath, nil))

	// Assert
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}

func TestReadinessWithoutACheckReportsTheProcessReady(t *testing.T) {
	// Arrange: no readiness dependency was injected.
	transport := probeTransport(t, nil)

	// Act
	recorder := probeGET(t, transport, mcpserver.ReadinessPath)

	// Assert
	if recorder.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want 200 when no check is configured", recorder.Code)
	}
}

// TestProbeHandlerIsMountableOnItsOwnListener proves the probes are also
// available as a standalone handler, so a deployment that wants them on a
// separate administrative listener can have that without a second implementation.
func TestProbeHandlerIsMountableOnItsOwnListener(t *testing.T) {
	// Arrange
	transport := probeTransport(t, func(context.Context) error { return errStoreUnreachable })
	handler := transport.ProbeHandler()

	// Act
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, mcpserver.ReadinessPath, nil))

	// Assert
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("standalone readiness status = %d, want 503", recorder.Code)
	}
}

// TestProbePathsDoNotShadowTheConfiguredEndpoints keeps the routing honest: a
// deployment that publishes MCP on /livez still reaches MCP there.
func TestProbePathsDoNotShadowTheConfiguredEndpoints(t *testing.T) {
	// Arrange
	opts := testHTTPOptions(newFakeAuthorizer(t))
	opts.PublicURL = "https://mcp.example.test" + mcpserver.LivenessPath
	transport := newTestTransport(t, opts)

	// Act
	recorder := probeGET(t, transport, mcpserver.LivenessPath)

	// Assert
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; the probe shadowed the MCP endpoint", recorder.Code)
	}
}
