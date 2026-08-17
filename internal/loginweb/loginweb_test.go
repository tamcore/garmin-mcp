package loginweb_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// Synthetic credentials. Nothing here is a real account.
// Form field names, spelled once.
const (
	fieldCSRF     = "csrf_token"
	fieldEmail    = "email"
	fieldPassword = "password"
	fieldCode     = "code"
)

const (
	testEmail    = "person@example.test"
	testPassword = "synthetic-password"
	testCode     = "123456"
	testMethod   = "email"
	testTxnID    = "0123456789abcdef"
)

// fakeAuthenticator is the login seam under test control. It records what it was
// called with, so a test can prove the credentials reached Garmin exactly once and
// that nothing else did.
type fakeAuthenticator struct {
	loginAttempt loginweb.Attempt
	loginErr     error
	mfaAttempt   loginweb.Attempt
	mfaErr       error
	// mfaResponses, when non-empty, answers CompleteMFA one call at a time
	// instead of the fixed mfaAttempt/mfaErr pair, so a test can script a
	// rejected code followed by a correct one.
	mfaResponses []mfaResponse

	logins    int
	mfaCalls  int
	lastEmail string
	lastPass  string
	lastCode  string
	lastTxnID string
}

// mfaResponse is one scripted CompleteMFA answer.
type mfaResponse struct {
	attempt loginweb.Attempt
	err     error
}

func (f *fakeAuthenticator) Login(_ context.Context, email, password string) (loginweb.Attempt, error) {
	f.logins++
	f.lastEmail = email
	f.lastPass = password
	return f.loginAttempt, f.loginErr
}

func (f *fakeAuthenticator) CompleteMFA(
	_ context.Context, transactionID, code string,
) (loginweb.Attempt, error) {
	f.mfaCalls++
	f.lastTxnID = transactionID
	f.lastCode = code
	if len(f.mfaResponses) > 0 {
		next := f.mfaResponses[0]
		f.mfaResponses = f.mfaResponses[1:]
		return next.attempt, next.err
	}
	return f.mfaAttempt, f.mfaErr
}

// succeeded and challenged are the two outcomes a Garmin login can report here.
func succeeded() loginweb.Attempt {
	return loginweb.Attempt{Strategy: "mobile_ios"}
}

func challenged() loginweb.Attempt {
	return loginweb.Attempt{
		Strategy:      "sso_widget",
		NeedsMFA:      true,
		TransactionID: testTxnID,
		MFAMethod:     testMethod,
	}
}

// browser is a cookie-keeping HTTP client against one test server, which is what a
// real browser is for this flow.
type browser struct {
	t      *testing.T
	client *http.Client
	base   string
}

func newBrowser(t *testing.T, handler http.Handler) *browser {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &browser{t: t, client: newBrowserClient(t), base: server.URL}
}

func newBrowserClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("build the cookie jar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (b *browser) get(path string) (*http.Response, string) {
	b.t.Helper()

	resp, err := b.client.Get(b.base + path)
	if err != nil {
		b.t.Fatalf("GET %s: %v", path, err)
	}
	return resp, readBody(b.t, resp)
}

func (b *browser) post(path string, form url.Values) (*http.Response, string) {
	b.t.Helper()

	resp, err := b.client.PostForm(b.base+path, form)
	if err != nil {
		b.t.Fatalf("POST %s: %v", path, err)
	}
	return resp, readBody(b.t, resp)
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	return string(body)
}

// csrfPattern extracts the hidden form token from a rendered page.
var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func csrfToken(t *testing.T, page string) string {
	t.Helper()

	match := csrfPattern.FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("page carries no CSRF token:\n%s", page)
	}
	return match[1]
}

func newServer(t *testing.T, authenticator loginweb.Authenticator) *loginweb.Server {
	t.Helper()

	server, err := loginweb.New(loginweb.Config{Authenticator: authenticator})
	if err != nil {
		t.Fatalf("loginweb.New returned error: %v", err)
	}
	return server
}

// submitCredentials walks the disclosure and credential pages and posts the form.
func submitCredentials(t *testing.T, b *browser) *http.Response {
	t.Helper()

	_, form := b.get("/login")
	resp, _ := b.post("/login", url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	})
	return resp
}

