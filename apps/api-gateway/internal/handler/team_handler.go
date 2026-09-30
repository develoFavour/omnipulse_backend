package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"
)

type TeamHandler struct {
	useCase *usecase.TeamUseCase
}

func NewTeamHandler(useCase *usecase.TeamUseCase) *TeamHandler {
	return &TeamHandler{useCase: useCase}
}

// ListTeam handles: GET /api/v1/team/members
func (h *TeamHandler) ListTeam(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	overview, err := h.useCase.ListTeam(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, overview)
}

type inviteReq struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// InviteMember handles: POST /api/v1/team/invite
func (h *TeamHandler) InviteMember(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	callerID, _ := r.Context().Value(UserIDKey).(string)
	callerRole, _ := r.Context().Value(UserRoleKey).(string)

	var req inviteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	inv, err := h.useCase.InviteMember(r.Context(), tenantID, callerID, callerRole, req.Email, req.Role)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"message":    "Invitation successfully dispatched",
		"invitation": inv,
	})
}

// RevokeInvitation handles: DELETE /api/v1/team/invitations/{id}
func (h *TeamHandler) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	callerRole, _ := r.Context().Value(UserRoleKey).(string)

	// Extract invitation ID from URL path: /api/v1/team/invitations/{id}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		utils.WriteError(w, http.StatusBadRequest, "Missing invitation ID")
		return
	}
	invitationID := parts[4]

	if err := h.useCase.RevokeInvitation(r.Context(), tenantID, callerRole, invitationID); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Invitation revoked successfully"})
}

// RemoveMember handles: DELETE /api/v1/team/members/{id}
func (h *TeamHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	callerID, _ := r.Context().Value(UserIDKey).(string)
	callerRole, _ := r.Context().Value(UserRoleKey).(string)

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		utils.WriteError(w, http.StatusBadRequest, "Missing member ID")
		return
	}
	memberID := parts[4]

	if err := h.useCase.RemoveMember(r.Context(), tenantID, callerID, callerRole, memberID); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Team member successfully removed"})
}

type updateRoleReq struct {
	Role string `json:"role"`
}

// UpdateMemberRole handles: PATCH /api/v1/team/members/{id}/role
func (h *TeamHandler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	callerRole, _ := r.Context().Value(UserRoleKey).(string)

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		utils.WriteError(w, http.StatusBadRequest, "Missing member ID")
		return
	}
	memberID := parts[4]

	var req updateRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.useCase.UpdateMemberRole(r.Context(), tenantID, callerRole, memberID, req.Role); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Member role successfully updated"})
}

// GetInvitationPreview handles: GET /api/v1/invitations/preview?token={token} (Public)
func (h *TeamHandler) GetInvitationPreview(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing invitation token")
		return
	}

	preview, err := h.useCase.GetInvitationPreview(r.Context(), token)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, preview)
}

type acceptInviteReq struct {
	Token string `json:"token"`
}

// AcceptInvitation handles: POST /api/v1/invitations/accept (Authenticated)
func (h *TeamHandler) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing authenticated user context")
		return
	}

	var req acceptInviteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	tenant, err := h.useCase.AcceptInvitation(r.Context(), req.Token, userID)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Successfully joined workspace",
		"tenant":  tenant,
	})
}
