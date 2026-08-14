package loginweb

import (
	"net/http"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// The fixed remote routes. They are few, and every one of them refuses an
// unsolicited submission the same way, because a route that refuses differently is
// the route a probe learns from.
const (
	routeRemoteLogin       = "/login"
	routeRemoteCredentials = "/login/credentials"
	routeRemoteMFA         = "/login/mfa"
	routeRemoteConsent     = "/login/consent"
)

// The form field names and the consent decision that grants.
const (
	fieldToken    = "csrf_token"
	fieldEmail    = "email"
	fieldPassword = "password"
	fieldCode     = "code"
	fieldDecision = "decision"

	decisionAllow = "allow"

	// maxDecisionLen bounds the consent field, which is one short word.
	maxDecisionLen = 16
)

// Handler returns the router for this deployment.
//
// Every response carries the browser security headers and HSTS, including the
// generic 404 and the stylesheet, because a header that depends on the route is a
// header that will be forgotten on one.
func (s *RemoteServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+RemoteAuthorizePath, s.handleAuthorize)
	mux.HandleFunc("GET "+remoteStylesheetPath, s.handleStylesheet)
	mux.HandleFunc("GET "+routeRemoteLogin, s.handleDisclosure)
	mux.HandleFunc("POST "+routeRemoteLogin, s.handleContinue)
	mux.HandleFunc("GET "+routeRemoteCredentials, s.handleCredentialForm)
	mux.HandleFunc("POST "+routeRemoteCredentials, s.handleCredentialSubmit)
	mux.HandleFunc("GET "+routeRemoteMFA, s.handleMFAForm)
	mux.HandleFunc("POST "+routeRemoteMFA, s.handleMFASubmit)
	mux.HandleFunc("GET "+routeRemoteConsent, s.handleConsentForm)
	mux.HandleFunc("POST "+routeRemoteConsent, s.handleConsentSubmit)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { s.notFound(w) })

	return remoteSecureHeaders(mux, s.hsts)
}

// handleAuthorize is the OAuth authorization endpoint.
//
// The authorization server validates the client and the exact registered redirect
// URI before anything else, and only then is a transaction opened. The capability it
// mints goes into the cookie and the browser is sent to a clean fixed route, so the
// capability never reaches a path, a query, a proxy log or a history entry.
func (s *RemoteServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	authorization, err := s.authorizations.Begin(r.Context(), r.URL.Query())
	if err != nil {
		s.refuseAuthorization(w, r, err)
		return
	}

	session, err := newRemoteSession(authorization.Capability,
		s.deadline(authorization.ExpiresAt), s.entropy)
	if err != nil {
		s.unavailable(w)
		return
	}
	if err := s.sessions.add(s.now(), session); err != nil {
		s.unavailable(w)
		return
	}

	s.setCookie(w, session)
	s.log(r.Context(), "an authorization transaction was opened")
	http.Redirect(w, r, routeRemoteLogin, http.StatusSeeOther)
}

// deadline is the earlier of the transaction's own expiry and this server's cap, so
// a browser session can never outlive the transaction it addresses.
func (s *RemoteServer) deadline(transactionExpiry time.Time) time.Time {
	capped := s.now().Add(s.ttl)
	if transactionExpiry.IsZero() || capped.Before(transactionExpiry) {
		return capped
	}
	return transactionExpiry
}

// handleStylesheet serves the one embedded asset a page references.
func (s *RemoteServer) handleStylesheet(w http.ResponseWriter, _ *http.Request) {
	s.pages.serveStylesheet(w)
}

// handleDisclosure renders the non-binding disclosure: who is asking, where the
// answer goes, and for what. No credential has been entered at this point, and
// reading the page grants nothing.
func (s *RemoteServer) handleDisclosure(w http.ResponseWriter, r *http.Request) {
	session, disclosure, ok := s.live(w, r)
	if !ok {
		return
	}
	if session.state() != auth.StateCreated {
		s.notFound(w)
		return
	}
	s.pages.render(w, http.StatusOK, pageDisclosure,
		newRemotePageData(disclosure, session.formToken(), ""))
}

// handleContinue accepts the user's decision to go on to the credential form. It
// spends no attempt budget, because nothing has been guessed.
func (s *RemoteServer) handleContinue(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(r)
	if err != nil {
		s.refuse(w, err)
		return
	}
	if !s.parseBounded(w, r) {
		return
	}
	if err := session.confirm(s.now(), r.PostFormValue(fieldToken), auth.StateCreated); err != nil {
		s.refuseSession(w, session, err)
		return
	}
	http.Redirect(w, r, routeRemoteCredentials, http.StatusSeeOther)
}

// handleCredentialForm renders the Garmin credential form. The page states plainly
// whose it is and what it does with what is typed into it.
func (s *RemoteServer) handleCredentialForm(w http.ResponseWriter, r *http.Request) {
	session, disclosure, ok := s.live(w, r)
	if !ok {
		return
	}
	if session.state() != auth.StateCreated {
		s.notFound(w)
		return
	}
	s.pages.render(w, http.StatusOK, pageCredentials,
		newRemotePageData(disclosure, session.formToken(), ""))
}

// handleCredentialSubmit runs one Garmin login.
//
// The order of the checks is the security property: the transaction cookie first, so
// an unsolicited request is refused before anything is parsed; then the bounded
// body; then the form token, the deadline, the state and the attempt budget, all
// inside one accept call that also rotates the token; then the field bounds; and
// only then Garmin. The credentials are dropped the moment the call returns.
func (s *RemoteServer) handleCredentialSubmit(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(r)
	if err != nil {
		s.refuse(w, err)
		return
	}
	if !s.parseBounded(w, r) {
		return
	}
	if err := session.accept(s.now(), r.PostFormValue(fieldToken),
		auth.StateCreated, s.maxAttempts); err != nil {
		s.refuseSession(w, session, err)
		return
	}

	email := r.PostFormValue(fieldEmail)
	password := r.PostFormValue(fieldPassword)
	if len(email) > MaxEmailLen || len(password) > MaxPasswordLen {
		s.retry(w, r, session, pageCredentials, msgFieldTooLong)
		return
	}

	attempt, err := s.authenticator.Login(r.Context(), email, password)
	dropCredentials(&email, &password)

	switch {
	case err != nil:
		s.log(r.Context(), "garmin did not accept the credentials")
		s.retry(w, r, session, pageCredentials, msgLoginRejected)
	case attempt.NeedsMFA:
		s.challenge(w, r, session, attempt)
	default:
		s.resolvePrincipal(w, r, session, attempt)
	}
}
