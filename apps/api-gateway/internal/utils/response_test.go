package utils

import (
	"net/http"
	"testing"
)

func TestUserFacingErrorPreservesUsefulValidationMessage(t *testing.T) {
	got := userFacingError(http.StatusBadRequest, "Campaign title cannot be empty")
	if got != "Campaign title cannot be empty" {
		t.Fatalf("expected validation message to be preserved, got %q", got)
	}
}

func TestUserFacingErrorHidesTechnicalDetails(t *testing.T) {
	got := userFacingError(http.StatusInternalServerError, "failed to load contacts: pq: connection refused")
	want := "Something went wrong on our side. Please try again shortly."
	if got != want {
		t.Fatalf("expected sanitized message %q, got %q", want, got)
	}
}

func TestUserFacingErrorReplacesNumericStatusMessages(t *testing.T) {
	got := userFacingError(http.StatusBadRequest, "402 failed to create checkout")
	want := "We couldn't process that request. Please check the information and try again."
	if got != want {
		t.Fatalf("expected sanitized message %q, got %q", want, got)
	}
}
