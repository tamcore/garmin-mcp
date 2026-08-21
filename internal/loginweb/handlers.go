package loginweb

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// The routes. They are fixed and few: a browser needs no more, and every additional
// route is another one that has to refuse an unsolicited submission correctly.
const (
	routeRoot  = "/"
	routeLogin = "/login"
	routeMFA   = "/mfa"
	routeDone  = "/done"
)

// cookieName is the per-run capability cookie.
//
// It carries neither the __Host- prefix nor Secure, because both require HTTPS and
// this is the plain-HTTP loopback profile. It is host-only, path-scoped, HttpOnly,
// SameSite=Strict, and it expires with the run.
const cookieName = "garmin_mcp_login"

// Refusal reasons. They are internal: a page shows generic text, because a browser
// that guessed a route must not learn whether it guessed a live one.
var (
	errRefused            = errors.New("loginweb: request refused")
	errTransactionExpired = errors.New("loginweb: the login run expired")
	errAttemptsExhausted  = errors.New("loginweb: the attempt budget is used up")
)

// The generic messages a page may show. None quotes a submitted value, and none
// distinguishes a wrong password from an unknown account.
const (
	msgLoginRejected = "Garmin did not accept those credentials. Check them and try again."
	msgCodeRejected  = "That code was not accepted. Check it and try again."
	msgFieldTooLong  = "One of the values is longer than this form accepts."
	msgExhausted     = "Too many attempts. Start a new login from the terminal."
)

// Handler returns the router for this run.
//
// Every response carries the browser security headers, including the generic 404,
// because a header that depends on the route is a header that will be forgotten on
// one.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+routeRoot, s.handleDisclosure)
	mux.HandleFunc("GET "+stylesheetPath, s.handleStylesheet)
	mux.HandleFunc("GET "+routeLogin, s.handleLoginForm)
	mux.HandleFunc("POST "+routeLogin, s.handleLoginSubmit)
	mux.HandleFunc("GET "+routeMFA, s.handleMFAForm)
	mux.HandleFunc("POST "+routeMFA, s.handleMFASubmit)
	mux.HandleFunc("GET "+routeDone, s.handleDone)

	return secureHeaders(mux)
}

// handleDisclosure renders the non-binding disclosure and installs the run cookie.
func (s *Server) handleDisclosure(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != routeRoot || s.ended() {
		s.notFound(w)
		return
	}

	s.setRunCookie(w)
	s.pages.render(w, http.StatusOK, pageDisclosure, newPageData(s.txn.formToken(), ""))
}

// handleStylesheet serves the one embedded asset a page references.
func (s *Server) handleStylesheet(w http.ResponseWriter, _ *http.Request) {
	s.pages.serveStylesheet(w)
}

// handleLoginForm renders the credential form.
func (s *Server) handleLoginForm(w http.ResponseWriter, _ *http.Request) {
	if s.ended() || s.txn.state() != auth.StateCreated {
		s.notFound(w)
		return
	}

	s.setRunCookie(w)
	s.pages.render(w, http.StatusOK, pageCredentials, newPageData(s.txn.formToken(), ""))
}

// handleLoginSubmit runs one Garmin login.
//
// The order of checks is the security property: the run cookie first, so an
// unsolicited request is refused before anything is parsed; then the bounded body;
// then the form token, the deadline, the state, and the attempt budget, all inside
// one accept call that also rotates the token; then the field bounds; and only then
// Garmin.
func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.hasRunCookie(r) {
		s.notFound(w)
		return
	}
	if !s.parseBoundedForm(w, r) {
		return
	}
	if err := s.txn.accept(s.now(), r.PostFormValue("csrf_token"),
		auth.StateCreated, s.maxAttempts); err != nil {
		s.refuse(w, err)
		return
	}

	email := r.PostFormValue("email")
	password := r.PostFormValue("password")
	if len(email) > MaxEmailLen || len(password) > MaxPasswordLen {
		s.retry(w, pageCredentials, msgFieldTooLong)
		return
	}

	attempt, err := s.authenticator.Login(r.Context(), email, password)
	dropCredentials(&email, &password)

	switch {
	case err != nil:
		s.log(r.Context(), "garmin did not accept the credentials")
		s.retry(w, pageCredentials, msgLoginRejected)
	case attempt.NeedsMFA:
		s.challenge(w, r, attempt)
	default:
		s.complete(w, r, attempt)
	}
}

// handleMFAForm renders the one-time code form.
func (s *Server) handleMFAForm(w http.ResponseWriter, r *http.Request) {
	if !s.hasRunCookie(r) || s.ended() || s.txn.state() != auth.StateMFAPending {
		s.notFound(w)
		return
	}

	method, uncertain := s.txn.challenge()
	data := newPageData(s.txn.formToken(), "")
	data.MFAMethod = method
	data.DeliveryUncertain = uncertain
	s.pages.render(w, http.StatusOK, pageMFA, data)
}

// handleMFASubmit submits one one-time code against the server-side continuation.
func (s *Server) handleMFASubmit(w http.ResponseWriter, r *http.Request) {
	if !s.hasRunCookie(r) {
		s.notFound(w)
		return
	}
	if !s.parseBoundedForm(w, r) {
		return
	}
	if err := s.txn.accept(s.now(), r.PostFormValue("csrf_token"),
		auth.StateMFAPending, s.maxAttempts); err != nil {
		s.refuse(w, err)
		return
	}

	code := r.PostFormValue("code")
	if len(code) > MaxCodeLen {
		s.retry(w, pageMFA, msgFieldTooLong)
		return
	}

	attempt, err := s.authenticator.CompleteMFA(r.Context(), s.txn.continuation(), code)
	dropCredentials(&code)

	if err != nil {
		if mfaFailureIsRetryable(err) {
			s.log(r.Context(), "the one-time code was not accepted")
			s.retry(w, pageMFA, msgCodeRejected)
			return
		}
		s.log(r.Context(), "the login could not continue")
		s.txn.fail(err)
		s.notFound(w)
		return
	}
	s.complete(w, r, attempt)
}

