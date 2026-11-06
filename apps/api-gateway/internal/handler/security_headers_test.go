package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersSetOnEveryResponse(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	sut := SecurityHeaders(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/contacts", nil)
	rec := httptest.NewRecorder()
	sut.ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", res.StatusCode)
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"X-XSS-Protection":       "0",
	} {
		if got := res.Header.Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	if got := res.Header.Get("Permissions-Policy"); got == "" {
		t.Fatal("Permissions-Policy should be set")
	}
	if got := res.Header.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("plain HTTP must not send HSTS, got %q", got)
	}
}

func TestSecurityHeadersHSTSOnlyOverTLS(t *testing.T) {
	sut := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	sut.ServeHTTP(rec, req)

	if got := rec.Result().Header.Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Fatalf("HSTS = %q over forwarded HTTPS", got)
	}
}
