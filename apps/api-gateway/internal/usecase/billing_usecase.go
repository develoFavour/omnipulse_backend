package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/service"
)

type BillingUseCase struct {
	subRepo     domain.SubscriptionRepository
	stripeSvc   *service.StripeService
	paystackSvc *service.PaystackService
	appBaseURL  string
}

func NewBillingUseCase(
	subRepo domain.SubscriptionRepository,
	stripeSvc *service.StripeService,
	paystackSvc *service.PaystackService,
	appBaseURL string,
) *BillingUseCase {
	return &BillingUseCase{
		subRepo:     subRepo,
		stripeSvc:   stripeSvc,
		paystackSvc: paystackSvc,
		appBaseURL:  appBaseURL,
	}
}

func (u *BillingUseCase) GetSubscription(ctx context.Context, tenantID string) (*domain.Subscription, error) {
	sub, err := u.subRepo.GetByTenantID(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve subscription: %w", err)
	}
	return sub, nil
}

func (u *BillingUseCase) CreateCheckout(
	ctx context.Context,
	tenantID, userID, userEmail string,
	req domain.CheckoutRequest,
) (*domain.CheckoutResponse, error) {
	plan, exists := domain.AvailablePlans[req.PlanTier]
	if !exists {
		return nil, errors.New("invalid plan tier requested")
	}

	if req.PlanTier == domain.PlanStarter {
		return nil, errors.New("starter plan is free and does not require checkout")
	}

	if req.BillingCycle == "" {
		req.BillingCycle = domain.CycleMonthly
	}

	successURL := req.SuccessURL
	if successURL == "" {
		successURL = fmt.Sprintf("%s/settings?billing_status=success", strings.TrimRight(u.appBaseURL, "/"))
	}
	cancelURL := req.CancelURL
	if cancelURL == "" {
		cancelURL = fmt.Sprintf("%s/settings?billing_status=canceled", strings.TrimRight(u.appBaseURL, "/"))
	}

	currency := strings.ToUpper(strings.TrimSpace(req.Currency))

	// Nigerian Naira (NGN) -> Paystack
	if currency == "NGN" {
		return u.paystackSvc.InitializeTransaction(
			tenantID, userID, userEmail,
			plan, req.BillingCycle,
			successURL,
		)
	}

	// Default: USD -> Stripe
	return u.stripeSvc.CreateCheckoutSession(
		tenantID, userID, userEmail,
		plan, req.BillingCycle,
		successURL, cancelURL,
	)
}

func (u *BillingUseCase) CreateCustomerPortal(ctx context.Context, tenantID string) (string, error) {
	sub, err := u.subRepo.GetByTenantID(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("failed to load subscription: %w", err)
	}

	if sub.Provider != domain.ProviderStripe || sub.CustomerID == "" {
		return "", errors.New("customer portal is only available for active Stripe subscriptions")
	}

	returnURL := fmt.Sprintf("%s/settings", strings.TrimRight(u.appBaseURL, "/"))
	return u.stripeSvc.CreateCustomerPortalSession(sub.CustomerID, returnURL)
}

// HandleStripeWebhook processes verified Stripe webhook events
func (u *BillingUseCase) HandleStripeWebhook(ctx context.Context, rawBody []byte, sigHeader string) error {
	if err := u.stripeSvc.VerifyWebhookSignature(rawBody, sigHeader); err != nil {
		return fmt.Errorf("stripe webhook signature verification failed: %w", err)
	}

	var event struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &event); err != nil {
		return fmt.Errorf("failed to parse stripe event: %w", err)
	}

	switch event.Type {
	case "checkout.session.completed":
		return u.handleStripeCheckoutCompleted(ctx, event.Data)
	case "customer.subscription.updated":
		return u.handleStripeSubscriptionUpdated(ctx, event.Data)
	case "customer.subscription.deleted":
		return u.handleStripeSubscriptionDeleted(ctx, event.Data)
	default:
		log.Printf("[STRIPE-WEBHOOK] Ignored unhandled event type: %s\n", event.Type)
		return nil
	}
}

