package tools

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

type authStatusCallerFunc func(context.Context, string, *http.Request) (*http.Response, error)

func (f authStatusCallerFunc) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	return f(ctx, principal, req)
}

func authStatusContext(t *testing.T) context.Context {
	t.Helper()
	principal, err := identity.NewPrincipal(newToolsPrincipal)
	if err != nil {
		t.Fatalf("identity.NewPrincipal() = %v", err)
	}
	return identity.WithPrincipal(t.Context(), principal)
}

func authStatusService(t *testing.T, caller client.Caller) *service {
	t.Helper()
	svc, _ := newToolsService(t, testkit.NewScript())
	svc.caller = caller
	return svc
}

func responseStatus(status int) *http.Response {
	return responsePayload(status, `{"message":"refused"}`)
}

func responsePayload(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestGarminAuthStatusReturnsTheLiveAccount(t *testing.T) {
	t.Parallel()

	svc, _ := newToolsService(t, testkit.NewScript().With(client.PathSocialProfile,
		testkit.JSON(http.StatusOK, `{"profileId":900001,"fullName":"Fake Tester"}`)))
	server := newToolsServer(t, svc, newToolsServerConfig{readOnly: []newToolsRegistration{{
		name: ToolGarminAuthStatus, register: registerGarminAuthStatus,
	}}})

	got := newToolsCall(t, newToolsSession(t, server), ToolGarminAuthStatus, nil)
	if got["authenticated"] != true {
		t.Errorf("authenticated = %v, want true", got["authenticated"])
	}
	if got["account"] != "Fake Tester" {
		t.Errorf("account = %v, want Fake Tester", got["account"])
	}
	if _, present := got["reason"]; present {
		t.Errorf("reason = %v on a successful probe", got["reason"])
	}
}

func TestGarminAuthStatusAcceptsAProfileWithoutAFullName(t *testing.T) {
	t.Parallel()

	for _, profile := range []string{
		`{"profileId":900001}`,
		`{"profileId":900001,"fullName":""}`,
	} {
		svc, _ := newToolsService(t, testkit.NewScript().With(client.PathSocialProfile,
			testkit.JSON(http.StatusOK, profile)))

		got, err := svc.garminAuthStatus(authStatusContext(t))
		if err != nil {
			t.Fatalf("garminAuthStatus() = %v", err)
		}
		if !got.Authenticated {
			t.Error("authenticated = false after Garmin accepted the live profile request")
		}
		if got.Account != nil {
			t.Errorf("Account = %q, want omitted", *got.Account)
		}
	}
}

func TestGarminAuthStatusTrimsTheAccountName(t *testing.T) {
	t.Parallel()

	svc, _ := newToolsService(t, testkit.NewScript().With(client.PathSocialProfile,
		testkit.JSON(http.StatusOK, `{"profileId":900001,"fullName":"  Fake Tester  "}`)))
	got, err := svc.garminAuthStatus(authStatusContext(t))
	if err != nil {
		t.Fatalf("garminAuthStatus() = %v", err)
	}
	if got.Account == nil || *got.Account != "Fake Tester" {
		t.Errorf("Account = %v, want trimmed full name", got.Account)
	}
}

func TestGarminAuthStatusClassifiesUnusableCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		caller     client.Caller
		wantReason string
	}{
		{name: "no stored tokens", caller: authStatusError(auth.ErrNoTokens),
			wantReason: authStatusReasonNoCredentials},
		{name: "no refresh token", caller: authStatusError(auth.ErrNoRefreshToken),
			wantReason: authStatusReasonNoCredentials},
		{name: "refresh rejected", caller: authStatusError(auth.ErrRefreshRejected),
			wantReason: authStatusReasonRejected},
		{name: "profile unauthorized", caller: authStatusResponse(http.StatusUnauthorized),
			wantReason: authStatusReasonRejected},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := authStatusService(t, tc.caller).garminAuthStatus(authStatusContext(t))
			if err != nil {
				t.Fatalf("garminAuthStatus() = %v", err)
			}
			if got.Authenticated {
				t.Error("authenticated = true, want false")
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Account != nil {
				t.Errorf("Account = %v, want omitted", *got.Account)
			}
		})
	}
}

func TestGarminAuthStatusDoesNotMisreportOperationalFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		caller client.Caller
		want   error
	}{
		{name: "forbidden", caller: authStatusResponse(http.StatusForbidden), want: client.ErrAuthentication},
		{name: "rate limited", caller: authStatusResponse(http.StatusTooManyRequests), want: client.ErrRateLimited},
		{name: "server failure", caller: authStatusResponse(http.StatusInternalServerError), want: client.ErrServer},
		{name: "malformed response", caller: authStatusMalformedResponse(), want: client.ErrMalformedPayload},
		{name: "temporary", caller: authStatusError(context.DeadlineExceeded), want: context.DeadlineExceeded},
		{name: "foreign host", caller: authStatusError(auth.ErrForeignHost), want: auth.ErrForeignHost},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := authStatusService(t, tc.caller).garminAuthStatus(authStatusContext(t))
			if !errors.Is(err, tc.want) {
				t.Errorf("garminAuthStatus() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestGarminAuthStatusRejectsAnUnattributedRequest(t *testing.T) {
	t.Parallel()

	svc, _ := newToolsService(t, testkit.NewScript())
	_, err := svc.garminAuthStatus(t.Context())
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Errorf("garminAuthStatus() = %v, want ErrNoPrincipal", err)
	}
}

func TestGarminAuthStatusRejectsUnknownArguments(t *testing.T) {
	t.Parallel()

	svc, _ := newToolsService(t, testkit.NewScript())
	server := newToolsServer(t, svc, newToolsServerConfig{readOnly: []newToolsRegistration{{
		name: ToolGarminAuthStatus, register: registerGarminAuthStatus,
	}}})
	text := newToolsCallError(t, newToolsSession(t, server), ToolGarminAuthStatus,
		map[string]any{keyUserID: "another-account"})
	if strings.Contains(text, "another-account") {
		t.Errorf("error %q echoed the rejected account selector", text)
	}
}

func TestGarminAuthStatusLogValueOmitsTheAccountName(t *testing.T) {
	t.Parallel()

	account := "Sensitive Person"
	rendered := (GarminAuthStatus{Authenticated: true, Account: &account}).LogValue().String()
	if strings.Contains(rendered, account) {
		t.Errorf("LogValue() = %q, which carries the account name", rendered)
	}
	if !strings.Contains(rendered, "account=set") {
		t.Errorf("LogValue() = %q, want account presence", rendered)
	}
}

func authStatusError(err error) client.Caller {
	return authStatusCallerFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		return nil, err
	})
}

func authStatusResponse(status int) client.Caller {
	return authStatusCallerFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		return responseStatus(status), nil
	})
}

func authStatusMalformedResponse() client.Caller {
	return authStatusCallerFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		return responsePayload(http.StatusOK, `{`), nil
	})
}
