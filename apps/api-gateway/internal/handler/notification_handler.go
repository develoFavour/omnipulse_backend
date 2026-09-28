package handler

import (
	"net/http"
	"strconv"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type NotificationHandler struct {
	useCase domain.NotificationUseCase
}

func NewNotificationHandler(useCase domain.NotificationUseCase) *NotificationHandler {
	return &NotificationHandler{useCase: useCase}
}

// List handles: GET /api/v1/notifications?limit=20
func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	notifications, err := h.useCase.List(r.Context(), tenantID, limit)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to fetch notifications: "+err.Error())
		return
	}

	unread, _ := h.useCase.UnreadCount(r.Context(), tenantID)

	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"notifications": notifications,
		"unread_count":  unread,
	})
}

// MarkRead handles: PATCH /api/v1/notifications/{id}/read
func (h *NotificationHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing notification ID")
		return
	}

	if err := h.useCase.MarkRead(r.Context(), tenantID, id); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to mark notification as read")
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Notification marked as read"})
}

// MarkAllRead handles: PATCH /api/v1/notifications/read-all
func (h *NotificationHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	if err := h.useCase.MarkAllRead(r.Context(), tenantID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to mark all notifications as read")
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "All notifications marked as read"})
}
