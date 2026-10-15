package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

// ─── Mock Inbox UseCase ──────────────────────────────────────────────────────

type mockInboxUseCase struct {
	listConversationsFunc        func(ctx context.Context, tenantID string, filter domain.ConversationFilter) ([]domain.Conversation, int, error)
	getConversationFunc          func(ctx context.Context, tenantID, conversationID string) (*domain.Conversation, error)
	listMessagesFunc             func(ctx context.Context, tenantID, conversationID string, limit, offset int) ([]domain.Message, error)
	updateConversationStatusFunc func(ctx context.Context, tenantID, conversationID string, status domain.ConversationStatus) error
	assignConversationFunc       func(ctx context.Context, tenantID, conversationID string, userID *string) error
	markConversationAsReadFunc   func(ctx context.Context, tenantID, conversationID string) error
	sendReplyFunc                func(ctx context.Context, tenantID, userID, conversationID string, req domain.SendReplyRequest) (*domain.Message, error)
}

func (m *mockInboxUseCase) ListConversations(ctx context.Context, tenantID string, filter domain.ConversationFilter) ([]domain.Conversation, int, error) {
	if m.listConversationsFunc != nil {
		return m.listConversationsFunc(ctx, tenantID, filter)
	}
	return nil, 0, nil
}

func (m *mockInboxUseCase) GetConversation(ctx context.Context, tenantID, conversationID string) (*domain.Conversation, error) {
	if m.getConversationFunc != nil {
		return m.getConversationFunc(ctx, tenantID, conversationID)
	}
	return nil, nil
}

func (m *mockInboxUseCase) ListMessages(ctx context.Context, tenantID, conversationID string, limit, offset int) ([]domain.Message, error) {
	if m.listMessagesFunc != nil {
		return m.listMessagesFunc(ctx, tenantID, conversationID, limit, offset)
	}
	return nil, nil
}

func (m *mockInboxUseCase) UpdateConversationStatus(ctx context.Context, tenantID, conversationID string, status domain.ConversationStatus) error {
	if m.updateConversationStatusFunc != nil {
		return m.updateConversationStatusFunc(ctx, tenantID, conversationID, status)
	}
	return nil
}

func (m *mockInboxUseCase) AssignConversation(ctx context.Context, tenantID, conversationID string, userID *string) error {
	if m.assignConversationFunc != nil {
		return m.assignConversationFunc(ctx, tenantID, conversationID, userID)
	}
	return nil
}

func (m *mockInboxUseCase) MarkConversationAsRead(ctx context.Context, tenantID, conversationID string) error {
	if m.markConversationAsReadFunc != nil {
		return m.markConversationAsReadFunc(ctx, tenantID, conversationID)
	}
	return nil
}

