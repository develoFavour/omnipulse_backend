package service

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"omnipulse/apps/api-gateway/internal/ctxkeys"
	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"

	"github.com/coder/websocket"
)

// InboxEvent represents a real-time WebSocket push packet to the inbox UI
type InboxEvent struct {
	Type           string               `json:"type"` // "NEW_MESSAGE", "CONVERSATION_UPDATED", "CONVERSATION_READ"
	TenantID       string               `json:"tenant_id"`
	ConversationID string               `json:"conversation_id"`
	Message        *domain.Message      `json:"message,omitempty"`
	Conversation   *domain.Conversation `json:"conversation,omitempty"`
	Timestamp      string               `json:"timestamp"`
}

type inboxClientConn struct {
	conn *websocket.Conn
	send chan []byte
}

// InboxHub coordinates real-time WebSocket connections per tenant
type InboxHub struct {
	mu          sync.RWMutex
	subscribers map[string]map[*inboxClientConn]struct{} // tenantID -> set of connections
}

func NewInboxHub() *InboxHub {
	return &InboxHub{
		subscribers: make(map[string]map[*inboxClientConn]struct{}),
	}
}

// HandleWebSocket upgrades HTTP to WebSocket and streams real-time inbox events
func (h *InboxHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(ctxkeys.TenantIDKey).(string)
	if !ok || tenantID == "" {
		tenantID = r.URL.Query().Get("tenant_id")
		if tenantID == "" {
			utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
			return
		}
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Printf("[INBOX-WS] Failed to accept websocket connection: %v\n", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "connection closed")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	client := &inboxClientConn{
		conn: c,
		send: make(chan []byte, 32),
	}

	h.mu.Lock()
	if _, exists := h.subscribers[tenantID]; !exists {
		h.subscribers[tenantID] = make(map[*inboxClientConn]struct{})
	}
	h.subscribers[tenantID][client] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		if clients, exists := h.subscribers[tenantID]; exists {
			delete(clients, client)
			if len(clients) == 0 {
				delete(h.subscribers, tenantID)
			}
		}
		h.mu.Unlock()
		close(client.send)
	}()

	log.Printf("[INBOX-WS] 🔌 Client connected to live inbox for tenant %s\n", tenantID)

	// Write pump
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-client.send:
				if !ok {
					return
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
				err := c.Write(writeCtx, websocket.MessageText, msg)
				writeCancel()
				if err != nil {
					return
				}
			}
		}
	}()

	// Read pump (keeps connection open and detects disconnect)
	for {
		_, _, err := c.Read(ctx)
		if err != nil {
			break
		}
	}
	c.Close(websocket.StatusNormalClosure, "closed by client")
	log.Printf("[INBOX-WS] Client disconnected from inbox for tenant %s\n", tenantID)
}

// BroadcastMessage publishes a new message event to all connected dashboard tabs of the tenant
func (h *InboxHub) BroadcastMessage(tenantID string, conv *domain.Conversation, msg *domain.Message) {
	if h == nil {
		return
	}
	h.mu.RLock()
	clients, ok := h.subscribers[tenantID]
	if !ok || len(clients) == 0 {
		h.mu.RUnlock()
		return
	}

	evt := InboxEvent{
		Type:           "NEW_MESSAGE",
		TenantID:       tenantID,
		ConversationID: msg.ConversationID,
		Message:        msg,
		Conversation:   conv,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		h.mu.RUnlock()
		return
	}

	for client := range clients {
		select {
		case client.send <- payload:
		default:
			log.Printf("[INBOX-WS] ⚠️ Buffer full for tenant %s client, dropping frame\n", tenantID)
		}
	}
	h.mu.RUnlock()
}

// BroadcastStatusUpdate pushes a STATUS_UPDATE event to all connected clients of a tenant.
// The frontend uses this to update the message tick (sent → delivered → read) in real time.
func (h *InboxHub) BroadcastStatusUpdate(tenantID, conversationID, messageID, status string) {
	if h == nil {
		return
	}
	h.mu.RLock()
	clients, ok := h.subscribers[tenantID]
	if !ok || len(clients) == 0 {
		h.mu.RUnlock()
		return
	}

	type statusPayload struct {
		Type           string `json:"type"`
		TenantID       string `json:"tenant_id"`
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		Status         string `json:"status"`
		Timestamp      string `json:"timestamp"`
	}

	evt := statusPayload{
		Type:           "STATUS_UPDATE",
		TenantID:       tenantID,
		ConversationID: conversationID,
		MessageID:      messageID,
		Status:         status,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		h.mu.RUnlock()
		return
	}

	for client := range clients {
		select {
		case client.send <- payload:
		default:
			log.Printf("[INBOX-WS] ⚠️ Buffer full for tenant %s client, dropping STATUS_UPDATE frame\n", tenantID)
		}
	}
	h.mu.RUnlock()
}

// BroadcastMessageEdit pushes a MESSAGE_EDITED event so all connected clients update
// the message body in real time without a page refresh.
func (h *InboxHub) BroadcastMessageEdit(tenantID, conversationID, messageID, newBody string) {
	if h == nil {
		return
	}
	h.mu.RLock()
	clients, ok := h.subscribers[tenantID]
	if !ok || len(clients) == 0 {
		h.mu.RUnlock()
		return
	}

	evt := struct {
		Type           string `json:"type"`
		TenantID       string `json:"tenant_id"`
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		NewBody        string `json:"new_body"`
		Timestamp      string `json:"timestamp"`
	}{
		Type:           "MESSAGE_EDITED",
		TenantID:       tenantID,
		ConversationID: conversationID,
		MessageID:      messageID,
		NewBody:        newBody,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		h.mu.RUnlock()
		return
	}

	for client := range clients {
		select {
		case client.send <- payload:
		default:
			log.Printf("[INBOX-WS] ⚠️ Buffer full for tenant %s client, dropping MESSAGE_EDITED frame\n", tenantID)
		}
	}
	h.mu.RUnlock()
}

// BroadcastMessageDelete pushes a MESSAGE_DELETED event so all connected clients
// soft-remove the message from the thread in real time.
func (h *InboxHub) BroadcastMessageDelete(tenantID, conversationID, messageID string) {
	if h == nil {
		return
	}
	h.mu.RLock()
	clients, ok := h.subscribers[tenantID]
	if !ok || len(clients) == 0 {
		h.mu.RUnlock()
		return
	}

	evt := struct {
		Type           string `json:"type"`
		TenantID       string `json:"tenant_id"`
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		Timestamp      string `json:"timestamp"`
	}{
		Type:           "MESSAGE_DELETED",
		TenantID:       tenantID,
		ConversationID: conversationID,
		MessageID:      messageID,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		h.mu.RUnlock()
		return
	}

	for client := range clients {
		select {
		case client.send <- payload:
		default:
			log.Printf("[INBOX-WS] ⚠️ Buffer full for tenant %s client, dropping MESSAGE_DELETED frame\n", tenantID)
		}
	}
	h.mu.RUnlock()
}
