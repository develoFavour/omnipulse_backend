package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter() (*IPRateLimiter, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	return newIPRateLimiter(clock.now), clock
}

func doRequest(limiter *IPRateLimiter, method, path, ip string) *httptest.ResponseRecorder {
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = ip + ":54321"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestRateLimiterBlocksAfterLimit(t *testing.T) {
	limiter, _ := newTestLimiter()

	for i := 0; i < 10; i++ {
		rec := doRequest(limiter, http.MethodPost, "/api/v1/contacts/import", "203.0.113.7")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	rec := doRequest(limiter, http.MethodPost, "/api/v1/contacts/import", "203.0.113.7")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after limit, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429")
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	limiter, clock := newTestLimiter()

	for i := 0; i < 10; i++ {
		doRequest(limiter, http.MethodPost, "/api/v1/contacts/import", "203.0.113.8")
	}
	if rec := doRequest(limiter, http.MethodPost, "/api/v1/contacts/import", "203.0.113.8"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 before window reset, got %d", rec.Code)
	}

	clock.advance(61 * time.Second)

	if rec := doRequest(limiter, http.MethodPost, "/api/v1/contacts/import", "203.0.113.8"); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after window reset, got %d", rec.Code)
	}
}

func TestRateLimiterSeparatesIpsAndClasses(t *testing.T) {
	limiter, _ := newTestLimiter()

	for i := 0; i < 10; i++ {
		doRequest(limiter, http.MethodPost, "/api/v1/contacts/import", "203.0.113.9")
	}

	if rec := doRequest(limiter, http.MethodPost, "/api/v1/contacts/import", "203.0.113.10"); rec.Code != http.StatusOK {
		t.Fatalf("expected a different IP to have its own budget, got %d", rec.Code)
	}
	if rec := doRequest(limiter, http.MethodGet, "/api/v1/contacts/export", "203.0.113.9"); rec.Code != http.StatusOK {
		t.Fatalf("expected export to have its own budget, got %d", rec.Code)
	}
}

func TestRateLimiterIgnoresWebhooks(t *testing.T) {
	limiter, _ := newTestLimiter()

	for i := 0; i < 200; i++ {
		rec := doRequest(limiter, http.MethodPost, "/api/v1/webhooks/telegram/tenant-1", "203.0.113.11")
		if rec.Code != http.StatusOK {
			t.Fatalf("webhook request %d was limited with %d", i+1, rec.Code)
		}
	}
}

func TestClientIPPrefersLastForwardedEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/reviews/token", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.20")

	if got := clientIP(req); got != "203.0.113.20" {
		t.Fatalf("expected last forwarded address, got %q", got)
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/reviews/token", nil)
	req.RemoteAddr = "198.51.100.5:443"

	if got := clientIP(req); got != "198.51.100.5" {
		t.Fatalf("expected remote address, got %q", got)
	}
}