func (m *mockInboxUseCase) SendReply(ctx context.Context, tenantID, userID, conversationID string, req domain.SendReplyRequest) (*domain.Message, error) {
	if m.sendReplyFunc != nil {
		return m.sendReplyFunc(ctx, tenantID, userID, conversationID, req)
	}
	return nil, nil
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestInboxHandler_UnauthorizedWithoutTenant(t *testing.T) {
	h := NewInboxHandler(&mockInboxUseCase{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/inbox/conversations", nil)
	rr := httptest.NewRecorder()

	h.ListConversations(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rr.Code)
	}
}

func TestInboxHandler_ListConversations(t *testing.T) {
	mockUC := &mockInboxUseCase{
		listConversationsFunc: func(ctx context.Context, tenantID string, filter domain.ConversationFilter) ([]domain.Conversation, int, error) {
			if filter.Channel != "whatsapp" {
				return nil, 0, fmt.Errorf("expected channel filter whatsapp, got %s", filter.Channel)
			}
			return []domain.Conversation{
				{ID: "conv-1", TenantID: tenantID, Channel: "whatsapp"},
			}, 1, nil
		},
	}
	h := NewInboxHandler(mockUC)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/inbox/conversations?channel=whatsapp&limit=10&offset=0", nil)
	ctx := context.WithValue(req.Context(), TenantIDKey, "test-tenant-1")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.ListConversations(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Data struct {
			Conversations []domain.Conversation `json:"conversations"`
			Total         int                   `json:"total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Data.Total != 1 || len(resp.Data.Conversations) != 1 {
		t.Fatalf("expected 1 conversation, got total=%d len=%d", resp.Data.Total, len(resp.Data.Conversations))
	}
}

func TestInboxHandler_GetConversation(t *testing.T) {
	mockUC := &mockInboxUseCase{
		getConversationFunc: func(ctx context.Context, tenantID, conversationID string) (*domain.Conversation, error) {
			if conversationID == "conv-1" {
				return &domain.Conversation{ID: "conv-1", TenantID: tenantID}, nil
			}
			return nil, fmt.Errorf("not found")
		},
	}
	h := NewInboxHandler(mockUC)

	// Valid ID
	req := httptest.NewRequest(http.MethodGet, "/api/v1/inbox/conversations/conv-1", nil)
	req.SetPathValue("id", "conv-1")
	ctx := context.WithValue(req.Context(), TenantIDKey, "test-tenant-1")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.GetConversation(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr.Code)
	}

	// Missing ID
	reqMissing := httptest.NewRequest(http.MethodGet, "/api/v1/inbox/conversations/", nil)
	ctxMissing := context.WithValue(reqMissing.Context(), TenantIDKey, "test-tenant-1")
	reqMissing = reqMissing.WithContext(ctxMissing)
	rrMissing := httptest.NewRecorder()

	h.GetConversation(rrMissing, reqMissing)
	if rrMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing id, got %d", rrMissing.Code)
	}
}

func TestInboxHandler_SendReply(t *testing.T) {
	mockUC := &mockInboxUseCase{
		sendReplyFunc: func(ctx context.Context, tenantID, userID, conversationID string, req domain.SendReplyRequest) (*domain.Message, error) {
			return &domain.Message{
				ID:             "msg-1",
				TenantID:       tenantID,
				ConversationID: conversationID,
				Body:           req.Body,
				Status:         "sent",
				CreatedAt:      time.Now(),
			}, nil
		},
	}
	h := NewInboxHandler(mockUC)

	// Valid reply
	body, _ := json.Marshal(domain.SendReplyRequest{Body: "Thanks for reaching out!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inbox/conversations/conv-1/reply", bytes.NewBuffer(body))
	req.SetPathValue("id", "conv-1")
	ctx := context.WithValue(req.Context(), TenantIDKey, "test-tenant-1")
	ctx = context.WithValue(ctx, UserIDKey, "user-admin")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.SendReply(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Empty body should fail
	emptyBody, _ := json.Marshal(domain.SendReplyRequest{Body: ""})
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/v1/inbox/conversations/conv-1/reply", bytes.NewBuffer(emptyBody))
	reqEmpty.SetPathValue("id", "conv-1")
	reqEmpty = reqEmpty.WithContext(ctx)
	rrEmpty := httptest.NewRecorder()

	h.SendReply(rrEmpty, reqEmpty)
	if rrEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for empty reply, got %d", rrEmpty.Code)
	}
}

func TestInboxHandler_UpdateConversationAndMarkRead(t *testing.T) {
	var statusUpdated domain.ConversationStatus
	var markedRead bool

	mockUC := &mockInboxUseCase{
		updateConversationStatusFunc: func(ctx context.Context, tenantID, conversationID string, status domain.ConversationStatus) error {
			statusUpdated = status
			return nil
		},
		markConversationAsReadFunc: func(ctx context.Context, tenantID, conversationID string) error {
			markedRead = true
			return nil
		},
	}
	h := NewInboxHandler(mockUC)

	ctx := context.WithValue(context.Background(), TenantIDKey, "test-tenant-1")

	// 1. Update Status
	st := domain.ConversationStatusClosed
	reqData, _ := json.Marshal(map[string]interface{}{"status": st})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/inbox/conversations/conv-1", bytes.NewBuffer(reqData))
	req.SetPathValue("id", "conv-1")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.UpdateConversation(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for update, got %d", rr.Code)
	}
	if statusUpdated != domain.ConversationStatusClosed {
		t.Fatalf("expected status closed, got %s", statusUpdated)
	}

	// 2. Mark as read
	reqRead := httptest.NewRequest(http.MethodPost, "/api/v1/inbox/conversations/conv-1/read", nil)
	reqRead.SetPathValue("id", "conv-1")
	reqRead = reqRead.WithContext(ctx)
	rrRead := httptest.NewRecorder()

	h.MarkAsRead(rrRead, reqRead)
	if rrRead.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for mark read, got %d", rrRead.Code)
	}
	if !markedRead {
		t.Fatalf("expected markedRead to be true")
	}
}
