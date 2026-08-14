package loginweb

import (
	"net/http"
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
