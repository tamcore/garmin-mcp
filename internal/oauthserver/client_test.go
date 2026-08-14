package oauthserver

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	testClientID     = "operator-registered-client"
	testRedirect     = "https://client.example/cb"
	testResourceURI  = "https://mcp.example/mcp"
	testClientSecret = "3Nq0oQ9Yt7vXk2sB1cD4eF6gH8jK0lM2nP4rS6tU8wY"
)

func publicClientSpec() ClientSpec {
	return ClientSpec{
		ID:                      testClientID,
		Name:                    testClientName,
		RedirectURIs:            []string{testRedirect},
		Scopes:                  testScopesBoth,
		Resources:               []string{testResourceURI},
		TokenEndpointAuthMethod: string(AuthMethodNone),
	}
}

func mustClient(t *testing.T, spec ClientSpec) Client {
	t.Helper()
	client, err := NewClient(spec)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestNewClientAcceptsAPublicPreRegisteredClient(t *testing.T) {
	client := mustClient(t, publicClientSpec())

	if client.ID() != testClientID || client.Name() != testClientName {
		t.Fatalf("identity not preserved: %q %q", client.ID(), client.Name())
	}
	if !client.IsPublic() || client.AuthMethod() != AuthMethodNone {
		t.Fatal("a client with auth method none must be public")
	}
	if got := client.MaxScopes().String(); got != "garmin.health.read garmin.profile.read" {
		t.Fatalf("MaxScopes() = %q", got)
	}
	if len(client.RedirectURIs()) != 1 {
		t.Fatalf("RedirectURIs() = %v", client.RedirectURIs())
	}
}

func TestClientRedirectURIsIsACopy(t *testing.T) {
	client := mustClient(t, publicClientSpec())

	uris := client.RedirectURIs()
	uris[0] = RedirectURI{}

	if !client.RedirectURIs()[0].Equal(mustRedirectURI(t, testRedirect)) {
		t.Fatal("RedirectURIs exposed the internal backing array")
	}
}

func TestNewClientRejectsInvalidSpecs(t *testing.T) {
	withSpec := func(mutate func(*ClientSpec)) ClientSpec {
		spec := publicClientSpec()
		mutate(&spec)
		return spec
	}
	longID := strings.Repeat("a", MaxClientIDLen+1)
	secretHash := SecretFromString(testClientSecret).Lookup().Hex()

	cases := map[string]struct {
		spec ClientSpec
		want error
	}{
		"identifier absent": {withSpec(func(s *ClientSpec) { s.ID = "" }), ErrInvalidClient},
		"padded id":         {withSpec(func(s *ClientSpec) { s.ID = " id " }), ErrInvalidClient},
		"control in id":     {withSpec(func(s *ClientSpec) { s.ID = "id\x01" }), ErrInvalidClient},
		"long id":           {withSpec(func(s *ClientSpec) { s.ID = longID }), ErrInvalidClient},
		"no redirect":       {withSpec(func(s *ClientSpec) { s.RedirectURIs = nil }), ErrInvalidClient},
		"bad redirect": {withSpec(func(s *ClientSpec) {
			s.RedirectURIs = []string{"http://evil.example/cb"}
		}), ErrInvalidRedirectURI},
		"dup redirect": {withSpec(func(s *ClientSpec) {
			s.RedirectURIs = []string{testRedirect, testRedirect}
		}), ErrInvalidClient},
		"no resource":  {withSpec(func(s *ClientSpec) { s.Resources = nil }), ErrInvalidClient},
		"bad resource": {withSpec(func(s *ClientSpec) { s.Resources = []string{"ftp://x/y"} }), ErrInvalidResource},
		"bad scope":    {withSpec(func(s *ClientSpec) { s.Scopes = testMalformedScope }), ErrInvalidScope},
		"no scope":     {withSpec(func(s *ClientSpec) { s.Scopes = "" }), ErrInvalidClient},
		"unknown method": {withSpec(func(s *ClientSpec) {
			s.TokenEndpointAuthMethod = "private_key_jwt"
		}), ErrInvalidClient},
		"auth method absent": {withSpec(func(s *ClientSpec) { s.TokenEndpointAuthMethod = "" }), ErrInvalidClient},
		"public + secret":    {withSpec(func(s *ClientSpec) { s.SecretHashHex = secretHash }), ErrInvalidClient},
		"secret + no hash": {withSpec(func(s *ClientSpec) {
			s.TokenEndpointAuthMethod = string(AuthMethodSecretPost)
		}), ErrInvalidClient},
		"bad hash": {withSpec(func(s *ClientSpec) {
			s.TokenEndpointAuthMethod = string(AuthMethodSecretPost)
			s.SecretHashHex = "not-hex"
		}), ErrInvalidClient},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClient(tc.spec); !errors.Is(err, tc.want) {
				t.Fatalf("NewClient error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestClientMatchRedirectURIIsExact(t *testing.T) {
	spec := publicClientSpec()
	spec.RedirectURIs = []string{testRedirect, "https://client.example/other"}
	client := mustClient(t, spec)

	got, err := client.MatchRedirectURI(testRedirect)
	if err != nil {
		t.Fatalf("MatchRedirectURI: %v", err)
	}
	if !got.Equal(mustRedirectURI(t, testRedirect)) {
		t.Fatalf("MatchRedirectURI returned %q", got.String())
	}

	for name, raw := range map[string]string{
		"unregistered":      "https://client.example/attacker",
		"trailing slash":    testRedirect + "/",
		"added query":       testRedirect + "?x=1",
		"host case":         "https://CLIENT.example/cb",
		"open redirect":     "https://client.example.evil.test/cb",
		"empty":             "",
		"malformed":         "://",
		"fragment appended": testRedirect + "#f",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.MatchRedirectURI(raw); !errors.Is(err, ErrRedirectURINotRegistered) {
				t.Fatalf("MatchRedirectURI(%q) error = %v, want ErrRedirectURINotRegistered", raw, err)
			}
		})
	}
}

func TestClientAllowsResourceExactly(t *testing.T) {
	client := mustClient(t, publicClientSpec())

	if !client.AllowsResource(mustResource(t, testResourceURI)) {
		t.Fatal("the registered resource must be allowed")
	}
	if client.AllowsResource(mustResource(t, testOtherResource)) {
		t.Fatal("an unregistered resource must not be allowed")
	}
}

func TestClientAuthenticatePublicClientRefusesASecret(t *testing.T) {
	client := mustClient(t, publicClientSpec())

	if err := client.Authenticate(Secret{}); err != nil {
		t.Fatalf("a public client must authenticate with no secret: %v", err)
	}
	if err := client.Authenticate(SecretFromString(testClientSecret)); !errors.Is(err, ErrClientAuthFailed) {
		t.Fatalf("a public client presenting a secret must fail, got %v", err)
	}
}

func TestClientAuthenticateConfidentialClient(t *testing.T) {
	spec := publicClientSpec()
	spec.TokenEndpointAuthMethod = string(AuthMethodSecretPost)
	spec.SecretHashHex = SecretFromString(testClientSecret).Lookup().Hex()
	client := mustClient(t, spec)

	if client.IsPublic() {
		t.Fatal("a client with a secret must not report IsPublic")
	}
	if err := client.Authenticate(SecretFromString(testClientSecret)); err != nil {
		t.Fatalf("Authenticate with the right secret: %v", err)
	}
	for name, presented := range map[string]Secret{
		"wrong secret": SecretFromString(testClientSecret + "x"),
		"no secret":    {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := client.Authenticate(presented); !errors.Is(err, ErrClientAuthFailed) {
				t.Fatalf("Authenticate error = %v, want ErrClientAuthFailed", err)
			}
		})
	}
}

func TestClientRenderingsCarryNoSecretHash(t *testing.T) {
	spec := publicClientSpec()
	spec.TokenEndpointAuthMethod = string(AuthMethodSecretBasic)
	spec.SecretHashHex = SecretFromString(testClientSecret).Lookup().Hex()
	client := mustClient(t, spec)

	for _, rendered := range []string{
		client.String(),
		client.GoString(),
		fmt.Sprintf("%v", client),
		fmt.Sprintf("%#v", client),
	} {
		if strings.Contains(rendered, spec.SecretHashHex) {
			t.Fatalf("rendering leaked the secret hash: %q", rendered)
		}
		if strings.Contains(rendered, testClientSecret) {
			t.Fatalf("rendering leaked the client secret: %q", rendered)
		}
		if !strings.Contains(rendered, testClientID) {
			t.Fatalf("rendering should name the client id, got %q", rendered)
		}
	}
}
