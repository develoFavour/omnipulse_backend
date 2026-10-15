package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyMetaSignature(t *testing.T) {
	secret := "test-meta-app-secret-12345"
	payload := []byte(`{"object":"whatsapp_business_account","entry":[]}`)

	// Generate valid signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// 1. Valid signature passes
	if !verifyMetaSignature(payload, validSig, secret) {
		t.Fatalf("Expected valid signature to verify successfully")
	}

	// 2. Tampered signature fails
	tamperedSig := "sha256=abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	if verifyMetaSignature(payload, tamperedSig, secret) {
		t.Fatalf("Expected tampered signature to fail verification")
	}

	// 3. Tampered payload fails
	tamperedPayload := []byte(`{"object":"whatsapp_business_account","entry":[{"tampered":true}]}`)
	if verifyMetaSignature(tamperedPayload, validSig, secret) {
		t.Fatalf("Expected tampered payload to fail verification")
	}

	// 4. Malformed header fails
	if verifyMetaSignature(payload, "invalid-header-format", secret) {
		t.Fatalf("Expected malformed signature header to fail verification")
	}

	// 5. Dev mode (empty secret) passes with fallback
	if !verifyMetaSignature(payload, "", "") {
		t.Fatalf("Expected empty secret dev fallback to return true")
	}
}

func TestTelegramWebhookSecretEnforcement(t *testing.T) {
	wh := &WebhookHandler{
		telegramWebhookSecret: "super-secret-tg-token",
	}

	// Request with missing secret should be rejected (403)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/telegram/test-tenant", nil)
	req.SetPathValue("tenant_id", "test-tenant")
	rr := httptest.NewRecorder()

	wh.HandleTelegram(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for missing secret, got %d", rr.Code)
	}

	// Request with wrong secret should be rejected (403)
	reqWrong := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/telegram/test-tenant", nil)
	reqWrong.SetPathValue("tenant_id", "test-tenant")
	reqWrong.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong-token")
	rrWrong := httptest.NewRecorder()

	wh.HandleTelegram(rrWrong, reqWrong)
	if rrWrong.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden for wrong secret, got %d", rrWrong.Code)
	}
}