func (u *BillingUseCase) handleStripeCheckoutCompleted(ctx context.Context, dataRaw json.RawMessage) error {
	var payload struct {
		Object struct {
			ID           string            `json:"id"`
			Customer     string            `json:"customer"`
			Subscription string            `json:"subscription"`
			AmountTotal  int               `json:"amount_total"`
			Currency     string            `json:"currency"`
			Metadata     map[string]string `json:"metadata"`
		} `json:"object"`
	}

	if err := json.Unmarshal(dataRaw, &payload); err != nil {
		return fmt.Errorf("failed to parse stripe checkout session object: %w", err)
	}

	session := payload.Object
	tenantID := session.Metadata["tenant_id"]
	planTier := domain.PlanTier(session.Metadata["plan_tier"])
	cycle := domain.BillingCycle(session.Metadata["billing_cycle"])

	if tenantID == "" || planTier == "" {
		log.Printf("[STRIPE-WEBHOOK] checkout.session.completed missing tenant_id or plan_tier metadata: %s\n", session.ID)
		return nil
	}

	planConfig, exists := domain.AvailablePlans[planTier]
	if !exists {
		planConfig = domain.AvailablePlans[domain.PlanGrowth]
		planTier = domain.PlanGrowth
	}

	if cycle == "" {
		cycle = domain.CycleMonthly
	}

	periodEnd := time.Now().AddDate(0, 1, 0)
	if cycle == domain.CycleYearly {
		periodEnd = time.Now().AddDate(1, 0, 0)
	}

	sub := &domain.Subscription{
		TenantID:            tenantID,
		Provider:            domain.ProviderStripe,
		CustomerID:          session.Customer,
		SubscriptionID:      session.Subscription,
		PlanTier:            planTier,
		BillingCycle:        cycle,
		Currency:            strings.ToUpper(session.Currency),
		Amount:              session.AmountTotal,
		Status:              "active",
		CurrentPeriodStart:  time.Now(),
		CurrentPeriodEnd:    &periodEnd,
		MonthlyMessageLimit: planConfig.MessageLimit,
		ContactsLimit:       planConfig.ContactLimit,
		ChannelsLimit:       planConfig.ChannelLimit,
	}

	if err := u.subRepo.Upsert(ctx, sub); err != nil {
		return fmt.Errorf("failed to save stripe subscription: %w", err)
	}

	log.Printf("[STRIPE-WEBHOOK] Successfully activated %s plan for tenant %s (sub: %s)\n", planTier, tenantID, session.Subscription)
	return nil
}

func (u *BillingUseCase) handleStripeSubscriptionUpdated(ctx context.Context, dataRaw json.RawMessage) error {
	var payload struct {
		Object struct {
			ID               string            `json:"id"`
			Customer         string            `json:"customer"`
			Status           string            `json:"status"`
			CurrentPeriodEnd int64             `json:"current_period_end"`
			Metadata         map[string]string `json:"metadata"`
		} `json:"object"`
	}

	if err := json.Unmarshal(dataRaw, &payload); err != nil {
		return fmt.Errorf("failed to parse stripe subscription object: %w", err)
	}

	subObj := payload.Object
	sub, err := u.subRepo.GetBySubscriptionID(ctx, string(domain.ProviderStripe), subObj.ID)
	if err != nil || sub == nil {
		// Look up by customer ID fallback
		sub, err = u.subRepo.GetByCustomerID(ctx, string(domain.ProviderStripe), subObj.Customer)
	}

	if sub == nil {
		log.Printf("[STRIPE-WEBHOOK] subscription.updated could not match local record for id: %s\n", subObj.ID)
		return nil
	}

	sub.Status = subObj.Status
	if subObj.CurrentPeriodEnd > 0 {
		end := time.Unix(subObj.CurrentPeriodEnd, 0)
		sub.CurrentPeriodEnd = &end
	}

	return u.subRepo.Upsert(ctx, sub)
}

func (u *BillingUseCase) handleStripeSubscriptionDeleted(ctx context.Context, dataRaw json.RawMessage) error {
	var payload struct {
		Object struct {
			ID       string `json:"id"`
			Customer string `json:"customer"`
		} `json:"object"`
	}

	if err := json.Unmarshal(dataRaw, &payload); err != nil {
		return fmt.Errorf("failed to parse stripe subscription deleted object: %w", err)
	}

	subObj := payload.Object
	sub, err := u.subRepo.GetBySubscriptionID(ctx, string(domain.ProviderStripe), subObj.ID)
	if err != nil || sub == nil {
		sub, err = u.subRepo.GetByCustomerID(ctx, string(domain.ProviderStripe), subObj.Customer)
	}

	if sub == nil {
		log.Printf("[STRIPE-WEBHOOK] subscription.deleted no matching record for: %s\n", subObj.ID)
		return nil
	}

	// Downgrade back to Free Starter
	starter := domain.AvailablePlans[domain.PlanStarter]
	sub.PlanTier = domain.PlanStarter
	sub.Status = "canceled"
	sub.MonthlyMessageLimit = starter.MessageLimit
	sub.ContactsLimit = starter.ContactLimit
	sub.ChannelsLimit = starter.ChannelLimit

	return u.subRepo.Upsert(ctx, sub)
}

