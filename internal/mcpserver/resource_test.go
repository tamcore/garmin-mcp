package mcpserver_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

const (
	testResourceURI  = "test://doc/one"
	testResourceBody = `{"hello":"world"}`
	testResourceMIME = "text/plain"
)

// resourceOnlyPolicy is the smallest policy a server needs: the built-in tool and
// nothing else. Resources are not gated by tier, so nothing here mentions them.
func resourceOnlyPolicy() policy.Config {
	return policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: []string{mcpserver.ServerInfoToolName},
	}
}

// staticRegistrar registers one constant document.
type staticRegistrar struct {
	uri  string
	body string
}

func (r staticRegistrar) RegisterResources(registry *mcpserver.Registry) error {
	return mcpserver.AddResource(registry, mcpserver.ResourceSpec{
		URI:         r.uri,
		Name:        "test_document",
		Title:       "Test document",
		Description: "a constant document",
		MIMEType:    testResourceMIME,
	}, func() string { return r.body })
}

// specRegistrar registers whatever spec and body it is given, valid or not.
type specRegistrar struct {
	spec mcpserver.ResourceSpec
	body func() string
}

func (r specRegistrar) RegisterResources(registry *mcpserver.Registry) error {
	return mcpserver.AddResource(registry, r.spec, r.body)
}

// withResource registers one document on a test server.
func withResource(uri, body string) func(*mcpserver.Deps) {
	return func(d *mcpserver.Deps) {
		d.ResourceRegistrars = []mcpserver.ResourceRegistrar{staticRegistrar{uri: uri, body: body}}
	}
}

// TestAResourceIsListedAndReadOverASession proves the whole path works end to end:
// a registrar contributes a document, the server advertises it, and a client reads
// its bytes back with the declared media type.
func TestAResourceIsListedAndReadOverASession(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, withResource(testResourceURI, testResourceBody))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	listed, err := session.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources returned error: %v", err)
	}
	uris := make([]string, 0, len(listed.Resources))
	for _, resource := range listed.Resources {
		uris = append(uris, resource.URI)
	}
	if !slices.Contains(uris, testResourceURI) {
		t.Fatalf("listed resources %v do not include %q", uris, testResourceURI)
	}

	read, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: testResourceURI})
	if err != nil {
		t.Fatalf("ReadResource returned error: %v", err)
	}
	if len(read.Contents) != 1 {
		t.Fatalf("read %d contents, want 1", len(read.Contents))
	}
	if got := read.Contents[0].Text; got != testResourceBody {
		t.Errorf("body = %q, want %q", got, testResourceBody)
	}
	if got := read.Contents[0].MIMEType; got != testResourceMIME {
		t.Errorf("mimeType = %q, want %q", got, testResourceMIME)
	}
}

// TestAnUnknownResourceIsRefused proves a URI nobody registered does not fall
// through to something else.
func TestAnUnknownResourceIsRefused(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, withResource(testResourceURI, testResourceBody))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "test://doc/absent",
	}); err == nil {
		t.Error("reading an unregistered URI succeeded")
	}
}

// TestRegisteringTheSameURITwiceIsRefused keeps two registrars from silently
// replacing each other's document. The SDK's own AddResource replaces on conflict,
// which is exactly the failure this catches before it reaches the SDK.
func TestRegisteringTheSameURITwiceIsRefused(t *testing.T) {
	t.Parallel()

	_, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: testServerName, Version: testVersion},
		Policy:     mustPolicy(t, resourceOnlyPolicy()),
		Principals: mustResolver(t),
		ResourceRegistrars: []mcpserver.ResourceRegistrar{
			staticRegistrar{uri: testResourceURI, body: testResourceBody},
			staticRegistrar{uri: testResourceURI, body: `{"other":"document"}`},
		},
	})
	if !errors.Is(err, mcpserver.ErrDuplicateResource) {
		t.Fatalf("New() = %v, want ErrDuplicateResource", err)
	}
}

// TestAResourceSpecMustBeUsable refuses the specs that would either panic the SDK or
// produce a document a client cannot interpret.
func TestAResourceSpecMustBeUsable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		spec mcpserver.ResourceSpec
		body func() string
	}{
		{
			name: "no URI",
			spec: mcpserver.ResourceSpec{Name: "n", MIMEType: testResourceMIME},
			body: func() string { return "{}" },
		},
		{
			// The SDK panics on a scheme-less URI, so this must be refused first.
			name: "no URI scheme",
			spec: mcpserver.ResourceSpec{URI: "doc/one", Name: "n", MIMEType: testResourceMIME},
			body: func() string { return "{}" },
		},
		{
			// url.Parse refuses this, and the SDK would panic on it at start-up.
			name: "unparsable URI",
			spec: mcpserver.ResourceSpec{URI: "test://%", Name: "n", MIMEType: testResourceMIME},
			body: func() string { return "{}" },
		},
		{
			name: "no name",
			spec: mcpserver.ResourceSpec{URI: testResourceURI, MIMEType: testResourceMIME},
			body: func() string { return "{}" },
		},
		{
			name: "no MIME type",
			spec: mcpserver.ResourceSpec{URI: testResourceURI, Name: "n"},
			body: func() string { return "{}" },
		},
		{
			name: "no body",
			spec: mcpserver.ResourceSpec{URI: testResourceURI, Name: "n", MIMEType: testResourceMIME},
			body: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := mcpserver.New(mcpserver.Deps{
				Info:       mcpserver.Info{Name: testServerName, Version: testVersion},
				Policy:     mustPolicy(t, resourceOnlyPolicy()),
				Principals: mustResolver(t),
				ResourceRegistrars: []mcpserver.ResourceRegistrar{
					specRegistrar{spec: tc.spec, body: tc.body},
				},
			})
			if !errors.Is(err, mcpserver.ErrInvalidResourceSpec) {
				t.Fatalf("New() = %v, want ErrInvalidResourceSpec", err)
			}
		})
	}
}

// TestANilResourceRegistrarIsAWiringMistake matches how a nil tool registrar is
// treated: rejected at construction rather than skipped at run time.
func TestANilResourceRegistrarIsAWiringMistake(t *testing.T) {
	t.Parallel()

	_, err := mcpserver.New(mcpserver.Deps{
		Info:               mcpserver.Info{Name: testServerName, Version: testVersion},
		Policy:             mustPolicy(t, resourceOnlyPolicy()),
		Principals:         mustResolver(t),
		ResourceRegistrars: []mcpserver.ResourceRegistrar{nil},
	})
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Fatalf("New() = %v, want ErrMissingDependency", err)
	}
}

// TestAResourceBodyIsRenderedOnce proves the document is produced at registration
// and not per read. Resource reads are deliberately not rate limited, so a body
// rebuilt on every call would let one authenticated caller spend the server's CPU
// without touching a budget.
func TestAResourceBodyIsRenderedOnce(t *testing.T) {
	t.Parallel()

	var built int
	counting := specRegistrar{
		spec: mcpserver.ResourceSpec{
			URI: testResourceURI, Name: "n", MIMEType: testResourceMIME,
		},
		body: func() string {
			built++
			return testResourceBody
		},
	}
	server, _, _ := tieredServer(t, func(d *mcpserver.Deps) {
		d.ResourceRegistrars = []mcpserver.ResourceRegistrar{counting}
	})
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	for range 3 {
		if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{
			URI: testResourceURI,
		}); err != nil {
			t.Fatalf("ReadResource returned error: %v", err)
		}
	}
	if built != 1 {
		t.Errorf("the body was built %d times, want once at registration", built)
	}
}
