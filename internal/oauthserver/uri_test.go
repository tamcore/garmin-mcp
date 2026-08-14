package oauthserver

import (
	"errors"
	"strings"
	"testing"
)

func mustRedirectURI(t *testing.T, raw string) RedirectURI {
	t.Helper()
	uri, err := ParseRedirectURI(raw)
	if err != nil {
		t.Fatalf("ParseRedirectURI(%q): %v", raw, err)
	}
	return uri
}

func TestParseRedirectURIAcceptsHTTPSAndLoopback(t *testing.T) {
	for _, raw := range []string{
		"https://client.example/callback",
		"https://client.example:8443/cb?fixed=1",
		"http://127.0.0.1:53682/callback",
		"http://[::1]:53682/callback",
	} {
		t.Run(raw, func(t *testing.T) {
			if got := mustRedirectURI(t, raw); got.String() != raw {
				t.Fatalf("String() = %q, want %q", got.String(), raw)
			}
		})
	}
}

func TestParseRedirectURIRejectsUnsafeForms(t *testing.T) {
	cases := map[string]string{
		"empty":                          "",
		"relative":                       "/callback",
		"trailing fragment":              "https://client.example/cb#frag",
		"bare fragment":                  "https://client.example/cb#",
		"userinfo":                       "https://user@client.example/cb",
		"userinfo+pass":                  "https://user:pw@client.example/cb",
		"wildcard host":                  "https://*.client.example/cb",
		"wildcard path":                  "https://client.example/*",
		"plain http":                     "http://client.example/cb",
		"http localhost":                 "http://localhost:8080/cb",
		"custom scheme":                  "com.example.app:/oauth",
		"javascript":                     "javascript:alert(1)",
		"data":                           "data:text/html,hi",
		"no host":                        "https:///cb",
		"space":                          "https://client.example/c b",
		"control char":                   "https://client.example/cb\x01",
		"embedded newline":               "https://client.example/cb\n",
		"redirect past the length limit": "https://client.example/" + strings.Repeat("a", MaxRedirectURILen),
		"uppercase scheme":               "HTTPS://client.example/cb",
		"http 127 no port":               "http://127.0.0.2/cb",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRedirectURI(raw); !errors.Is(err, ErrInvalidRedirectURI) {
				t.Fatalf("ParseRedirectURI(%q) error = %v, want ErrInvalidRedirectURI", raw, err)
			}
		})
	}
}

func TestRedirectURIEqualIsByteExact(t *testing.T) {
	base := mustRedirectURI(t, "https://client.example/cb")

	for _, other := range []string{
		"https://client.example/cb/",
		"https://client.example/CB",
		"https://Client.example/cb",
		"https://client.example:443/cb",
		"https://client.example/cb?x=1",
	} {
		t.Run(other, func(t *testing.T) {
			if base.Equal(mustRedirectURI(t, other)) {
				t.Fatalf("%q must not equal %q", base.String(), other)
			}
		})
	}
	if !base.Equal(mustRedirectURI(t, "https://client.example/cb")) {
		t.Fatal("an identical redirect URI must compare equal")
	}
}

func TestRedirectURIWithParamsPreservesExistingQuery(t *testing.T) {
	uri := mustRedirectURI(t, "https://client.example/cb?fixed=1")

	got, err := uri.WithParams(map[string]string{"code": "abc", "state": "s p&=?#"})
	if err != nil {
		t.Fatalf("WithParams: %v", err)
	}
	if !strings.HasPrefix(got, "https://client.example/cb?") {
		t.Fatalf("WithParams lost the base URI: %q", got)
	}
	for _, want := range []string{"fixed=1", "code=abc", "state=s+p%26%3D%3F%23"} {
		if !strings.Contains(got, want) {
			t.Fatalf("WithParams() = %q, want it to contain %q", got, want)
		}
	}
}

func mustResource(t *testing.T, raw string) Resource {
	t.Helper()
	res, err := ParseResource(raw)
	if err != nil {
		t.Fatalf("ParseResource(%q): %v", raw, err)
	}
	return res
}

func TestParseResourceCanonicalizesEmptyPath(t *testing.T) {
	if got := mustResource(t, testIssuerURI).String(); got != testIssuerURI {
		t.Fatalf("String() = %q", got)
	}
	if !mustResource(t, testIssuerURI).Equal(mustResource(t, testIssuerURI+"/")) {
		t.Fatal("an empty path and a root path name the same resource")
	}
	if mustResource(t, "https://mcp.example/mcp").Equal(mustResource(t, "https://mcp.example/mcp/")) {
		t.Fatal("a non-empty path must compare exactly")
	}
}

func TestParseResourceRejectsUnsafeForms(t *testing.T) {
	for name, raw := range map[string]string{
		"no characters":                  "",
		"relative":                       "/mcp",
		"trailing fragment":              "https://mcp.example/mcp#f",
		"userinfo":                       "https://u@mcp.example/mcp",
		"plain http":                     "http://mcp.example/mcp",
		"wildcard":                       "https://*.mcp.example/mcp",
		"no host":                        "https:///mcp",
		"control char":                   "https://mcp.example/\x00",
		"resource past the length limit": "https://mcp.example/" + strings.Repeat("a", MaxResourceLen),
		"custom scheme":                  "urn:example:mcp",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseResource(raw); !errors.Is(err, ErrInvalidResource) {
				t.Fatalf("ParseResource(%q) error = %v, want ErrInvalidResource", raw, err)
			}
		})
	}
}

func TestParseResourceAllowsLoopbackForLocalDeployments(t *testing.T) {
	if _, err := ParseResource("http://127.0.0.1:8080/mcp"); err != nil {
		t.Fatalf("loopback resource rejected: %v", err)
	}
}
