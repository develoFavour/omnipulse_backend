package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlatformAdminGateAllowsOnlyListedIdentities(t *testing.T) {
	gate := NewPlatformAdminGate([]string{" user_owner ", "user_ops", ""})

	cases := []struct {
		userID string
		want   bool
	}{
		{"user_owner", true},
		{"user_ops", true},
		{"user_other", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := gate.Allows(tc.userID); got != tc.want {
			t.Errorf("Allows(%q) = %v, want %v", tc.userID, got, tc.want)
		}
	}

	if gate.Configured() != 2 {
		t.Errorf("Configured() = %d, want 2", gate.Configured())
	}
}

func TestPlatformAdminGateRejectsUnknownAndMissingIdentity(t *testing.T) {
	gate := NewPlatformAdminGate([]string{"user_owner"})

	reached := false
	protected := gate.RequirePlatformAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	for _, tc := range []struct {
		name   string
		userID any
	}{
		{"missing identity", nil},
		{"unlisted identity", "user_stranger"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview", nil)
			if tc.userID != nil {
				req = req.WithContext(context.WithValue(req.Context(), UserIDKey, tc.userID))
			}
			rec := httptest.NewRecorder()

			protected.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
			if reached {
				t.Error("protected handler ran for a non-admin caller")
			}
		})
	}
}

func TestPlatformAdminGateAdmitsAllowlistedIdentity(t *testing.T) {
	gate := NewPlatformAdminGate([]string{"user_owner"})

	reached := false
	protected := gate.RequirePlatformAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview?days=30", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserIDKey, "user_owner"))
	rec := httptest.NewRecorder()

	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !reached {
		t.Error("protected handler did not run for an allowlisted caller")
	}
}