// TestCredentialLoginCompletesInOneRequest is the happy path: the disclosure page
// leads to the credential form, the form posts to this service, and the credentials
// reach Garmin exactly once.
func TestCredentialLoginCompletesInOneRequest(t *testing.T) {
	fake := &fakeAuthenticator{loginAttempt: succeeded()}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	_, disclosure := b.get("/")
	for _, want := range []string{"not", "Garmin", "forward"} {
		if !strings.Contains(disclosure, want) {
			t.Errorf("the disclosure page does not mention %q:\n%s", want, disclosure)
		}
	}

	resp := submitCredentials(t, b)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/done" {
		t.Errorf("Location = %q, want %q", got, "/done")
	}

	if fake.logins != 1 {
		t.Errorf("Garmin login was called %d times, want 1", fake.logins)
	}
	if fake.lastEmail != testEmail || fake.lastPass != testPassword {
		t.Error("the credentials did not reach the login call intact")
	}

	select {
	case <-server.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the transaction did not reach a terminal state")
	}
	if !server.Outcome().Succeeded() {
		t.Errorf("outcome = %+v, want a successful login", server.Outcome())
	}
}

// TestMFAContinuationUsesTheServerSideTransaction covers the second leg: the
// capability stays server-side, and the code travels with it.
func TestMFAContinuationUsesTheServerSideTransaction(t *testing.T) {
	fake := &fakeAuthenticator{loginAttempt: challenged(), mfaAttempt: succeeded()}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	resp := submitCredentials(t, b)
	if got := resp.Header.Get("Location"); got != "/mfa" {
		t.Fatalf("Location = %q, want %q", got, "/mfa")
	}

	resp, mfaForm := b.get("/mfa")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /mfa = %d, want 200", resp.StatusCode)
	}
	if strings.Contains(mfaForm, testTxnID) {
		t.Error("the MFA page carries the transaction capability")
	}

	resp, page := b.post("/mfa", url.Values{
		fieldCSRF: {csrfToken(t, mfaForm)},
		fieldCode: {testCode},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /mfa = %d, want 303; body=%s", resp.StatusCode, page)
	}
	if fake.mfaCalls != 1 {
		t.Errorf("CompleteMFA was called %d times, want 1", fake.mfaCalls)
	}
	if fake.lastCode != testCode {
		t.Errorf("code = %q, want %q", fake.lastCode, testCode)
	}
	if fake.lastTxnID != testTxnID {
		t.Errorf("transaction = %q, want the capability the login returned", fake.lastTxnID)
	}
	if !server.Outcome().Succeeded() {
		t.Errorf("outcome = %+v, want a successful login", server.Outcome())
	}
}

// TestOutOfOrderMFASubmissionIsRefused covers the forbidden transition: an OTP
// before any credentials must not reach Garmin.
func TestOutOfOrderMFASubmissionIsRefused(t *testing.T) {
	fake := &fakeAuthenticator{}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	_, form := b.get("/login")
	resp, _ := b.post("/mfa", url.Values{
		fieldCSRF: {csrfToken(t, form)},
		fieldCode: {testCode},
	})

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST /mfa before credentials = %d, want 404", resp.StatusCode)
	}
	if fake.mfaCalls != 0 {
		t.Error("an out-of-order code reached Garmin")
	}
}

// TestASecondLoginSubmissionIsRefused keeps the transaction single-use, so a
// replayed form cannot start a second Garmin login.
func TestASecondLoginSubmissionIsRefused(t *testing.T) {
	fake := &fakeAuthenticator{loginAttempt: succeeded()}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	_, form := b.get("/login")
	credentials := url.Values{
		fieldCSRF:     {csrfToken(t, form)},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	}

	if resp, _ := b.post("/login", credentials); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the first submission = %d, want 303", resp.StatusCode)
	}
	resp, _ := b.post("/login", credentials)

	if resp.StatusCode == http.StatusSeeOther {
		t.Error("the replayed submission was accepted")
	}
	if fake.logins != 1 {
		t.Errorf("Garmin login was called %d times, want 1", fake.logins)
	}
}

// TestServeBindsLoopbackOnlyAndStopsWhenTheTransactionEnds covers the one-shot
// listener: loopback only, never a wildcard bind, and gone once the login is done.
func TestServeBindsLoopbackOnlyAndStopsWhenTheTransactionEnds(t *testing.T) {
	fake := &fakeAuthenticator{loginAttempt: succeeded()}
	server := newServer(t, fake)

	listener, err := loginweb.ListenLoopback()
	if err != nil {
		t.Fatalf("ListenLoopback returned error: %v", err)
	}
	if address := listener.Addr().String(); !strings.HasPrefix(address, "127.0.0.1:") {
		t.Errorf("listener address = %q, want a 127.0.0.1 bind", address)
	}

	endpoint := loginweb.LoopbackURL(listener)
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") {
		t.Errorf("LoopbackURL = %q, want a loopback URL", endpoint)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, listener) }()

	b := &browser{t: t, client: newBrowserClient(t), base: endpoint}
	submitCredentials(t, b)

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return after the transaction completed")
	}
	if !server.Outcome().Succeeded() {
		t.Errorf("outcome = %+v, want a successful login", server.Outcome())
	}
}

