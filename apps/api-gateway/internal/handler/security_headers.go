package handler

import "net/http"

// SecurityHeaders sets baseline hardening headers on every response. CSP is
// deliberately left out: the operator-facing surface loads Clerk, Sentry, and
// channel-provider scripts from third-party origins, and a half-right CSP
// breaks sign-in without adding real protection.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// Modern browsers ship XSS auditors that do more harm than good; 0 disables them.
		h.Set("X-XSS-Protection", "0")
		// Behind a TLS-terminating proxy the request arrives as plain HTTP, so
		// HSTS keys off the forwarded proto instead of r.TLS.
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
