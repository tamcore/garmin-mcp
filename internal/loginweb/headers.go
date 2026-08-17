package loginweb

import (
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// contentSecurityPolicy is the page's whole permission set.
//
// It starts from nothing and adds back exactly two things: this origin's own
// stylesheet, and this origin as a form target. There is no script source at all, so
// a script tag added by mistake would not run; there is no connect, image, or font
// source, so nothing can be fetched from a CDN or a tracker; and framing is refused
// outright, which is what stops a click-jacked login form.
const contentSecurityPolicy = "default-src 'none'; " +
	"style-src 'self'; " +
	"form-action 'self'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// redirectOrigin extracts the scheme, host and port from target — never the path,
// query or fragment — reporting false if target does not parse into an absolute
// URL with both. It is used only on a value the caller is about to redirect the
// browser to, which by this point in every call site is the client's own
// already-validated, registered redirect URI: never a request-supplied string.
func redirectOrigin(target string) (string, bool) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return parsed.Scheme + "://" + parsed.Host, true
}

// setOutboundRedirectCSP replaces the response's Content-Security-Policy with one
// that additionally allows target's origin as a form-action destination, on top of
// this origin. It must be called before the handler writes its status line — a
// header set after that point is silently dropped.
//
// target must be the exact value the handler is about to redirect the browser to:
// the client's already-validated, registered redirect URI, or a location built
// from it. It is never read from the request. If target does not carry a usable
// origin, the response keeps the unmodified constant policy rather than guessing:
// a same-origin-only form-action is safe, merely non-functional for that redirect,
// which is the failure this whole fix is closing, not one to reopen by widening
// blindly.
func setOutboundRedirectCSP(w http.ResponseWriter, target string) {
	origin, ok := redirectOrigin(target)
	if !ok {
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; "+
		"style-src 'self'; "+
		"form-action 'self' "+origin+"; "+
		"base-uri 'none'; "+
		"frame-ancestors 'none'")
}

// secureHeaders wraps next for the loopback profile.
//
// It sends no HSTS: the loopback profile is plain HTTP on 127.0.0.1, and a
// Strict-Transport-Security header there would either be ignored or, on a host that
// honoured it, break every other plain-HTTP service on the same name.
func secureHeaders(next http.Handler) http.Handler { return browserHeaders(next, "") }

// remoteSecureHeaders wraps next for the remote profile, adding HSTS. The value is
// rendered once at construction, so no request pays for it.
func remoteSecureHeaders(next http.Handler, hsts string) http.Handler {
	return browserHeaders(next, hsts)
}

// hstsHeader renders the Strict-Transport-Security value for maxAge.
func hstsHeader(maxAge time.Duration) string {
	return "max-age=" + strconv.FormatInt(int64(maxAge.Seconds()), 10) + "; includeSubDomains"
}

// browserHeaders wraps next so every response carries the browser protections,
// including the generic 404, the expired page and the stylesheet.
//
// The headers are set before the handler runs, because a handler that writes its
// status line first would freeze the header map with them missing.
func browserHeaders(next http.Handler, hsts string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		if hsts != "" {
			header.Set("Strict-Transport-Security", hsts)
		}
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		// A credential form and a one-time code page must not sit in a cache, a
		// proxy, or a back-button restore.
		header.Set("Cache-Control", "no-store")
		header.Set("Pragma", "no-cache")

		next.ServeHTTP(w, r)
	})
}
