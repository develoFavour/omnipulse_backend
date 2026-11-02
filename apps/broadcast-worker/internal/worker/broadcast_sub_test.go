package worker

import "testing"

func TestClassifyDeliveryError(t *testing.T) {
	tests := []struct {
		name       string
		errMsg     string
		httpStatus int
		transient  bool
		wantCode   string
		wantRetry  bool
	}{
		{
			name:       "permanent 400 is not retryable",
			errMsg:     "WhatsApp API rejected message (status 400)",
			httpStatus: 400,
			wantCode:   "provider_error",
			wantRetry:  false,
		},
		{
			name:       "forbidden 403 is not retryable",
			errMsg:     "Telegram API returned status 403",
			httpStatus: 403,
			wantCode:   "provider_error",
			wantRetry:  false,
		},
		{
			name:       "rate limited is retryable",
			errMsg:     "WhatsApp API rejected message (status 429)",
			httpStatus: 429,
			wantCode:   "rate_limited",
			wantRetry:  true,
		},
		{
			name:       "rate limit body without status code",
			errMsg:     "too many requests, slow down",
			httpStatus: 0,
			wantCode:   "rate_limited",
			wantRetry:  true,
		},
		{
			name:       "server error is retryable",
			errMsg:     "WhatsApp API rejected message (status 502)",
			httpStatus: 502,
			wantCode:   "provider_error",
			wantRetry:  true,
		},
		{
			name:      "network failure is retryable",
			errMsg:    "network error calling WhatsApp Cloud API: dial tcp: timeout",
			transient: true,
			wantCode:  "provider_error",
			wantRetry: true,
		},
		{
			name:       "invalid recipient is terminal",
			errMsg:     "recipient is not a valid WhatsApp number",
			httpStatus: 400,
			wantCode:   "invalid_number",
			wantRetry:  false,
		},
		{
			name:      "expired window is terminal",
			errMsg:    "message expired: dispatch window closed at 2026-01-01T00:00:00Z",
			wantCode:  "window_expired",
			wantRetry: false,
		},
		{
			name:      "compliance block is terminal",
			errMsg:    "recipient opted out",
			wantCode:  "blocked",
			wantRetry: false,
		},
		{
			name:      "unsupported platform is terminal",
			errMsg:    "unsupported platform 'x' — no delivery adapter configured",
			wantCode:  "unsupported",
			wantRetry: false,
		},
		{
			name:      "unknown local failure does not default to retryable",
			errMsg:    "failed to parse telegram token credentials",
			wantCode:  "provider_error",
			wantRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, retryable := classifyDeliveryError(tt.errMsg, tt.httpStatus, tt.transient)
			if code != tt.wantCode {
				t.Errorf("failure code: got %q, want %q", code, tt.wantCode)
			}
			if retryable != tt.wantRetry {
				t.Errorf("retryable: got %v, want %v", retryable, tt.wantRetry)
			}
		})
	}
}
