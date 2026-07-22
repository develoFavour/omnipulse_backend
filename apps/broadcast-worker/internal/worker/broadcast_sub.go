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

	"omnipulse/shared/contracts"

	"github.com/nats-io/nats.go"
)

type BroadcastConsumer struct {
	nc  *nats.Conn
	js  nats.JetStreamContext
	sub *nats.Subscription
	db  *sql.DB
}

func NewBroadcastConsumer(natsURL string, db *sql.DB) (*BroadcastConsumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}

	// Double check stream configuration to ensure campaign topics are tracked
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "CAMPAIGNS",
		Subjects: []string{"campaign.dispatched", "campaign.approved", "dispatch.result"},
		Storage:  nats.FileStorage,
	})
	if err != nil {
		// Safely ignores if already updated
	}

	return &BroadcastConsumer{nc: nc, js: js, db: db}, nil
}

func (c *BroadcastConsumer) Start(ctx context.Context) error {
	// Subscribe to campaign.approved (listening to the compliance engine)
	sub, err := c.js.QueueSubscribe(
		"campaign.approved",
		"broadcast-worker-v2",
		func(msg *nats.Msg) {
			c.executeDelivery(ctx, msg)
		},
		nats.ManualAck(),
	)
	if err != nil {
		return err
	}

	c.sub = sub
	log.Println("[WORKER] Broadcast Engine actively monitoring outbound streams...")
	return nil
}

func (c *BroadcastConsumer) Stop() {
	if c.sub != nil {
		_ = c.sub.Unsubscribe()
	}
	if c.nc != nil {
		c.nc.Close()
	}
	log.Println("[WORKER] Broadcast Engine cleanly disconnected.")
}

func (c *BroadcastConsumer) executeDelivery(ctx context.Context, msg *nats.Msg) {
	var task contracts.TargetDispatchTask
	if err := json.Unmarshal(msg.Data, &task); err != nil {
		log.Printf("[WORKER-ERROR] Failed to unmarshal task: %v\n", err)
		_ = msg.Term()
		return
	}

	status := "delivered"
	var errMsg *string

	log.Printf("[WORKER] Processing delivery to %s (%s) on channel %s\n", task.FirstName, task.RoutingValue, task.TargetPlatform)

	// Fetch token from DB if target is Telegram
	if task.TargetPlatform == "telegram" {
		var tokenData []byte
		err := c.db.QueryRowContext(ctx, "SELECT encrypted_credentials FROM tenant_channels WHERE tenant_id = $1 AND platform_name = 'telegram' AND status = 'active' LIMIT 1", task.TenantID).Scan(&tokenData)

		if err != nil {
			status = "failed"
			reason := fmt.Sprintf("failed to find active telegram channel for tenant: %v", err)
			errMsg = &reason
			log.Printf("[❌ TELEGRAM API -> ERROR] %s\n", reason)
		} else {
			// Extract token string
			var creds struct {
				BotToken string `json:"bot_token"`
			}
			if err := json.Unmarshal(tokenData, &creds); err != nil {
				status = "failed"
				reason := "failed to parse telegram token credentials"
				errMsg = &reason
				log.Printf("[❌ TELEGRAM API -> ERROR] %s\n", reason)
			} else {
				// Send to Telegram
				// Format message with personalization
				personalizedMsg := strings.ReplaceAll(task.MessageBody, "{first_name}", task.FirstName)

				tgPayload := map[string]interface{}{
					"chat_id": task.RoutingValue,
					"text":    personalizedMsg,
				}

				payloadBytes, _ := json.Marshal(tgPayload)
				tgURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", creds.BotToken)

				resp, err := http.Post(tgURL, "application/json", bytes.NewBuffer(payloadBytes))
				if err != nil {
					status = "failed"
					reason := fmt.Sprintf("network error calling telegram API: %v", err)
					errMsg = &reason
					log.Printf("[❌ TELEGRAM API -> ERROR] %s\n", reason)
				} else {
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						status = "failed"
						reason := fmt.Sprintf("telegram API rejected message (status %d)", resp.StatusCode)
						errMsg = &reason
						log.Printf("[❌ TELEGRAM API -> ERROR] %s\n", reason)
					} else {
						log.Printf("[📲 TELEGRAM API] Dispatched cleanly to %s (%s)\n", task.FirstName, task.RoutingValue)
					}
				}
			}
		}
	} else {
		// Not telegram, just mock success for now (like WhatsApp)
		time.Sleep(150 * time.Millisecond)
		log.Printf("[📲 %s API] Mock dispatched cleanly to %s (%s)\n", strings.ToUpper(task.TargetPlatform), task.FirstName, task.RoutingValue)
	}

	// Pack the return receipt
	result := contracts.TargetDeliveryResult{
		CampaignID:   task.CampaignID,
		ContactID:    task.ContactID,
		TargetType:   normalizedTargetType(task.TargetType),
		Platform:     task.TargetPlatform,
		RoutingValue: task.RoutingValue,
		Status:       status,
		ErrorMessage: errMsg,
	}

	resultBytes, _ := json.Marshal(result)

	// Publish the return receipt right back onto the message bus stream!
	_, err := c.js.Publish("dispatch.result", resultBytes)
	if err != nil {
		log.Printf("[WORKER-ERROR] Failed to publish return receipt: %v\n", err)
		_ = msg.Nak()
		return
	}

	_ = msg.Ack()
}
func normalizedTargetType(targetType string) string {
	if targetType == "telegram_destination" {
		return targetType
	}
	return "contact"
}
