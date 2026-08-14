package testkit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// contentTypeForm is the media type of the widget HTML form posts.
const contentTypeForm = "application/x-www-form-urlencoded"

func post(t *testing.T, client *http.Client, rawURL, contentType, body string) protocol.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, rawURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return protocol.Response{Status: resp.StatusCode, Header: resp.Header, Body: payload}
}

func get(t *testing.T, client *http.Client, rawURL string) protocol.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return protocol.Response{Status: resp.StatusCode, Header: resp.Header, Body: payload}
}

func TestServerHostsPointAtTheFakeOnly(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())
	hosts := srv.Hosts(protocol.DomainGlobal)

	urls := []string{
		hosts.MobileLoginURL(),
		hosts.PortalLoginURL(),
		hosts.WidgetSignInURL(),
		hosts.DITokenURL(),
		hosts.SocialProfileURL(),
		hosts.IOSServiceURL(),
		hosts.PortalServiceURL(),
	}
	for _, raw := range urls {
		if !strings.HasPrefix(raw, srv.BaseURL()) {
			t.Fatalf("url %q does not start with the fake base %q", raw, srv.BaseURL())
		}
		if strings.Contains(raw, "garmin.com") || strings.Contains(raw, "garmin.cn") {
			t.Fatalf("url %q still points at the real Garmin", raw)
		}
	}
}

func TestServerScriptedLoginScenarios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		behavior    Behavior
		wantOutcome protocol.Outcome
		wantTicket  string
		wantRetry   time.Duration
	}{
		{
			name:        "ios login success returns a service ticket",
			behavior:    JSON(http.StatusOK, LoginSuccessJSON("ST-fake-1001")),
			wantOutcome: protocol.OutcomeSuccess,
			wantTicket:  "ST-fake-1001",
		},
		{
			name:        "mfa required",
			behavior:    JSON(http.StatusOK, LoginMFARequiredJSON("email")),
			wantOutcome: protocol.OutcomeMFARequired,
		},
		{
			name:        "invalid credentials",
			behavior:    JSON(http.StatusOK, LoginInvalidCredentialsJSON()),
			wantOutcome: protocol.OutcomeInvalidCredentials,
		},
		{
			name:        "403 bot challenge",
			behavior:    HTML(http.StatusForbidden, BotChallengeHTML()),
			wantOutcome: protocol.OutcomeBotChallenge,
		},
		{
			name:        "429 with retry-after",
			behavior:    RateLimited(90),
			wantOutcome: protocol.OutcomeRateLimited,
			wantRetry:   90 * time.Second,
		},
		{
			name:        "429 reported inside the json body",
			behavior:    JSON(http.StatusOK, LoginRateLimitedBodyJSON()),
			wantOutcome: protocol.OutcomeRateLimited,
		},
		{
			name:        "non json payload",
			behavior:    Behavior{Status: http.StatusOK, ContentType: "text/plain", Body: "not json at all"},
			wantOutcome: protocol.OutcomeUnknown,
		},
		{
			name:        "truncated json payload",
			behavior:    JSON(http.StatusOK, `{"responseStatus":`),
			wantOutcome: protocol.OutcomeUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := NewServer(t, NewScript().With(protocol.PathMobileLogin, tc.behavior))
			hosts := srv.Hosts(protocol.DomainGlobal)

			resp := post(t, srv.Client(), hosts.MobileLoginURL(), ContentTypeJSON, `{"username":"fake@example.test"}`)
			got := protocol.ClassifyJSONLogin(resp)

			if got.Outcome != tc.wantOutcome {
				t.Fatalf("Outcome = %v, want %v", got.Outcome, tc.wantOutcome)
			}
			if got.ServiceTicket != tc.wantTicket {
				t.Fatalf("ServiceTicket = %q, want %q", got.ServiceTicket, tc.wantTicket)
			}
			if got.RetryAfter != tc.wantRetry {
				t.Fatalf("RetryAfter = %v, want %v", got.RetryAfter, tc.wantRetry)
			}
		})
	}
}

