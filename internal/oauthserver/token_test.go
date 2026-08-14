package oauthserver

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// issuedCode drives a full authorization to the point of holding a usable code.
func (h *harness) issuedCode(t *testing.T, req AuthorizeRequest) string {
	t.Helper()
	capability, _ := h.authenticated(t, req)
	completion, err := h.srv.GrantConsent(context.Background(), capability)
	if err != nil {
		t.Fatalf("GrantConsent: %v", err)
	}
	parsed, err := url.Parse(completion.RedirectTo)
	if err != nil {
		t.Fatalf("redirect does not parse: %v", err)
	}
	return parsed.Query().Get("code")
}

func (h *harness) exchange(t *testing.T, req TokenRequest) (TokenResponse, error) {
	t.Helper()
	return h.srv.Token(context.Background(), req)
}

func asTokenError(t *testing.T, err error) *TokenError {
	t.Helper()
	var tokenErr *TokenError
	if !errors.As(err, &tokenErr) {
		t.Fatalf("error is not a *TokenError: %v", err)
	}
	return tokenErr
}

func codeGrantRequest(code string) TokenRequest {
	return TokenRequest{
		GrantType:    GrantAuthorizationCode,
		ClientID:     testClientID,
		Code:         code,
		RedirectURI:  testRedirect,
		CodeVerifier: testVerifier,
		Resource:     testResourceURI,
	}
}

func TestTokenRefusesEveryGrantButTheTwoImplemented(t *testing.T) {
	h := newHarness(t)

	for _, grant := range []string{
		"", "password", "implicit", "client_credentials", "token",
		"urn:ietf:params:oauth:grant-type:token-exchange",
		"urn:ietf:params:oauth:grant-type:jwt-bearer",
		"authorization_Code", "refresh",
	} {
		t.Run(grant, func(t *testing.T) {
			_, err := h.exchange(t, TokenRequest{GrantType: grant, ClientID: testClientID})

			if !errors.Is(err, ErrUnsupportedGrantType) {
				t.Fatalf("error = %v, want ErrUnsupportedGrantType", err)
			}
			tokenErr := asTokenError(t, err)
			if tokenErr.Code() != ErrorUnsupportedGrantType {
				t.Fatalf("Code() = %q, want %q", tokenErr.Code(), ErrorUnsupportedGrantType)
			}
			if tokenErr.Status() != http.StatusBadRequest {
				t.Fatalf("Status() = %d, want 400", tokenErr.Status())
			}
		})
	}
}

func TestTokenRejectsAnUnknownClient(t *testing.T) {
	h := newHarness(t)
	code := h.issuedCode(t, validAuthorizeRequest())
	req := codeGrantRequest(code)
	req.ClientID = testUnknownClient

	_, err := h.exchange(t, req)

	if !errors.Is(err, ErrUnknownClient) {
		t.Fatalf("error = %v, want ErrUnknownClient", err)
	}
	tokenErr := asTokenError(t, err)
	if tokenErr.Code() != ErrorInvalidClient {
		t.Fatalf("Code() = %q, want %q", tokenErr.Code(), ErrorInvalidClient)
	}
	if tokenErr.Status() != http.StatusUnauthorized {
		t.Fatalf("Status() = %d, want 401", tokenErr.Status())
	}
}

func TestTokenRejectsAPublicClientThatPresentsASecret(t *testing.T) {
	h := newHarness(t)
	code := h.issuedCode(t, validAuthorizeRequest())
	req := codeGrantRequest(code)
	req.ClientSecret = SecretFromString(testClientSecret)

	_, err := h.exchange(t, req)

	if !errors.Is(err, ErrClientAuthFailed) {
		t.Fatalf("error = %v, want ErrClientAuthFailed", err)
	}
	if got := asTokenError(t, err).Code(); got != ErrorInvalidClient {
		t.Fatalf("Code() = %q, want %q", got, ErrorInvalidClient)
	}
}

func TestTokenAuthenticatesAConfidentialClient(t *testing.T) {
	spec := publicClientSpec()
	spec.TokenEndpointAuthMethod = string(AuthMethodSecretPost)
	spec.SecretHashHex = SecretFromString(testClientSecret).Lookup().Hex()
	h := newHarness(t, spec)

	code := h.issuedCode(t, validAuthorizeRequest())
	req := codeGrantRequest(code)
	req.ClientSecret = SecretFromString(testClientSecret)
	if _, err := h.exchange(t, req); err != nil {
		t.Fatalf("a correct client secret was refused: %v", err)
	}

	wrong := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))
	wrong.ClientSecret = SecretFromString(testClientSecret + "x")
	if _, err := h.exchange(t, wrong); !errors.Is(err, ErrClientAuthFailed) {
		t.Fatalf("error = %v, want ErrClientAuthFailed", err)
	}

	none := codeGrantRequest(h.issuedCode(t, validAuthorizeRequest()))
	if _, err := h.exchange(t, none); !errors.Is(err, ErrClientAuthFailed) {
		t.Fatalf("a confidential client with no secret: error = %v, want ErrClientAuthFailed", err)
	}
}

