package observability

import (
	"strings"
	"testing"

	"github.com/getsentry/sentry-go"
)

func TestScrubEventRedactsRecipientData(t *testing.T) {
	event := &sentry.Event{
		Message: "failed to deliver to +2348012345678 for brand@example.com",
		Exception: []sentry.Exception{
			{Value: "Telegram send failed for 1234567890"},
		},
		Breadcrumbs: []*sentry.Breadcrumb{
			{Message: "GET /api/v1/public/review/abc123secret", Data: map[string]interface{}{
				"recipient": "+447911123456",
			}},
		},
		Request: &sentry.Request{
			URL:         "https://api.example.com/api/v1/public/review/tok_987654321",
			QueryString: "phone=2348012345678&token=secret",
			Data:        `{"message_body":"Secret campaign copy"}`,
			Cookies:     "session=abc",
			Headers: map[string]string{
				"Authorization": "Bearer secret",
				"Content-Type":  "application/json",
			},
		},
		User: sentry.User{Email: "operator@example.com", IPAddress: "203.0.113.7", Username: "fav"},
	}

	got := scrubEvent(event, nil)
	if got == nil {
		t.Fatal("scrubEvent returned nil")
	}
	if strings.Contains(got.Message, "2348012345678") || strings.Contains(got.Message, "brand@example.com") {
		t.Fatalf("message not scrubbed: %q", got.Message)
	}
	if strings.Contains(got.Exception[0].Value, "1234567890") {
		t.Fatalf("exception not scrubbed: %q", got.Exception[0].Value)
	}
	if strings.Contains(got.Breadcrumbs[0].Message, "abc123secret") {
		t.Fatalf("token path not scrubbed: %q", got.Breadcrumbs[0].Message)
	}
	if strings.Contains(got.Breadcrumbs[0].Data["recipient"].(string), "447911123456") {
		t.Fatalf("breadcrumb data not scrubbed: %v", got.Breadcrumbs[0].Data)
	}
	if got.Request.Data != "" || got.Request.Cookies != "" || got.Request.QueryString != "" {
		t.Fatalf("request body/query/cookies should be dropped: %+v", got.Request)
	}
	if _, ok := got.Request.Headers["Authorization"]; ok {
		t.Fatal("Authorization header must be removed")
	}
	if strings.Contains(got.Request.URL, "tok_987654321") {
		t.Fatalf("review token in URL not scrubbed: %q", got.Request.URL)
	}
	if got.User.Email != "" || got.User.IPAddress != "" || got.User.Username != "" {
		t.Fatalf("user PII not cleared: %+v", got.User)
	}
}