func TestServerScriptsMFAThenVerifyCode(t *testing.T) {
	t.Parallel()

	script := NewScript().
		With(protocol.PathMobileLogin, JSON(http.StatusOK, LoginMFARequiredJSON("sms"))).
		With(protocol.PathMobileMFAVerifyCode,
			JSON(http.StatusOK, LoginInvalidCredentialsJSON()),
			JSON(http.StatusOK, LoginSuccessJSON("ST-fake-1002")),
		)
	srv := NewServer(t, script)
	hosts := srv.Hosts(protocol.DomainGlobal)

	login := protocol.ClassifyJSONLogin(post(t, srv.Client(), hosts.MobileLoginURL(), ContentTypeJSON, `{}`))
	if login.Outcome != protocol.OutcomeMFARequired || login.MFAMethod != "sms" {
		t.Fatalf("login = %v/%q, want mfa_required/sms", login.Outcome, login.MFAMethod)
	}

	first := protocol.ClassifyJSONLogin(post(t, srv.Client(), hosts.MobileMFAVerifyCodeURL(), ContentTypeJSON, `{}`))
	if first.Outcome != protocol.OutcomeInvalidCredentials {
		t.Fatalf("first verify = %v, want invalid_credentials", first.Outcome)
	}

	second := protocol.ClassifyJSONLogin(post(t, srv.Client(), hosts.MobileMFAVerifyCodeURL(), ContentTypeJSON, `{}`))
	if second.Outcome != protocol.OutcomeSuccess || second.ServiceTicket != "ST-fake-1002" {
		t.Fatalf("second verify = %v/%q, want success/ST-fake-1002", second.Outcome, second.ServiceTicket)
	}

	// The last scripted behavior repeats once the queue is drained.
	third := protocol.ClassifyJSONLogin(post(t, srv.Client(), hosts.MobileMFAVerifyCodeURL(), ContentTypeJSON, `{}`))
	if third.Outcome != protocol.OutcomeSuccess {
		t.Fatalf("third verify = %v, want success", third.Outcome)
	}
}

func TestServerScriptsWidgetFlow(t *testing.T) {
	t.Parallel()

	script := NewScript().
		With(protocol.PathWidgetEmbed, HTML(http.StatusOK, WidgetSignInPageHTML("fake-csrf-1001"))).
		With(protocol.PathWidgetSignIn,
			HTML(http.StatusOK, WidgetSignInPageHTML("fake-csrf-1002")),
			HTML(http.StatusOK, WidgetMFAHTML(WidgetTitleTOTPMFA)),
		).
		With(protocol.PathWidgetVerifyMFA, HTML(http.StatusOK, WidgetSuccessHTML("ST-fake-1003")))
	srv := NewServer(t, script)
	hosts := srv.Hosts(protocol.DomainChina)

	embed := protocol.ClassifyWidgetSignInPage(get(t, srv.Client(), hosts.WidgetEmbedURL()))
	if embed.Outcome != protocol.OutcomeSuccess || embed.CSRFToken != "fake-csrf-1001" {
		t.Fatalf("embed = %v/%q", embed.Outcome, embed.CSRFToken)
	}

	signIn := protocol.ClassifyWidgetSignInPage(get(t, srv.Client(), hosts.WidgetSignInURL()))
	if signIn.CSRFToken != "fake-csrf-1002" {
		t.Fatalf("signin csrf = %q", signIn.CSRFToken)
	}

	credentials := protocol.ClassifyWidgetLogin(post(t, srv.Client(), hosts.WidgetSignInURL(),
		contentTypeForm, "username=fake%40example.test"))
	if credentials.Outcome != protocol.OutcomeMFARequired {
		t.Fatalf("credential post = %v, want mfa_required", credentials.Outcome)
	}

	verified := protocol.ClassifyWidgetLogin(post(t, srv.Client(), hosts.WidgetVerifyMFAURL(),
		contentTypeForm, "mfa-code=000000"))
	if verified.Outcome != protocol.OutcomeSuccess || verified.ServiceTicket != "ST-fake-1003" {
		t.Fatalf("verify = %v/%q", verified.Outcome, verified.ServiceTicket)
	}
}

