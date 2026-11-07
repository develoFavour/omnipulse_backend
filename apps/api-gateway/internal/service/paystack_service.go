package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type PaystackService struct {
	secretKey  string
	publicKey  string
	httpClient *http.Client
}

func NewPaystackService(secretKey, publicKey string) *PaystackService {
	return &PaystackService{
		secretKey:  secretKey,
		publicKey:  publicKey,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

type PaystackInitializeRequest struct {
	Email       string                 `json:"email"`
	Amount      int                    `json:"amount"` // in kobo
	Currency    string                 `json:"currency"`
	CallbackURL string                 `json:"callback_url"`
	Metadata    map[string]interface{} `json:"metadata"`
	Channels    []string               `json:"channels"`
}

type PaystackInitializeResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		AuthorizationURL string `json:"authorization_url"`
		AccessCode       string `json:"access_code"`
		Reference        string `json:"reference"`
	} `json:"data"`
}

func (p *PaystackService) InitializeTransaction(
	tenantID, userID, customerEmail string,
	plan domain.PlanConfig,
	cycle domain.BillingCycle,
	callbackURL string,
) (*domain.CheckoutResponse, error) {
	if p.secretKey == "" {
		return nil, errors.New("paystack secret key is not configured")
	}

	amountKobo := plan.NGNMonthly
	if cycle == domain.CycleYearly {
		amountKobo = plan.NGNYearly
	}

	reqBody := PaystackInitializeRequest{
		Email:       customerEmail,
		Amount:      amountKobo,
		Currency:    "NGN",
		CallbackURL: callbackURL,
		Metadata: map[string]interface{}{
			"tenant_id":     tenantID,
			"user_id":       userID,
			"plan_tier":     string(plan.Tier),
			"billing_cycle": string(cycle),
			"custom_fields": []map[string]string{
				{
					"display_name":  "Workspace Tier",
					"variable_name": "plan_tier",
					"value":         plan.Name,
				},
				{
					"display_name":  "Billing Frequency",
					"variable_name": "billing_cycle",
					"value":         string(cycle),
				},
			},
		},
		Channels: []string{"card", "bank", "ussd", "qr", "bank_transfer"},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to encode paystack request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.paystack.co/transaction/initialize", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create paystack request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paystack api request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read paystack response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("paystack transaction initialization failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var initResp PaystackInitializeResponse
	if err := json.Unmarshal(bodyBytes, &initResp); err != nil {
		return nil, fmt.Errorf("failed to parse paystack response: %w", err)
	}

	if !initResp.Status {
		return nil, fmt.Errorf("paystack error: %s", initResp.Message)
	}

	return &domain.CheckoutResponse{
		CheckoutURL: initResp.Data.AuthorizationURL,
		Reference:   initResp.Data.Reference,
		Provider:    string(domain.ProviderPaystack),
	}, nil
}

// InitializePaymentLink generates a conversational commerce Paystack checkout link
func (p *PaystackService) InitializePaymentLink(
	tenantID, contactID, conversationID string,
	campaignID *string,
	customerEmail, customerPhone string,
	amountKobo int64,
	description string,
	callbackURL string,
) (*domain.CheckoutResponse, error) {
	if p.secretKey == "" {
		return nil, errors.New("paystack secret key is not configured")
	}

	email := strings.TrimSpace(customerEmail)
	if email == "" || !strings.Contains(email, "@") || !strings.Contains(email, ".") {
		cleanedPhone := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, customerPhone)
		if cleanedPhone == "" {
			cleanedPhone = strings.ReplaceAll(contactID, "-", "")
		}
		email = fmt.Sprintf("customer.%s@messagerail.com", cleanedPhone)
	}

	metadata := map[string]interface{}{
		"intent":          "payment_link",
		"tenant_id":       tenantID,
		"contact_id":      contactID,
		"conversation_id": conversationID,
		"description":     description,
		"custom_fields": []map[string]string{
			{
				"display_name":  "Item / Description",
				"variable_name": "description",
				"value":         description,
			},
		},
	}
	if campaignID != nil && *campaignID != "" {
		metadata["campaign_id"] = *campaignID
	}

	reqBody := PaystackInitializeRequest{
		Email:       email,
		Amount:      int(amountKobo),
		Currency:    "NGN",
		CallbackURL: callbackURL,
		Metadata:    metadata,
		Channels:    []string{"card", "bank", "ussd", "qr", "bank_transfer"},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to encode paystack request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.paystack.co/transaction/initialize", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create paystack request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paystack api request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read paystack response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("paystack payment link initialization failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var initResp PaystackInitializeResponse
	if err := json.Unmarshal(bodyBytes, &initResp); err != nil {
		return nil, fmt.Errorf("failed to parse paystack response: %w", err)
	}

	if !initResp.Status {
		return nil, fmt.Errorf("paystack error: %s", initResp.Message)
	}

	return &domain.CheckoutResponse{
		CheckoutURL: initResp.Data.AuthorizationURL,
		Reference:   initResp.Data.Reference,
		Provider:    string(domain.ProviderPaystack),
	}, nil
}

// VerifyWebhookSignature computes HMAC-SHA512 of raw body with Paystack secret key
func (p *PaystackService) VerifyWebhookSignature(rawBody []byte, sigHeader string) error {
	if p.secretKey == "" {
		return errors.New("paystack secret key is not configured")
	}

	if sigHeader == "" {
		return errors.New("missing paystack signature header")
	}

	mac := hmac.New(sha512.New, []byte(p.secretKey))
	mac.Write(rawBody)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sigHeader), []byte(expectedSignature)) {
		return errors.New("paystack webhook signature mismatch")
	}

	return nil
}
