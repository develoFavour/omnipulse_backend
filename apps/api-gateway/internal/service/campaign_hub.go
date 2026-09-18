package service

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"

	"github.com/coder/websocket"
)

// CampaignEvent represents a real-time WebSocket telemetry push packet
type CampaignEvent struct {
	Type       string                   `json:"type"` // "INIT", "PROGRESS", "COMPLETED"
	CampaignID string                   `json:"campaign_id"`
	Stats      *domain.CampaignStats    `json:"stats,omitempty"`
	Delivery   *domain.CampaignDelivery `json:"delivery,omitempty"`
	Timestamp  string                   `json:"timestamp"`
}

// CampaignHub coordinates real-time WebSocket connections per campaign
type CampaignHub struct {
	mu          sync.RWMutex
	subscribers map[string]map[*clientConn]struct{} // campaignID -> set of clientConn
	repo        domain.CampaignRepository
}

type clientConn struct {
	conn *websocket.Conn
	send chan []byte
}

func NewCampaignHub(repo domain.CampaignRepository) *CampaignHub {
	return &CampaignHub{
		subscribers: make(map[string]map[*clientConn]struct{}),
		repo:        repo,
	}
}

// HandleWebSocket upgrades HTTP to WebSocket and streams campaign telemetry
func (h *CampaignHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	if campaignID == "" {
		http.Error(w, "missing campaign id", http.StatusBadRequest)
		return
	}

	// Accept WebSocket connection from any configured origin
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Printf("[WS-HUB] Failed to accept websocket connection: %v\n", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "the connection was closed")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	client := &clientConn{
		conn: c,
		send: make(chan []byte, 32),
	}

	// Register subscriber
	h.mu.Lock()
	if _, ok := h.subscribers[campaignID]; !ok {
		h.subscribers[campaignID] = make(map[*clientConn]struct{})
	}
	h.subscribers[campaignID][client] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		if clients, ok := h.subscribers[campaignID]; ok {
			delete(clients, client)
			if len(clients) == 0 {
				delete(h.subscribers, campaignID)
			}
		}
		h.mu.Unlock()
		close(client.send)
	}()

	log.Printf("[WS-HUB] 🔌 Client connected to live stream for campaign %s\n", campaignID)

	// Send initial snapshot on connect
	go func() {
		stats, err := h.repo.GetCampaignStats(ctx, "", campaignID)
		if err == nil && stats != nil {
			initEvt := CampaignEvent{
				Type:       "INIT",
				CampaignID: campaignID,
				Stats:      stats,
				Timestamp:  time.Now().UTC().Format(time.RFC3339),
			}
			if b, err := json.Marshal(initEvt); err == nil {
				select {
				case client.send <- b:
				default:
				}
			}
		}
	}()

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

	// Read pump (keeps connection open and detects close)
	for {
		_, _, err := c.Read(ctx)
		if err != nil {
			break
		}
	}
	c.Close(websocket.StatusNormalClosure, "closed by client")
	log.Printf("[WS-HUB] Client disconnected from campaign %s\n", campaignID)
}

// BroadcastProgress pushes live progress & delivery audit to all connected clients
func (h *CampaignHub) BroadcastProgress(campaignID string, stats *domain.CampaignStats, delivery *domain.CampaignDelivery) {
	h.mu.RLock()
	clients, ok := h.subscribers[campaignID]
	if !ok || len(clients) == 0 {
		h.mu.RUnlock()
		return
	}

	evtType := "PROGRESS"
	if stats != nil && (stats.Status == "completed" || stats.Status == "failed") {
		evtType = "COMPLETED"
	}

	evt := CampaignEvent{
		Type:       evtType,
		CampaignID: campaignID,
		Stats:      stats,
		Delivery:   delivery,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	b, err := json.Marshal(evt)
	if err != nil {
		h.mu.RUnlock()
		return
	}

	for client := range clients {
		select {
		case client.send <- b:
		default:
			log.Printf("[WS-HUB-WARN] Dropping message to slow client on campaign %s\n", campaignID)
		}
	}
	h.mu.RUnlock()
}
