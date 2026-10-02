package handler

import (
	"encoding/json"
	"log"
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
		log.Printf("[TeamHandler] ListTeam REJECTED: missing tenant context in request context (path: %s)\n", r.URL.Path)
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	log.Printf("[TeamHandler] ListTeam requested for tenant_id=%s\n", tenantID)
	overview, err := h.useCase.ListTeam(r.Context(), tenantID)
	if err != nil {
		log.Printf("[TeamHandler] ListTeam FAILED for tenant_id=%s: %v\n", tenantID, err)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("[TeamHandler] ListTeam SUCCESS for tenant_id=%s: %d members, %d invites\n", tenantID, len(overview.Members), len(overview.Invitations))
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
		log.Printf("[TeamHandler] InviteMember REJECTED: missing tenant context\n")
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	callerID, _ := r.Context().Value(UserIDKey).(string)
	callerRole, _ := r.Context().Value(UserRoleKey).(string)

	var req inviteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[TeamHandler] InviteMember JSON decode error: %v\n", err)
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	log.Printf("[TeamHandler] InviteMember: caller=%s (role=%s) inviting email=%s as role=%s (tenant=%s)\n", callerID, callerRole, req.Email, req.Role, tenantID)
	inv, err := h.useCase.InviteMember(r.Context(), tenantID, callerID, callerRole, req.Email, req.Role)
	if err != nil {
		log.Printf("[TeamHandler] InviteMember FAILED for email=%s: %v\n", req.Email, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[TeamHandler] InviteMember SUCCESS for email=%s (invite_id=%s)\n", req.Email, inv.ID)
	utils.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"message":    "Invitation successfully dispatched",
		"invitation": inv,
	})
}

// RevokeInvitation handles: DELETE /api/v1/team/invitations/{id}
func (h *TeamHandler) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		log.Printf("[TeamHandler] RevokeInvitation REJECTED: missing tenant context\n")
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

	log.Printf("[TeamHandler] RevokeInvitation requested for invitation_id=%s (callerRole=%s, tenant=%s)\n", invitationID, callerRole, tenantID)
	if err := h.useCase.RevokeInvitation(r.Context(), tenantID, callerRole, invitationID); err != nil {
		log.Printf("[TeamHandler] RevokeInvitation FAILED for invitation_id=%s: %v\n", invitationID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[TeamHandler] RevokeInvitation SUCCESS for invitation_id=%s\n", invitationID)
	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Invitation revoked successfully"})
}

// RemoveMember handles: DELETE /api/v1/team/members/{id}
func (h *TeamHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		log.Printf("[TeamHandler] RemoveMember REJECTED: missing tenant context\n")
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

	log.Printf("[TeamHandler] RemoveMember requested for member_id=%s by caller=%s (role=%s, tenant=%s)\n", memberID, callerID, callerRole, tenantID)
	if err := h.useCase.RemoveMember(r.Context(), tenantID, callerID, callerRole, memberID); err != nil {
		log.Printf("[TeamHandler] RemoveMember FAILED for member_id=%s: %v\n", memberID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[TeamHandler] RemoveMember SUCCESS for member_id=%s\n", memberID)
	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Team member successfully removed"})
}

type updateRoleReq struct {
	Role string `json:"role"`
}

// UpdateMemberRole handles: PATCH /api/v1/team/members/{id}/role
func (h *TeamHandler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		log.Printf("[TeamHandler] UpdateMemberRole REJECTED: missing tenant context\n")
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
		log.Printf("[TeamHandler] UpdateMemberRole JSON decode error: %v\n", err)
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	log.Printf("[TeamHandler] UpdateMemberRole: member_id=%s newRole=%s (callerRole=%s, tenant=%s)\n", memberID, req.Role, callerRole, tenantID)
	if err := h.useCase.UpdateMemberRole(r.Context(), tenantID, callerRole, memberID, req.Role); err != nil {
		log.Printf("[TeamHandler] UpdateMemberRole FAILED for member_id=%s: %v\n", memberID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[TeamHandler] UpdateMemberRole SUCCESS for member_id=%s -> %s\n", memberID, req.Role)
	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Member role successfully updated"})
}

// GetInvitationPreview handles: GET /api/v1/invitations/preview?token={token} (Public)
func (h *TeamHandler) GetInvitationPreview(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		log.Printf("[TeamHandler] GetInvitationPreview REJECTED: missing token query param\n")
		utils.WriteError(w, http.StatusBadRequest, "Missing invitation token")
		return
	}

	preview, err := h.useCase.GetInvitationPreview(r.Context(), token)
	if err != nil {
		log.Printf("[TeamHandler] GetInvitationPreview FAILED: %v\n", err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[TeamHandler] GetInvitationPreview SUCCESS for workspace=%s, invited=%s\n", preview.WorkspaceName, preview.InvitedEmail)
	utils.WriteJSON(w, http.StatusOK, preview)
}

type acceptInviteReq struct {
	Token string `json:"token"`
}

// AcceptInvitation handles: POST /api/v1/invitations/accept (Authenticated)
func (h *TeamHandler) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok || userID == "" {
		log.Printf("[TeamHandler] AcceptInvitation REJECTED: missing authenticated user context\n")
		utils.WriteError(w, http.StatusUnauthorized, "Missing authenticated user context")
		return
	}

	var req acceptInviteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[TeamHandler] AcceptInvitation JSON decode error: %v\n", err)
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	log.Printf("[TeamHandler] AcceptInvitation requested by user_id=%s\n", userID)
	tenant, err := h.useCase.AcceptInvitation(r.Context(), req.Token, userID)
	if err != nil {
		log.Printf("[TeamHandler] AcceptInvitation FAILED for user_id=%s: %v\n", userID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("[TeamHandler] AcceptInvitation SUCCESS: user_id=%s joined tenant=%s\n", userID, tenant.ID)

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Successfully joined workspace",
		"tenant":  tenant,
	})
}
