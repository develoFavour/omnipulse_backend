package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"
)

type BillingHandler struct {
	billingUseCase *usecase.BillingUseCase
}

func NewBillingHandler(billingUseCase *usecase.BillingUseCase) *BillingHandler {
	return &BillingHandler{
		billingUseCase: billingUseCase,
	}
}

// GetSubscription returns current subscription status and plan configuration
func (h *BillingHandler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	sub, err := h.billingUseCase.GetSubscription(r.Context(), tenantID)
	if err != nil {
		log.Printf("[BillingHandler] Failed to get subscription for tenant %s: %v\n", tenantID, err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load subscription")
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"subscription": sub,
		"plans":        domain.AvailablePlans,
	})
}

// CreateCheckout initiates a checkout session with Stripe or Paystack
func (h *BillingHandler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	userID, _ := r.Context().Value(UserIDKey).(string)
	userEmail := r.Header.Get("X-User-Email")

	var req domain.CheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	resp, err := h.billingUseCase.CreateCheckout(r.Context(), tenantID, userID, userEmail, req)
	if err != nil {
		log.Printf("[BillingHandler] Checkout creation error for tenant %s: %v\n", tenantID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, resp)
}

// CreateCustomerPortal generates a billing portal URL (Stripe only)
func (h *BillingHandler) CreateCustomerPortal(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	portalURL, err := h.billingUseCase.CreateCustomerPortal(r.Context(), tenantID)
	if err != nil {
		log.Printf("[BillingHandler] Portal error for tenant %s: %v\n", tenantID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"portal_url": portalURL,
	})
}

// HandleStripeWebhook processes Stripe incoming webhook events
func (h *BillingHandler) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	sigHeader := r.Header.Get("Stripe-Signature")
	if sigHeader == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing Stripe-Signature header")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	if err := h.billingUseCase.HandleStripeWebhook(r.Context(), body, sigHeader); err != nil {
		log.Printf("[BillingHandler] Stripe webhook handling failed: %v\n", err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]bool{"received": true})
}

// HandlePaystackWebhook processes Paystack incoming webhook events
func (h *BillingHandler) HandlePaystackWebhook(w http.ResponseWriter, r *http.Request) {
	sigHeader := r.Header.Get("x-paystack-signature")
	if sigHeader == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing x-paystack-signature header")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	if err := h.billingUseCase.HandlePaystackWebhook(r.Context(), body, sigHeader); err != nil {
		log.Printf("[BillingHandler] Paystack webhook handling failed: %v\n", err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]bool{"received": true})
}
