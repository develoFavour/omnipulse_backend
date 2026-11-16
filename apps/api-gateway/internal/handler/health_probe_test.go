package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddlewareBypassesHealthProbes(t *testing.T) {
	sut := AuthMiddleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/health", "/health/ready", "/health/workers"} {
		rec := httptest.NewRecorder()
		sut.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d, want 200 without credentials", path, rec.Code)
		}
	}
}
