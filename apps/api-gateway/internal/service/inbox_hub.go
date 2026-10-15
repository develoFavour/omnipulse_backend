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
			http.Error(w, "missing tenant context", http.StatusUnauthorized)
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
