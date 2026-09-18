package worker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/service"
	"omnipulse/shared/contracts"

	"github.com/nats-io/nats.go"
)

// BroadcastConsumer manages delivery tasks consumed directly from NATS JetStream.
type BroadcastConsumer struct {
	nc        *nats.Conn
	js        nats.JetStreamContext
	sub       *nats.Subscription
	db        *sql.DB
	waManager *service.WhatsAppManager
}

// NewBroadcastConsumer creates a new broadcast delivery worker reusing existing NATS and DB connections.
func NewBroadcastConsumer(nc *nats.Conn, js nats.JetStreamContext, db *sql.DB, waManager *service.WhatsAppManager) (*BroadcastConsumer, error) {
	if nc == nil || js == nil {
		return nil, fmt.Errorf("nats connection or jetstream context is nil")
	}
	return &BroadcastConsumer{
		nc:        nc,
		js:        js,
		db:        db,
		waManager: waManager,
	}, nil
}

// Start initiates the QueueSubscription on campaign.dispatched.
func (c *BroadcastConsumer) Start(ctx context.Context) error {
	sub, err := c.js.QueueSubscribe(
		"campaign.dispatched",
		"broadcast-delivery-v1",
		func(msg *nats.Msg) {
			c.executeDelivery(ctx, msg)
		},
		nats.ManualAck(),
		nats.DeliverAll(),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe to campaign.dispatched: %w", err)
	}

	c.sub = sub
	log.Println("[BROADCAST-WORKER] 🚀 Outbound delivery engine active and listening to campaign.dispatched (queue: broadcast-delivery-v1)...")
	return nil
}

// Stop safely unsubscribes the consumer.
func (c *BroadcastConsumer) Stop() {
	if c.sub != nil {
		_ = c.sub.Unsubscribe()
	}
	log.Println("[BROADCAST-WORKER] Broadcast delivery engine cleanly disconnected.")
}

