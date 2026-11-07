package handler

import (
	"encoding/json"
	"net/http"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"
)

type CommerceHandler struct {
	commerceUC  usecase.CommerceUseCase
	paymentRepo domain.PaymentRepository
}

func NewCommerceHandler(commerceUC usecase.CommerceUseCase, paymentRepo domain.PaymentRepository) *CommerceHandler {
	return &CommerceHandler{
		commerceUC:  commerceUC,
		paymentRepo: paymentRepo,
	}
}

// CreatePaymentLink handles POST /api/v1/conversations/{id}/payment-link
func (h *CommerceHandler) CreatePaymentLink(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	userID, _ := r.Context().Value(UserIDKey).(string)

	conversationID := r.PathValue("id")
	if conversationID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation ID")
		return
	}

	var req domain.CreatePaymentLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	tx, err := h.commerceUC.CreatePaymentLink(r.Context(), tenantID, userID, conversationID, req)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, tx)
}

// ListConversationPaymentLinks handles GET /api/v1/conversations/{id}/payment-links
func (h *CommerceHandler) ListConversationPaymentLinks(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	conversationID := r.PathValue("id")
	if conversationID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation ID")
		return
	}

	links, err := h.commerceUC.ListPaymentLinks(r.Context(), tenantID, conversationID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if links == nil {
		links = []domain.PaymentTransaction{}
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"payment_links": links,
	})
}

// ListContactPaymentLinks handles GET /api/v1/contacts/{id}/payment-links
func (h *CommerceHandler) ListContactPaymentLinks(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	contactID := r.PathValue("id")
	if contactID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing contact ID")
		return
	}

	links, err := h.paymentRepo.ListByContact(r.Context(), tenantID, contactID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if links == nil {
		links = []domain.PaymentTransaction{}
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"payment_links": links,
	})
}