// TestServeStopsWhenTheContextIsCancelled is the cancellation half of the same
// contract: no listener outlives the run.
func TestServeStopsWhenTheContextIsCancelled(t *testing.T) {
	server := newServer(t, &fakeAuthenticator{})

	listener, err := loginweb.ListenLoopback()
	if err != nil {
		t.Fatalf("ListenLoopback returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, listener) }()
	cancel()

	select {
	case err := <-served:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return after cancellation")
	}
	if server.Outcome().Succeeded() {
		t.Error("a cancelled run reported success")
	}
}

// TestExpiredTransactionRefusesCredentials keeps the absolute lifetime real.
func TestExpiredTransactionRefusesCredentials(t *testing.T) {
	fake := &fakeAuthenticator{loginAttempt: succeeded()}
	now := time.Now()
	server, err := loginweb.New(loginweb.Config{
		Authenticator: fake,
		TTL:           time.Minute,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("loginweb.New returned error: %v", err)
	}

	b := newBrowser(t, server.Handler())
	_, form := b.get("/login")
	token := csrfToken(t, form)

	now = now.Add(2 * time.Minute)

	resp, _ := b.post("/login", url.Values{
		fieldCSRF:     {token},
		fieldEmail:    {testEmail},
		fieldPassword: {testPassword},
	})
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("an expired transaction accepted credentials")
	}
	if fake.logins != 0 {
		t.Error("an expired transaction reached Garmin")
	}
}

// TestMFARejectedCodeMayBeRetried covers the retryable half: a rejected code
// re-renders the same form, and a correct code afterward still completes the
// login through the same transaction.
func TestMFARejectedCodeMayBeRetried(t *testing.T) {
	fake := &fakeAuthenticator{
		loginAttempt: challenged(),
		mfaResponses: []mfaResponse{
			{err: protocol.ErrMFARejected},
			{attempt: succeeded()},
		},
	}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	submitCredentials(t, b)
	_, mfaForm := b.get("/mfa")

	resp, retryForm := b.post("/mfa", url.Values{
		fieldCSRF: {csrfToken(t, mfaForm)},
		fieldCode: {"000000"},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /mfa with a rejected code = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(retryForm, "not accepted") {
		t.Errorf("page = %q, want it to say the code was not accepted", retryForm)
	}

	resp, _ = b.post("/mfa", url.Values{
		fieldCSRF: {csrfToken(t, retryForm)},
		fieldCode: {testCode},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("retry with the right code = %d, want 303", resp.StatusCode)
	}
	if fake.mfaCalls != 2 {
		t.Errorf("CompleteMFA was called %d times, want 2", fake.mfaCalls)
	}
}

// TestMFATerminalFailureEndsTheTransaction covers the other half: a failure that
// says nothing about the submitted code — an account lockout, here — must not be
// offered a retry, and the transaction must not survive it.
func TestMFATerminalFailureEndsTheTransaction(t *testing.T) {
	fake := &fakeAuthenticator{
		loginAttempt: challenged(),
		mfaErr:       protocol.ErrAccountLocked,
	}
	server := newServer(t, fake)
	b := newBrowser(t, server.Handler())

	submitCredentials(t, b)
	_, mfaForm := b.get("/mfa")

	resp, page := b.post("/mfa", url.Values{
		fieldCSRF: {csrfToken(t, mfaForm)},
		fieldCode: {testCode},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /mfa with a terminal failure = %d, want 404; body=%s", resp.StatusCode, page)
	}
	if strings.Contains(page, "not accepted") {
		t.Error("a terminal failure was rendered as a retryable rejected code")
	}

	// The transaction is over: a further submission finds nothing to submit to.
	resp, _ = b.post("/mfa", url.Values{
		fieldCSRF: {csrfToken(t, mfaForm)},
		fieldCode: {testCode},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a second submission after a terminal failure = %d, want 404", resp.StatusCode)
	}
	if fake.mfaCalls != 1 {
		t.Errorf("CompleteMFA was called %d times, want 1", fake.mfaCalls)
	}
}