// mfaFailureIsRetryable reports whether a CompleteMFA failure means only that the
// submitted code was wrong, so the user may try another one on the same
// transaction. Every other cause — an account lockout, a bot challenge, a rate
// limit, an exhausted or expired Garmin-side continuation, or anything
// unrecognized — is terminal: the pending login cannot continue, and offering a
// retry would resubmit against a failure retrying cannot fix.
func mfaFailureIsRetryable(err error) bool {
	return errors.Is(err, protocol.ErrMFARejected)
}

// handleDone renders the final page for a completed run.
func (s *Server) handleDone(w http.ResponseWriter, r *http.Request) {
	if !s.hasRunCookie(r) || !s.txn.snapshot().Succeeded() {
		s.notFound(w)
		return
	}
	s.pages.render(w, http.StatusOK, pageDone, newPageData("", ""))
}

// challenge records an MFA challenge and sends the browser to the code form.
func (s *Server) challenge(w http.ResponseWriter, r *http.Request, attempt Attempt) {
	if err := s.txn.challenged(attempt); err != nil {
		s.txn.fail(err)
		s.notFound(w)
		return
	}
	s.log(r.Context(), "garmin asked for a one-time code")
	http.Redirect(w, r, routeMFA, http.StatusSeeOther)
}

// complete records a successful login and sends the browser to the final page.
func (s *Server) complete(w http.ResponseWriter, r *http.Request, attempt Attempt) {
	if err := s.txn.authenticated(attempt); err != nil {
		s.txn.fail(err)
		s.notFound(w)
		return
	}
	s.log(r.Context(), "the garmin account was linked")
	http.Redirect(w, r, routeDone, http.StatusSeeOther)
}

// retry re-renders a form with generic text after a refused attempt.
func (s *Server) retry(w http.ResponseWriter, page, message string) {
	method, uncertain := s.txn.challenge()
	data := newPageData(s.txn.formToken(), sanitizedMessage(message))
	data.MFAMethod = method
	data.DeliveryUncertain = uncertain
	s.pages.render(w, http.StatusUnauthorized, page, data)
}

// refuse renders the outcome of a refused submission. An expired or exhausted run
// says so, because the user can act on it; everything else is a generic 404.
func (s *Server) refuse(w http.ResponseWriter, cause error) {
	switch {
	case errors.Is(cause, errTransactionExpired):
		s.txn.expire()
		s.pages.render(w, http.StatusGone, pageNotFound, newPageData("", ""))
	case errors.Is(cause, errAttemptsExhausted):
		s.txn.fail(errAttemptsExhausted)
		s.pages.render(w, http.StatusTooManyRequests, pageNotFound,
			newPageData("", sanitizedMessage(msgExhausted)))
	default:
		s.notFound(w)
	}
}

// notFound renders the generic page. It is the same page and the same status for an
// unknown route, a missing cookie, a wrong form token, and an out-of-order
// submission, so a probe learns nothing from the difference.
func (s *Server) notFound(w http.ResponseWriter) {
	s.pages.render(w, http.StatusNotFound, pageNotFound, newPageData("", ""))
}

// parseBoundedForm bounds the body before parsing it and reports whether the caller
// may continue. An oversized body is answered with 413 and never parsed.
func (s *Server) parseBoundedForm(w http.ResponseWriter, r *http.Request) bool {
	switch err := readBoundedForm(w, r); {
	case errors.Is(err, errBodyTooLarge):
		s.pages.render(w, http.StatusRequestEntityTooLarge, pageNotFound, newPageData("", ""))
		return false
	case err != nil:
		s.notFound(w)
		return false
	}
	return true
}

// errBodyTooLarge reports a body over MaxRequestBytes, which is refused before it is
// parsed rather than after.
var errBodyTooLarge = errors.New("loginweb: the request body is too large")

// readBoundedForm bounds the body and parses the form. Both profiles use it, because
// a bound that only one of them applies is a bound one route will be missing.
func readBoundedForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)

	if err := r.ParseForm(); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return errBodyTooLarge
		}
		return errRefused
	}
	return nil
}

// ended reports whether the run reached a terminal state.
func (s *Server) ended() bool {
	select {
	case <-s.txn.done:
		return true
	default:
		return false
	}
}

// hasRunCookie reports whether the request carries this run's capability.
func (s *Server) hasRunCookie(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return s.txn.authorized(strings.TrimSpace(cookie.Value))
}

// setRunCookie installs the per-run capability.
func (s *Server) setRunCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.txn.capability,
		Path:     "/",
		MaxAge:   int(max(s.txn.expires.Sub(s.now()).Seconds(), 1)),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Secure is deliberately unset: this profile is plain HTTP on loopback,
		// and a Secure cookie would simply never be sent.
	})
}

// dropCredentials clears the variables that held credential material as soon as the
// Garmin call returns, so no later code path in this handler can reach them.
//
// It takes pointers because that is what makes the clearing observable: a plain
// assignment to a variable that is never read again is dead code a compiler and a
// linter may both discard. Go promises nothing about erasing the underlying string
// from memory, and this does not claim otherwise.
func dropCredentials(values ...*string) {
	for _, value := range values {
		*value = ""
	}
}

// log records one coarse progress line. The vocabulary is fixed text: there is no
// argument through which a credential, an account, or a capability could travel.
func (s *Server) log(ctx context.Context, message string) {
	if s.logger == nil {
		return
	}
	s.logger.InfoContext(ctx, "login transaction", slog.String("event", message))
}