func TestTokenErrorsNeverCarryCredentialMaterial(t *testing.T) {
	h := newHarness(t)
	code := h.issuedCode(t, validAuthorizeRequest())
	req := codeGrantRequest(code)
	req.CodeVerifier = strings.Repeat("z", 43)

	_, err := h.exchange(t, req)

	tokenErr := asTokenError(t, err)
	for _, text := range []string{err.Error(), tokenErr.Error(), tokenErr.Description()} {
		for label, secret := range map[string]string{
			"authorization code": code,
			"code verifier":      testVerifier,
			"presented verifier": strings.Repeat("z", 43),
			"client secret":      testClientSecret,
		} {
			if strings.Contains(text, secret) {
				t.Fatalf("error text leaked the %s: %q", label, text)
			}
		}
	}
}

func tokenForm() url.Values {
	return url.Values{
		paramGrantType:    {GrantAuthorizationCode},
		paramClientID:     {testClientID},
		paramCode:         {"the-code"},
		paramRedirectURI:  {testRedirect},
		paramCodeVerifier: {testVerifier},
		paramResource:     {testResourceURI},
		paramScope:        {testScopeProfile},
		paramRefreshToken: {"the-refresh-token"},
	}
}

func TestParseTokenFormReadsEveryParameter(t *testing.T) {
	got, err := ParseTokenForm(tokenForm(), http.Header{})
	if err != nil {
		t.Fatalf("ParseTokenForm: %v", err)
	}

	want := TokenRequest{
		GrantType:    GrantAuthorizationCode,
		ClientID:     testClientID,
		Code:         "the-code",
		RedirectURI:  testRedirect,
		CodeVerifier: testVerifier,
		Resource:     testResourceURI,
		Scope:        testScopeProfile,
		RefreshToken: "the-refresh-token",
	}
	if got.GrantType != want.GrantType || got.ClientID != want.ClientID ||
		got.Code != want.Code || got.RedirectURI != want.RedirectURI ||
		got.CodeVerifier != want.CodeVerifier || got.Resource != want.Resource ||
		got.Scope != want.Scope || got.RefreshToken != want.RefreshToken {
		t.Fatalf("ParseTokenForm() = %+v, want %+v", got, want)
	}
	if !got.ClientSecret.IsZero() {
		t.Fatal("a form with no secret produced a secret")
	}
}

func TestParseTokenFormRejectsDuplicateSecurityParameters(t *testing.T) {
	for _, key := range []string{
		paramGrantType, paramClientID, paramCode, paramRedirectURI,
		paramCodeVerifier, paramRefreshToken, paramScope, paramResource, paramClientSecret,
	} {
		t.Run(key, func(t *testing.T) {
			form := tokenForm()
			form.Add(key, "second-value")
			form.Add(key, "third-value")

			_, err := ParseTokenForm(form, http.Header{})
			if !errors.Is(err, ErrDuplicateParameter) {
				t.Fatalf("ParseTokenForm error = %v, want ErrDuplicateParameter", err)
			}
		})
	}
}

func TestParseTokenFormReadsBasicAuthentication(t *testing.T) {
	form := tokenForm()
	form.Del(paramClientID)
	header := http.Header{}
	header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
		[]byte(url.QueryEscape(testClientID)+":"+url.QueryEscape(testClientSecret))))

	got, err := ParseTokenForm(form, header)
	if err != nil {
		t.Fatalf("ParseTokenForm: %v", err)
	}
	if got.ClientID != testClientID {
		t.Fatalf("ClientID = %q, want %q", got.ClientID, testClientID)
	}
	if got.ClientSecret.Reveal() != testClientSecret {
		t.Fatal("the basic-auth client secret was not read")
	}
}

func TestParseTokenFormRefusesConflictingClientCredentials(t *testing.T) {
	form := tokenForm()
	form.Set(paramClientSecret, "in-the-body")
	header := http.Header{}
	header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
		[]byte(testClientID+":"+testClientSecret)))

	if _, err := ParseTokenForm(form, header); !errors.Is(err, ErrDuplicateParameter) {
		t.Fatalf("ParseTokenForm error = %v, want ErrDuplicateParameter", err)
	}
}

func TestParseTokenFormRefusesAMismatchedBasicClientID(t *testing.T) {
	form := tokenForm()
	header := http.Header{}
	header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
		[]byte("another-client:"+testClientSecret)))

	if _, err := ParseTokenForm(form, header); !errors.Is(err, ErrDuplicateParameter) {
		t.Fatalf("ParseTokenForm error = %v, want ErrDuplicateParameter", err)
	}
}

func TestParseTokenFormRejectsAMalformedAuthorizationHeader(t *testing.T) {
	for name, value := range map[string]string{
		"not base64":  "Basic !!!!",
		"no colon":    "Basic " + base64.StdEncoding.EncodeToString([]byte("no-colon")),
		"bearer":      "Bearer some-token",
		"empty basic": "Basic ",
	} {
		t.Run(name, func(t *testing.T) {
			header := http.Header{}
			header.Set("Authorization", value)

			if _, err := ParseTokenForm(tokenForm(), header); !errors.Is(err, ErrClientAuthFailed) {
				t.Fatalf("ParseTokenForm error = %v, want ErrClientAuthFailed", err)
			}
		})
	}
}
