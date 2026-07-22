package handler

import (
	"encoding/json"
	"net/http"
	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"

	"github.com/clerk/clerk-sdk-go/v2/jwt"
)

type IdentityHandler struct {
	useCase *usecase.IdentityUseCase
}

func NewIdentityHandler(useCase *usecase.IdentityUseCase) *IdentityHandler {
	return &IdentityHandler{useCase: useCase}
}

// SyncUser handles: POST /api/v1/auth/sync
func (h *IdentityHandler) SyncUser(w http.ResponseWriter, r *http.Request) {
	// 1. Get raw token
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || len(authHeader) < 8 {
		utils.WriteError(w, http.StatusUnauthorized, "Missing authorization token")
		return
	}
	token := authHeader[7:]

	// 2. Verify with Clerk
	claims, err := jwt.Verify(r.Context(), &jwt.VerifyParams{
		Token: token,
	})
	if err != nil {
		utils.WriteError(w, http.StatusUnauthorized, "Invalid token")
		return
	}

	// 3. JIT Sync
	syncRes, err := h.useCase.SyncUser(r.Context(), claims.Subject, "clerk-sync@omnipulse.dev")
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to sync user")
		return
	}

	utils.WriteJSON(w, http.StatusOK, syncRes)
}

type updateBrandReq struct {
	CompanyName string `json:"company_name"`
}

// UpdateBrand handles: PATCH /api/v1/onboarding/brand
func (h *IdentityHandler) UpdateBrand(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req updateBrandReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.useCase.UpdateBrandName(r.Context(), tenantID, req.CompanyName); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Brand updated"})
}

// CompleteOnboarding handles: POST /api/v1/onboarding/complete
func (h *IdentityHandler) CompleteOnboarding(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	if err := h.useCase.CompleteOnboarding(r.Context(), tenantID); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Onboarding completed"})
}