func (c *BroadcastConsumer) executeDelivery(ctx context.Context, msg *nats.Msg) {
	msgCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

	var task contracts.TargetDispatchTask
	if err := json.Unmarshal(msg.Data, &task); err != nil {
		log.Printf("[BROADCAST-WORKER-ERROR] Failed to unmarshal task: %v. Dropping message.\n", err)
		_ = msg.Term()
		return
	}

	log.Printf("[BROADCAST-WORKER] 📨 Received dispatch task: campaign=%s contact=%s (%s) platform=%s\n",
		task.CampaignID, task.ContactID, task.RoutingValue, task.TargetPlatform)

	// TTL guard: discard stale messages that arrived after the campaign expiry window.
	// This prevents "zombie broadcasts" delivering announcements hours or days after dispatch.
	if task.ExpiresAt > 0 && time.Now().Unix() > task.ExpiresAt {
		expiredReason := fmt.Sprintf("message expired: dispatch window closed at %s (arrived %s late)",
			time.Unix(task.ExpiresAt, 0).UTC().Format(time.RFC3339),
			time.Since(time.Unix(task.ExpiresAt, 0)).Truncate(time.Second))
		log.Printf("[BROADCAST-WORKER] ⏰ EXPIRED task for %s (%s): %s — skipping delivery\n",
			task.FirstName, task.RoutingValue, expiredReason)

		// Still emit a delivery result so telemetryWorker can track the count and eventually complete the campaign
		expiredResult := contracts.TargetDeliveryResult{
			CampaignID:   task.CampaignID,
			ContactID:    task.ContactID,
			TargetType:   deliveryNormalizedTargetType(task.TargetType),
			Platform:     task.TargetPlatform,
			RoutingValue: task.RoutingValue,
			Status:       "failed",
			ErrorMessage: &expiredReason,
		}
		if resultBytes, err := json.Marshal(expiredResult); err == nil {
			_, _ = c.js.Publish("dispatch.result", resultBytes)
		}
		_ = msg.Ack()
		return
	}

	status := "delivered"
	var errMsg *string

	personalizedMsg := strings.ReplaceAll(task.MessageBody, "{first_name}", task.FirstName)

	if task.TargetPlatform == "telegram" {
		var tokenData []byte
		err := c.db.QueryRowContext(msgCtx, "SELECT encrypted_credentials FROM tenant_channels WHERE tenant_id = $1 AND platform_name = 'telegram' AND status = 'active' LIMIT 1", task.TenantID).Scan(&tokenData)
		if err != nil {
			status = "failed"
			reason := fmt.Sprintf("no active Telegram channel configured for tenant %s: %v", task.TenantID, err)
			errMsg = &reason
			log.Printf("[❌ TELEGRAM -> ERROR] %s\n", reason)
		} else {
			var creds struct {
				BotToken string `json:"bot_token"`
			}
			if err := json.Unmarshal(tokenData, &creds); err != nil || creds.BotToken == "" {
				status = "failed"
				reason := "failed to parse telegram bot credentials"
				errMsg = &reason
				log.Printf("[❌ TELEGRAM -> ERROR] %s\n", reason)
			} else {
				var tgURL string
				var tgPayload map[string]interface{}
				if task.MediaURL != nil && *task.MediaURL != "" {
					tgURL = fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", creds.BotToken)
					tgPayload = map[string]interface{}{
						"chat_id": task.RoutingValue,
						"photo":   *task.MediaURL,
						"caption": personalizedMsg,
					}
				} else {
					tgURL = fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", creds.BotToken)
					tgPayload = map[string]interface{}{
						"chat_id": task.RoutingValue,
						"text":    personalizedMsg,
					}
				}

				payloadBytes, _ := json.Marshal(tgPayload)
				req, reqErr := http.NewRequestWithContext(msgCtx, "POST", tgURL, bytes.NewBuffer(payloadBytes))
				if reqErr != nil {
					status = "failed"
					reason := fmt.Sprintf("failed to construct telegram request: %v", reqErr)
					errMsg = &reason
					log.Printf("[❌ TELEGRAM -> ERROR] %s\n", reason)
				} else {
					req.Header.Set("Content-Type", "application/json")
					tgClient := &http.Client{Timeout: 15 * time.Second}
					resp, err := tgClient.Do(req)
					if err != nil {
						status = "failed"
						reason := fmt.Sprintf("network error calling Telegram API: %v", err)
						errMsg = &reason
						log.Printf("[❌ TELEGRAM -> ERROR] %s\n", reason)
					} else {
						defer resp.Body.Close()
						if resp.StatusCode != http.StatusOK {
							status = "failed"
							reason := fmt.Sprintf("Telegram API returned non-200 status (%d)", resp.StatusCode)
							errMsg = &reason
							log.Printf("[❌ TELEGRAM -> ERROR] %s\n", reason)
						} else {
							log.Printf("[📲 TELEGRAM] Dispatched cleanly to %s (%s)\n", task.FirstName, task.RoutingValue)
						}
					}
				}
			}
		}
	} else if task.TargetPlatform == "whatsapp" {
		sent := false

		// 1. Try WhatsApp Multi-Device session first if available
		if c.waManager != nil {
			waErr := c.waManager.SendMessage(msgCtx, task.TenantID, task.RoutingValue, personalizedMsg, task.MediaURL)
			if waErr == nil {
				sent = true
				log.Printf("[📲 WHATSAPP MULTI-DEVICE] Dispatched cleanly to %s (%s)\n", task.FirstName, task.RoutingValue)
			} else {
				log.Printf("[⚠️ WHATSAPP MULTI-DEVICE] Send attempt failed: %v. Checking Cloud API fallback...\n", waErr)
			}
		}

		// 2. Cloud API Fallback
		if !sent {
			var tokenData []byte
			err := c.db.QueryRowContext(msgCtx, "SELECT encrypted_credentials FROM tenant_channels WHERE tenant_id = $1 AND platform_name = 'whatsapp' AND status = 'active' LIMIT 1", task.TenantID).Scan(&tokenData)
			if err != nil {
				status = "failed"
				reason := fmt.Sprintf("no active WhatsApp channel found: %v", err)
				errMsg = &reason
				log.Printf("[❌ WHATSAPP -> ERROR] %s\n", reason)
			} else {
				var creds map[string]interface{}
				_ = json.Unmarshal(tokenData, &creds)
				phoneID, _ := creds["phone_number_id"].(string)
				token, _ := creds["access_token"].(string)

				if phoneID == "" || token == "" {
					status = "failed"
					reason := "WhatsApp Multi-Device disconnected and no Cloud API credentials found"
					errMsg = &reason
					log.Printf("[❌ WHATSAPP -> ERROR] %s\n", reason)
				} else {
					var waPayload map[string]interface{}
					if task.MediaURL != nil && *task.MediaURL != "" {
						waPayload = map[string]interface{}{
							"messaging_product": "whatsapp",
							"recipient_type":    "individual",
							"to":                task.RoutingValue,
							"type":              "image",
							"image": map[string]interface{}{
								"link":    *task.MediaURL,
								"caption": personalizedMsg,
							},
						}
					} else {
						waPayload = map[string]interface{}{
							"messaging_product": "whatsapp",
							"recipient_type":    "individual",
							"to":                task.RoutingValue,
							"type":              "text",
							"text": map[string]interface{}{
								"preview_url": false,
								"body":        personalizedMsg,
							},
						}
					}

					payloadBytes, _ := json.Marshal(waPayload)
					waURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/messages", phoneID)
					req, _ := http.NewRequestWithContext(msgCtx, "POST", waURL, bytes.NewBuffer(payloadBytes))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer "+token)

					client := &http.Client{Timeout: 15 * time.Second}
					resp, err := client.Do(req)
					if err != nil {
						status = "failed"
						reason := fmt.Sprintf("network error calling WhatsApp Cloud API: %v", err)
						errMsg = &reason
						log.Printf("[❌ WHATSAPP API -> ERROR] %s\n", reason)
					} else {
						defer resp.Body.Close()
						if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
							status = "failed"
							reason := fmt.Sprintf("WhatsApp Cloud API rejected message (status %d)", resp.StatusCode)
							errMsg = &reason
							log.Printf("[❌ WHATSAPP API -> ERROR] %s\n", reason)
						} else {
							log.Printf("[📲 WHATSAPP CLOUD API] Dispatched cleanly to %s (%s)\n", task.FirstName, task.RoutingValue)
						}
					}
				}
			}
		}
	} else {
		status = "failed"
		reason := fmt.Sprintf("unsupported platform '%s' — no delivery adapter configured", task.TargetPlatform)
		errMsg = &reason
		log.Printf("[❌ WORKER -> ERROR] %s\n", reason)
	}

	result := contracts.TargetDeliveryResult{
		CampaignID:   task.CampaignID,
		ContactID:    task.ContactID,
		TargetType:   deliveryNormalizedTargetType(task.TargetType),
		Platform:     task.TargetPlatform,
		RoutingValue: task.RoutingValue,
		Status:       status,
		ErrorMessage: errMsg,
	}

	resultBytes, _ := json.Marshal(result)
	_, pubErr := c.js.Publish("dispatch.result", resultBytes)
	if pubErr != nil {
		log.Printf("[BROADCAST-WORKER-ERROR] Failed to publish return receipt onto dispatch.result: %v\n", pubErr)
		_ = msg.Nak()
		return
	}

	log.Printf("[BROADCAST-WORKER] ✅ Finished task %s for contact %s, status=%s\n", task.CampaignID, task.ContactID, status)
	_ = msg.Ack()
}

func deliveryNormalizedTargetType(targetType string) string {
	if targetType == "telegram_destination" {
		return targetType
	}
	return "contact"
}
