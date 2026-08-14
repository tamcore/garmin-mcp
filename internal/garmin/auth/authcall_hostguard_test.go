package auth_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// foreignHost is a host that belongs to no Garmin region and to no test override.
const foreignHost = "attacker.example"

// garminHosts are the production bases for domain, with no overrides, so a test
// can prove the allowlist is derived from the region rather than hardcoded. The
// stub transport answers before any connection is made, so no Garmin host is
// contacted.
func garminHosts(t *testing.T, domain protocol.Domain) protocol.Hosts {
	t.Helper()

	hosts, err := protocol.NewHosts(domain)
	if err != nil {
		t.Fatalf("NewHosts(%q): %v", domain, err)
	}
	return hosts
}

// mustRequest builds a GET for rawURL.
func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("build request for %q: %v", rawURL, err)
	}
	return req
}

// assertRefused runs Do against rawURL and requires a refusal that sent nothing.
func assertRefused(t *testing.T, h *refreshHarness, rawURL string) {
	t.Helper()

	before := len(h.transport.recorded())

	resp, err := h.refresher.Do(t.Context(), testPrincipalID, mustRequest(t, rawURL))
	if !errors.Is(err, auth.ErrForeignHost) {
		t.Fatalf("Do(%q): err = %v, want ErrForeignHost", rawURL, err)
	}
	if resp != nil {
		t.Errorf("Do(%q) returned a response alongside the refusal", rawURL)
	}
	if recorded := h.transport.recorded(); len(recorded) != before {
		t.Errorf("Do(%q) dispatched %d requests, want none: %v",
			rawURL, len(recorded)-before, recorded[before:])
	}
}

func TestDoRefusesAHostOutsideTheConfiguredHosts(t *testing.T) {
	h := newRefreshHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
		t.Error("the transport was reached for an off-boundary host")
		return jsonResponse(http.StatusOK, `{}`), nil
	})
	seedValidTokens(h)

	assertRefused(t, h, "https://"+foreignHost+apiPath)
}

func TestDoRefusesASuffixAttackHost(t *testing.T) {
	h := newRefreshHarnessWithHosts(t, garminHosts(t, protocol.DomainGlobal),
		func(_ *http.Request, _ int) (*http.Response, error) {
			t.Error("the transport was reached for a suffix-attack host")
			return jsonResponse(http.StatusOK, `{}`), nil
		})
	seedValidTokens(h)

	for _, rawURL := range []string{
		"https://sso.garmin.com." + foreignHost + apiPath,
		"https://connectapi.garmin.com." + foreignHost + apiPath,
		"https://" + foreignHost + "/?x=https://connectapi.garmin.com",
		"https://user:pass@connectapi.garmin.com" + apiPath,
	} {
		assertRefused(t, h, rawURL)
	}
}

func TestDoRefusesAPlaintextDowngrade(t *testing.T) {
	h := newRefreshHarnessWithHosts(t, garminHosts(t, protocol.DomainGlobal),
		func(_ *http.Request, _ int) (*http.Response, error) {
			t.Error("the transport was reached for an http URL")
			return jsonResponse(http.StatusOK, `{}`), nil
		})
	seedValidTokens(h)

	assertRefused(t, h, "http://connectapi.garmin.com"+apiPath)
}

func TestDoAllowsEveryConfiguredBase(t *testing.T) {
	hosts := offlineHosts(t)
	bases := map[string]string{
		"sso":                hosts.SSOBase(),
		"connect":            hosts.ConnectBase(),
		"connectapi":         hosts.ConnectAPIBase(),
		"diauth":             hosts.DIAuthBase(),
		"mobile integration": hosts.MobileIntegrationBase(),
	}

	for name, base := range bases {
		t.Run(name, func(t *testing.T) {
			h := newRefreshHarnessWithHosts(t, hosts, func(_ *http.Request, _ int) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{}`), nil
			})
			seedValidTokens(h)

			resp, err := h.refresher.Do(t.Context(), testPrincipalID, mustRequest(t, base+apiPath))
			if err != nil {
				t.Fatalf("Do(%q): %v", base+apiPath, err)
			}
			defer func() { _ = resp.Body.Close() }()

			recorded := h.transport.recorded()
			if len(recorded) != 1 {
				t.Fatalf("%d requests, want 1", len(recorded))
			}
			if want := "Bearer " + storedToken; recorded[0].authHeader != want {
				t.Errorf("Authorization = %q, want %q", recorded[0].authHeader, want)
			}
		})
	}
}

// The replay after a 401 must be checked again, so a Doer that rewrites the
// caller's request between attempts cannot redirect an authorized retry.
func TestDoRefusesAForeignHostOnTheReplayPath(t *testing.T) {
	req := mustRequest(t, offlineHosts(t).ConnectAPIBase()+apiPath)

	h := newRefreshHarness(t, func(sent *http.Request, _ int) (*http.Response, error) {
		if sent.URL.Path == protocol.PathDIToken {
			return rotatedTokenResponse(), nil
		}
		if sent.URL.Host == foreignHost {
			t.Error("the replay reached an off-boundary host")
			return jsonResponse(http.StatusOK, `{}`), nil
		}
		// Move the caller's request off the boundary before the replay clones it.
		req.URL.Host = foreignHost
		return jsonResponse(http.StatusUnauthorized, `{"message":"expired"}`), nil
	})
	seedValidTokens(h)

	resp, err := h.refresher.Do(t.Context(), testPrincipalID, req)
	if !errors.Is(err, auth.ErrForeignHost) {
		t.Fatalf("err = %v, want ErrForeignHost on the replay", err)
	}
	if resp != nil {
		t.Error("a response was returned alongside the replay refusal")
	}
	if got := h.transport.countFor(apiPath); got != 1 {
		t.Errorf("%d API calls, want only the first attempt", got)
	}
}

func TestDoAllowsTheChinaRegionAndRefusesTheGlobalOne(t *testing.T) {
	h := newRefreshHarnessWithHosts(t, garminHosts(t, protocol.DomainChina),
		func(_ *http.Request, _ int) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{}`), nil
		})
	seedValidTokens(h)

	resp, err := h.refresher.Do(t.Context(), testPrincipalID,
		mustRequest(t, "https://connectapi.garmin.cn"+apiPath))
	if err != nil {
		t.Fatalf("a .cn base was refused for a .cn region: %v", err)
	}
	_ = resp.Body.Close()

	assertRefused(t, h, "https://connectapi.garmin.com"+apiPath)
}

func TestDoRefusalRendersNoCredentialAndNoURL(t *testing.T) {
	h := newRefreshHarness(t, alwaysRotate)
	seedValidTokens(h)

	_, err := h.refresher.Do(t.Context(), testPrincipalID,
		mustRequest(t, "https://"+foreignHost+apiPath+"?token=secret-0700"))
	if !errors.Is(err, auth.ErrForeignHost) {
		t.Fatalf("err = %v, want ErrForeignHost", err)
	}

	message := err.Error()
	for _, forbidden := range []string{storedToken, storedRefresh, "secret-0700", apiPath, foreignHost} {
		if strings.Contains(message, forbidden) {
			t.Errorf("the refusal message rendered %q: %s", forbidden, message)
		}
	}
}
