package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type StripeService struct {
	secretKey     string
	webhookSecret string
	httpClient    *http.Client
}

func NewStripeService(secretKey, webhookSecret string) *StripeService {
	return &StripeService{
		secretKey:     secretKey,
		webhookSecret: webhookSecret,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (s *StripeService) CreateCheckoutSession(
	tenantID, userID, customerEmail string,
	plan domain.PlanConfig,
	cycle domain.BillingCycle,
	successURL, cancelURL string,
) (*domain.CheckoutResponse, error) {
	if s.secretKey == "" {
		return nil, errors.New("stripe secret key is not configured")
	}

	amountCents := plan.USDMonthly
	interval := "month"
	if cycle == domain.CycleYearly {
		amountCents = plan.USDYearly
		interval = "year"
	}

	data := url.Values{}
	data.Set("mode", "subscription")
	data.Set("payment_method_types[0]", "card")
	data.Set("customer_email", customerEmail)
	data.Set("success_url", successURL)
	data.Set("cancel_url", cancelURL)

	// Dynamic product data
	data.Set("line_items[0][price_data][currency]", "usd")
	data.Set("line_items[0][price_data][unit_amount]", strconv.Itoa(amountCents))
	data.Set("line_items[0][price_data][recurring][interval]", interval)
	data.Set("line_items[0][price_data][product_data][name]", fmt.Sprintf("MessageRail %s (%s)", plan.Name, strings.Title(string(cycle))))
	data.Set("line_items[0][quantity]", "1")

	// Session metadata
	data.Set("metadata[tenant_id]", tenantID)
	data.Set("metadata[user_id]", userID)
	data.Set("metadata[plan_tier]", string(plan.Tier))
	data.Set("metadata[billing_cycle]", string(cycle))

	// Subscription metadata (persisted on stripe subscription object)
	data.Set("subscription_data[metadata][tenant_id]", tenantID)
	data.Set("subscription_data[metadata][plan_tier]", string(plan.Tier))
	data.Set("subscription_data[metadata][billing_cycle]", string(cycle))

	req, err := http.NewRequest("POST", "https://api.stripe.com/v1/checkout/sessions", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create stripe request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.secretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stripe api request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read stripe response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("stripe checkout creation failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var sessionResp struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(bodyBytes, &sessionResp); err != nil {
		return nil, fmt.Errorf("failed to parse stripe checkout response: %w", err)
	}

	return &domain.CheckoutResponse{
		CheckoutURL: sessionResp.URL,
		Reference:   sessionResp.ID,
		Provider:    string(domain.ProviderStripe),
	}, nil
}

func (s *StripeService) CreateCustomerPortalSession(customerID, returnURL string) (string, error) {
	if s.secretKey == "" {
		return "", errors.New("stripe secret key is not configured")
	}
	if customerID == "" {
		return "", errors.New("customer id is required for customer portal")
	}

	data := url.Values{}
	data.Set("customer", customerID)
	data.Set("return_url", returnURL)

	req, err := http.NewRequest("POST", "https://api.stripe.com/v1/billing_portal/sessions", strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to build portal request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.secretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("stripe portal request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read portal response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("stripe portal creation failed (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var portalResp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(bodyBytes, &portalResp); err != nil {
		return "", fmt.Errorf("failed to parse portal response: %w", err)
	}

	return portalResp.URL, nil
}

// VerifyWebhookSignature verifies Stripe's signature header (t=timestamp,v1=signature)
func (s *StripeService) VerifyWebhookSignature(rawBody []byte, sigHeader string) error {
	if s.webhookSecret == "" {
		return errors.New("stripe webhook secret is not configured")
	}

	var timestampStr string
	var signatures []string

	pairs := strings.Split(sigHeader, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == "t" {
			timestampStr = parts[1]
		} else if parts[0] == "v1" {
			signatures = append(signatures, parts[1])
		}
	}

	if timestampStr == "" || len(signatures) == 0 {
		return errors.New("malformed stripe signature header")
	}

	ts, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return errors.New("invalid stripe timestamp in signature")
	}

	// Verify timestamp is within 5 minutes tolerance
	eventTime := time.Unix(ts, 0)
	if time.Since(eventTime).Abs() > 5*time.Minute {
		return errors.New("stripe webhook timestamp outside tolerance window")
	}

	signedPayload := fmt.Sprintf("%s.%s", timestampStr, string(rawBody))
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write([]byte(signedPayload))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	for _, sig := range signatures {
		if hmac.Equal([]byte(sig), []byte(expectedSignature)) {
			return nil
		}
	}

	return errors.New("stripe webhook signature mismatch")
}
