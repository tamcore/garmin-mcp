package client_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func TestDoRejectsInvalidRequestsBeforeDispatch(t *testing.T) {
	t.Parallel()

	cases := map[string]client.Request{
		"unknown op": {
			Op: client.Op("../etc/passwd"), Endpoint: client.EndpointSocialProfile, Path: client.PathSocialProfile,
		},
		"unknown endpoint": {
			Op: client.OpGetSocialProfile, Endpoint: client.Endpoint("https://evil.example/?t=1"),
			Path: client.PathSocialProfile,
		},
		"query in path": {
			Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile,
			Path: client.PathSocialProfile + "?token=SENTINEL",
		},
		"fragment in path": {
			Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile,
			Path: client.PathSocialProfile + "#x",
		},
		"traversal segment": {
			Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile,
			Path: "/userprofile-service/../../secrets",
		},
		"encoded traversal": {
			Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile,
			Path: "/userprofile-service/%2e%2e/secrets",
		},
		"backslash in path": {
			Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile,
			Path: "/userprofile-service\\secrets",
		},
		"relative path": {
			Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile, Path: "userprofile-service",
		},
		"empty path": {Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile},
		"unsupported method": {
			Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile,
			Path: client.PathSocialProfile, Method: "CONNECT",
		},
		"body on a read": {
			Op: client.OpGetSocialProfile, Endpoint: client.EndpointSocialProfile,
			Path: client.PathSocialProfile, Body: []byte(`{}`), Effect: client.EffectRead,
		},
	}

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusOK}}}
			_, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), req)
			if !errors.Is(err, client.ErrValidation) {
				t.Errorf("Do() = %v, want a validation error", err)
			}
			if caller.calls() != 0 {
				t.Errorf("caller was used %d times, want 0: the request never leaves this package", caller.calls())
			}
		})
	}
}

func TestNewSessionRequiresACallerAndAPrincipal(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{}
	if _, err := client.NewSession(caller, ""); !errors.Is(err, client.ErrMissingPrincipal) {
		t.Errorf("NewSession(caller, \"\") = %v, want ErrMissingPrincipal", err)
	}
	if _, err := client.NewSession(nil, testPrincipal); !errors.Is(err, client.ErrNotConfigured) {
		t.Errorf("NewSession(nil, principal) = %v, want ErrNotConfigured", err)
	}

	session, err := client.NewSession(caller, testPrincipal)
	if err != nil {
		t.Fatalf("NewSession() = %v", err)
	}
	if session.Principal() != testPrincipal {
		t.Errorf("Principal() = %q, want %q", session.Principal(), testPrincipal)
	}
}

func TestNewRejectsAnUnusableConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := client.New(client.Config{}); !errors.Is(err, client.ErrNotConfigured) {
		t.Errorf("New(Config{}) = %v, want ErrNotConfigured for a zero Hosts", err)
	}

	bad := client.Config{Hosts: testHosts(t), Limits: client.Limits{MaxResponseBytes: -1}}
	if _, err := client.New(bad); !errors.Is(err, client.ErrInvalidLimits) {
		t.Errorf("New() = %v, want ErrInvalidLimits", err)
	}
}