// HandlePaystackWebhook processes verified Paystack webhook events
func (u *BillingUseCase) HandlePaystackWebhook(ctx context.Context, rawBody []byte, sigHeader string) error {
	if err := u.paystackSvc.VerifyWebhookSignature(rawBody, sigHeader); err != nil {
		return fmt.Errorf("paystack webhook signature verification failed: %w", err)
	}

	var event struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &event); err != nil {
		return fmt.Errorf("failed to parse paystack event: %w", err)
	}

	switch event.Event {
	case "charge.success":
		return u.handlePaystackChargeSuccess(ctx, event.Data)
	case "subscription.disable":
		return u.handlePaystackSubscriptionDisabled(ctx, event.Data)
	default:
		log.Printf("[PAYSTACK-WEBHOOK] Ignored unhandled event: %s\n", event.Event)
		return nil
	}
}

func (u *BillingUseCase) handlePaystackChargeSuccess(ctx context.Context, dataRaw json.RawMessage) error {
	var payload struct {
		Reference string `json:"reference"`
		Amount    int    `json:"amount"` // in kobo
		Currency  string `json:"currency"`
		Status    string `json:"status"`
		Customer  struct {
			CustomerCode string `json:"customer_code"`
			Email        string `json:"email"`
		} `json:"customer"`
		Metadata struct {
			TenantID     string `json:"tenant_id"`
			PlanTier     string `json:"plan_tier"`
			BillingCycle string `json:"billing_cycle"`
		} `json:"metadata"`
	}

	if err := json.Unmarshal(dataRaw, &payload); err != nil {
		return fmt.Errorf("failed to parse paystack charge.success payload: %w", err)
	}

	if payload.Status != "success" {
		return nil
	}

	tenantID := payload.Metadata.TenantID
	if tenantID == "" {
		log.Printf("[PAYSTACK-WEBHOOK] Missing tenant_id in charge.success metadata (ref: %s)\n", payload.Reference)
		return nil
	}

	tier := domain.PlanTier(payload.Metadata.PlanTier)
	planConfig, exists := domain.AvailablePlans[tier]
	if !exists {
		planConfig = domain.AvailablePlans[domain.PlanGrowth]
		tier = domain.PlanGrowth
	}

	cycle := domain.BillingCycle(payload.Metadata.BillingCycle)
	if cycle == "" {
		cycle = domain.CycleMonthly
	}

	periodEnd := time.Now().AddDate(0, 1, 0)
	if cycle == domain.CycleYearly {
		periodEnd = time.Now().AddDate(1, 0, 0)
	}

	sub := &domain.Subscription{
		TenantID:            tenantID,
		Provider:            domain.ProviderPaystack,
		CustomerID:          payload.Customer.CustomerCode,
		SubscriptionID:      payload.Reference,
		PlanTier:            tier,
		BillingCycle:        cycle,
		Currency:            "NGN",
		Amount:              payload.Amount,
		Status:              "active",
		CurrentPeriodStart:  time.Now(),
		CurrentPeriodEnd:    &periodEnd,
		MonthlyMessageLimit: planConfig.MessageLimit,
		ContactsLimit:       planConfig.ContactLimit,
		ChannelsLimit:       planConfig.ChannelLimit,
	}

	if err := u.subRepo.Upsert(ctx, sub); err != nil {
		return fmt.Errorf("failed to save paystack subscription: %w", err)
	}

	log.Printf("[PAYSTACK-WEBHOOK] Successfully activated %s plan for tenant %s via Paystack (ref: %s)\n", tier, tenantID, payload.Reference)
	return nil
}

func (u *BillingUseCase) handlePaystackSubscriptionDisabled(ctx context.Context, dataRaw json.RawMessage) error {
	var payload struct {
		SubscriptionCode string `json:"subscription_code"`
		Customer         struct {
			CustomerCode string `json:"customer_code"`
		} `json:"customer"`
	}

	if err := json.Unmarshal(dataRaw, &payload); err != nil {
		return fmt.Errorf("failed to parse paystack subscription.disable payload: %w", err)
	}

	sub, err := u.subRepo.GetByCustomerID(ctx, string(domain.ProviderPaystack), payload.Customer.CustomerCode)
	if err != nil || sub == nil {
		log.Printf("[PAYSTACK-WEBHOOK] subscription.disable no matching record for: %s\n", payload.Customer.CustomerCode)
		return nil
	}

	starter := domain.AvailablePlans[domain.PlanStarter]
	sub.PlanTier = domain.PlanStarter
	sub.Status = "canceled"
	sub.MonthlyMessageLimit = starter.MessageLimit
	sub.ContactsLimit = starter.ContactLimit
	sub.ChannelsLimit = starter.ChannelLimit

	return u.subRepo.Upsert(ctx, sub)
}
