package handler

import (
	"encoding/json"
	"log"
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
		log.Printf("[IdentityHandler] SyncUser REJECTED: missing or malformed Authorization header")
		utils.WriteError(w, http.StatusUnauthorized, "Missing authorization token")
		return
	}
	token := authHeader[7:]

	// 2. Verify with Clerk
	claims, err := jwt.Verify(r.Context(), &jwt.VerifyParams{
		Token: token,
	})
	if err != nil {
		log.Printf("[IdentityHandler] SyncUser REJECTED: JWT verification failed: %v", err)
		utils.WriteError(w, http.StatusUnauthorized, "Invalid token")
		return
	}

	// 3. Extract real user email from the X-User-Email header (injected by ClerkAuthProvider on the frontend)
	// Fall back to empty string so SyncUser usecase will resolve it via Clerk API if needed
	userEmail := r.Header.Get("X-User-Email")
	clerkUserID := claims.Subject
	log.Printf("[IdentityHandler] SyncUser: resolving identity for clerk_user_id=%s email=%q", clerkUserID, userEmail)

	// 4. JIT Sync — resolves tenant, user record, and workspace role
	syncRes, err := h.useCase.SyncUser(r.Context(), clerkUserID, userEmail)
	if err != nil {
		log.Printf("[IdentityHandler] SyncUser FAILED for clerk_user_id=%s: %v", clerkUserID, err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to sync user")
		return
	}

	log.Printf("[IdentityHandler] SyncUser SUCCESS: clerk_user_id=%s role=%s tenant=%s",
		clerkUserID, syncRes.User.Role, syncRes.Tenant.ID)
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

	callerRole, _ := r.Context().Value(UserRoleKey).(string)
	if callerRole != "owner" {
		utils.WriteError(w, http.StatusForbidden, "Only workspace owners can rename the workspace")
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

// ListWorkspaces handles: GET /api/v1/workspaces
func (h *IdentityHandler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		log.Printf("[IdentityHandler] ListWorkspaces REJECTED: missing authenticated user context")
		utils.WriteError(w, http.StatusUnauthorized, "Missing user context")
		return
	}

	workspaces, err := h.useCase.ListWorkspaces(r.Context(), userID)
	if err != nil {
		log.Printf("[IdentityHandler] ListWorkspaces FAILED for user=%s: %v", userID, err)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, workspaces)
}

type switchWorkspaceReq struct {
	TenantID string `json:"tenant_id"`
}

// SwitchWorkspace handles: POST /api/v1/workspaces/switch
func (h *IdentityHandler) SwitchWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		log.Printf("[IdentityHandler] SwitchWorkspace REJECTED: missing authenticated user context")
		utils.WriteError(w, http.StatusUnauthorized, "Missing user context")
		return
	}

	var req switchWorkspaceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TenantID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body: tenant_id is required")
		return
	}

	tenant, role, err := h.useCase.SwitchWorkspace(r.Context(), userID, req.TenantID)
	if err != nil {
		log.Printf("[IdentityHandler] SwitchWorkspace FAILED user=%s target=%s: %v", userID, req.TenantID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[IdentityHandler] SwitchWorkspace SUCCESS: user=%s switched to tenant=%s (%s) role=%s", userID, tenant.ID, tenant.CompanyName, role)
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Switched workspace successfully",
		"tenant":  tenant,
		"role":    role,
	})
}

type createWorkspaceReq struct {
	CompanyName string `json:"company_name"`
}

// CreateWorkspace handles: POST /api/v1/workspaces
func (h *IdentityHandler) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		log.Printf("[IdentityHandler] CreateWorkspace REJECTED: missing authenticated user context")
		utils.WriteError(w, http.StatusUnauthorized, "Missing user context")
		return
	}

	var req createWorkspaceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	tenant, err := h.useCase.CreateWorkspace(r.Context(), userID, req.CompanyName)
	if err != nil {
		log.Printf("[IdentityHandler] CreateWorkspace FAILED user=%s: %v", userID, err)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("[IdentityHandler] CreateWorkspace SUCCESS: user=%s created tenant=%s (%s)", userID, tenant.ID, tenant.CompanyName)
	utils.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "Workspace created successfully",
		"tenant":  tenant,
		"role":    "owner",
	})
}

// DeleteWorkspace handles: DELETE /api/v1/workspaces
func (h *IdentityHandler) DeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing user context")
		return
	}
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	if err := h.useCase.DeleteWorkspace(r.Context(), userID, tenantID); err != nil {
		log.Printf("[IdentityHandler] DeleteWorkspace FAILED user=%s tenant=%s: %v", userID, tenantID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[IdentityHandler] DeleteWorkspace SUCCESS: user=%s deleted tenant=%s", userID, tenantID)
	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Workspace deleted successfully",
	})
}