func TestServerSlowResponseTripsClientTimeout(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript().With(
		protocol.PathMobileLogin,
		JSON(http.StatusOK, LoginSuccessJSON("ST-fake-1004")).WithDelay(300*time.Millisecond),
	))

	client := srv.Client()
	client.Timeout = 20 * time.Millisecond

	loginURL := srv.Hosts(protocol.DomainGlobal).MobileLoginURL()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, loginURL, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestServerRecordsRequestsWithoutSharingState(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript().With(protocol.PathMobileLogin, JSON(http.StatusOK, LoginSuccessJSON("ST-fake-1005"))))
	target := srv.Hosts(protocol.DomainGlobal).MobileLoginURL() + "?" + url.Values{
		"clientId": {protocol.ClientIDIOS},
		"locale":   {protocol.LoginLocale},
	}.Encode()

	_ = post(t, srv.Client(), target, ContentTypeJSON, `{"username":"fake@example.test"}`)

	recorded := srv.Requests()
	if len(recorded) != 1 {
		t.Fatalf("len(Requests()) = %d, want 1", len(recorded))
	}
	if recorded[0].Method != http.MethodPost || recorded[0].Path != protocol.PathMobileLogin {
		t.Fatalf("recorded = %s %s", recorded[0].Method, recorded[0].Path)
	}
	if got := recorded[0].Query.Get("clientId"); got != protocol.ClientIDIOS {
		t.Fatalf("clientId = %q, want %q", got, protocol.ClientIDIOS)
	}
	if !strings.Contains(string(recorded[0].Body), "fake@example.test") {
		t.Fatalf("body = %q", string(recorded[0].Body))
	}

	recorded[0].Path = "mutated"
	recorded[0].Query.Set("clientId", "mutated")
	again := srv.Requests()
	if again[0].Path != protocol.PathMobileLogin || again[0].Query.Get("clientId") != protocol.ClientIDIOS {
		t.Fatal("Requests() exposed shared state")
	}
}

func TestServerUnscriptedPathIsNotFound(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	resp := get(t, srv.Client(), srv.BaseURL()+"/unscripted/path")
	if resp.Status != http.StatusNotFound {
		t.Fatalf("Status = %d, want 404", resp.Status)
	}
	if len(srv.Requests()) != 1 {
		t.Fatal("unscripted request was not recorded")
	}
}

func TestScriptWithDoesNotMutateTheReceiver(t *testing.T) {
	t.Parallel()

	base := NewScript().With(protocol.PathMobileLogin, JSON(http.StatusOK, LoginSuccessJSON("ST-fake-1006")))
	derived := base.
		With(protocol.PathMobileLogin, JSON(http.StatusOK, LoginInvalidCredentialsJSON())).
		With(protocol.PathPortalLogin, JSON(http.StatusOK, LoginSuccessJSON("ST-fake-1007")))

	baseSrv := NewServer(t, base)
	got := protocol.ClassifyJSONLogin(post(t, baseSrv.Client(),
		baseSrv.Hosts(protocol.DomainGlobal).MobileLoginURL(), ContentTypeJSON, "{}"))
	if got.Outcome != protocol.OutcomeSuccess {
		t.Fatalf("base script changed: Outcome = %v", got.Outcome)
	}

	derivedSrv := NewServer(t, derived)
	got = protocol.ClassifyJSONLogin(post(t, derivedSrv.Client(),
		derivedSrv.Hosts(protocol.DomainGlobal).MobileLoginURL(), ContentTypeJSON, "{}"))
	if got.Outcome != protocol.OutcomeInvalidCredentials {
		t.Fatalf("derived script Outcome = %v, want invalid_credentials", got.Outcome)
	}
}
