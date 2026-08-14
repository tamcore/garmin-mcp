package mcpserver_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// everyVerb is the set of fmt verbs a leak test has to try.
//
// %v and %+v are the ordinary paths. %#v, %s, %q, %d and %x matter more: for a
// value whose type has no method for the verb, fmt falls into badVerb, which
// re-prints the value at depth zero and dereferences a pointer to a struct,
// printing its unexported fields verbatim.
var everyVerb = []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"}

// TestTransportRendersNoSessionIDUnderAnyVerb formats the live transport, with
// its type's methods stripped by an alias, and asserts no session id appears.
//
// The alias is the point. HTTPTransport could satisfy every verb with a
// redacting String method, and the test would then prove only that the method
// exists. Redefined as a distinct type with no methods, fmt has to fall back to
// structural printing, which is the path that reaches unexported fields.
//
// The transport passes because it never holds a session id: sessionBindings keys
// its map by the SHA-256 digest of the id. That is why this package adds no
// secret-bearing type of its own — there is no session id in memory to redact.
//
// The transport is built with the production authorizer rather than the test
// fake, and that distinction is load-bearing. HTTPTransport holds its authorizer
// as an interface field, so structural printing descends into whatever the
// implementation holds; the assertion is therefore about the real pair. The fake
// keeps a plaintext token table, which no production type does.
func TestTransportRendersNoSessionIDUnderAnyVerb(t *testing.T) {
	// Arrange
	type strippedTransport mcpserver.HTTPTransport

	transport := oauthTransport(t)
	sessionID := initSession(t, transport, liveToken)
	stripped := (*strippedTransport)(transport)

	for _, verb := range everyVerb {
		t.Run(verb, func(t *testing.T) {
			// Act
			rendered := fmt.Sprintf(verb, stripped)
			alsoRendered := fmt.Sprintf(verb, *stripped)

			// Assert
			for _, output := range []string{rendered, alsoRendered} {
				if strings.Contains(output, sessionID) {
					t.Fatalf("verb %s rendered the session id: %q", verb, output)
				}
				if strings.Contains(output, liveToken) {
					t.Fatalf("verb %s rendered a bearer token: %q", verb, output)
				}
			}
		})
	}
}

// TestGrantRendersNoTokenUnderAnyVerb does the same for Grant, the transport's
// description of a caller. A grant is not a credential and may name its
// principal and client, but it must never acquire a field holding token material.
func TestGrantRendersNoTokenUnderAnyVerb(t *testing.T) {
	// Arrange
	type strippedGrant mcpserver.Grant

	grant := strippedGrant{
		Principal: mustPrincipalID(t, principalAlice),
		ClientID:  clientA,
		Resource:  testResource,
		Scopes:    []string{scopeRead},
		Family:    familyAlice,
	}

	for _, verb := range everyVerb {
		t.Run(verb, func(t *testing.T) {
			// Act
			rendered := fmt.Sprintf(verb, grant) + fmt.Sprintf(verb, &grant)

			// Assert
			if strings.Contains(rendered, tokenAlice) {
				t.Fatalf("verb %s rendered a bearer token: %q", verb, rendered)
			}
		})
	}
}

// TestHTTPLogsCarryNoSessionIDTokenOrHeader drives a real request through the
// transport with a logger attached and reads every record it emitted.
func TestHTTPLogsCarryNoSessionIDTokenOrHeader(t *testing.T) {
	// Arrange
	const cookieValue = "a-cookie-value"

	sink := &syncBuffer{}
	authorizer := newFakeAuthorizer(t)
	resolver, err := identity.NewBearerResolver(mcpserver.PrincipalSource(authorizer))
	if err != nil {
		t.Fatalf("NewBearerResolver returned error: %v", err)
	}
	server := newTestServer(t, mcpserver.Deps{
		Info:   mcpserver.Info{Name: testServerName, Version: testVersion},
		Logger: mustLogger(t, sink, mcplog.Config{}),
		Policy: mustPolicy(t, policy.Config{
			Mode:          policy.ModeRemote,
			ReadOnlyTools: []string{mcpserver.ServerInfoToolName, whoamiTool},
		}),
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{whoamiRegistrar{}},
	})
	transport, err := mcpserver.NewHTTPTransport(server, testHTTPOptions(authorizer))
	if err != nil {
		t.Fatalf("NewHTTPTransport returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	go func() { _ = transport.Run(ctx) }()

	sessionID := initSession(t, transport, tokenAlice)
	req := mcpPOST(t, callToolBody(2), tokenAlice, sessionID)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookieValue})

	// Act
	transport.ServeHTTP(httptest.NewRecorder(), req)
	cancel()

	// Assert
	logged := sink.String()
	if logged == "" {
		t.Fatalf("the transport emitted no log records at all")
	}
	for name, secret := range map[string]string{
		"session id":   sessionID,
		"bearer token": tokenAlice,
		"cookie value": cookieValue,
	} {
		if strings.Contains(logged, secret) {
			t.Fatalf("the %s reached the log: %q", name, logged)
		}
	}
}
