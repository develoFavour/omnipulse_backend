package worker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"omnipulse/shared/contracts"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/nats-io/nats.go"
)

type BroadcastConsumer struct {
	nc          *nats.Conn
	js          nats.JetStreamContext
	sub         *nats.Subscription
	db          *sql.DB
	waContainer *sqlstore.Container
	waClients   sync.Map // map[string]*whatsmeow.Client (key: jid.String())
}

func NewBroadcastConsumer(natsURL string, natsCreds string, db *sql.DB) (*BroadcastConsumer, error) {
	opts := getNatsOptions(natsCreds)
	nc, err := nats.Connect(natsURL, opts...)
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
		MaxBytes: 10 * 1024 * 1024,
	})
	if err != nil {
		// Safely ignores if already updated
	}

	var waContainer *sqlstore.Container
	if db != nil {
		store.SetOSInfo("Chrome (Windows)", [3]uint32{128, 0, 0})
		waContainer = sqlstore.NewWithDB(db, "postgres", waLog.Stdout("WA-Worker", "WARN", true))
		_ = waContainer.Upgrade(context.Background())
	}

	return &BroadcastConsumer{nc: nc, js: js, db: db, waContainer: waContainer}, nil
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

	if task.TargetPlatform == "telegram" {
		var tokenData []byte
		err := c.db.QueryRowContext(ctx, "SELECT encrypted_credentials FROM tenant_channels WHERE tenant_id = $1 AND platform_name = 'telegram' AND status = 'active' LIMIT 1", task.TenantID).Scan(&tokenData)

		if err != nil {
			status = "failed"
			reason := fmt.Sprintf("failed to find active telegram channel for tenant: %v", err)
			errMsg = &reason
			log.Printf("[❌ TELEGRAM API -> ERROR] %s\n", reason)
		} else {
			var creds struct {
				BotToken string `json:"bot_token"`
			}
			if err := json.Unmarshal(tokenData, &creds); err != nil || creds.BotToken == "" {
				status = "failed"
				reason := "failed to parse telegram token credentials"
				errMsg = &reason
				log.Printf("[❌ TELEGRAM API -> ERROR] %s\n", reason)
			} else {
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
	} else if task.TargetPlatform == "whatsapp" {
		var tokenData []byte
		err := c.db.QueryRowContext(ctx, "SELECT encrypted_credentials FROM tenant_channels WHERE tenant_id = $1 AND platform_name = 'whatsapp' AND status = 'active' LIMIT 1", task.TenantID).Scan(&tokenData)

		if err != nil {
			status = "failed"
			reason := fmt.Sprintf("no active WhatsApp channel configured for tenant: %v", err)
			errMsg = &reason
			log.Printf("[❌ WHATSAPP -> ERROR] %s\n", reason)
		} else {
			var creds map[string]interface{}
			_ = json.Unmarshal(tokenData, &creds)
			personalizedMsg := strings.ReplaceAll(task.MessageBody, "{first_name}", task.FirstName)

			if jidStr, ok := creds["jid"].(string); ok && jidStr != "" && c.waContainer != nil {
				// Send via Whatsmeow Multi-Device session
				jid, parseErr := types.ParseJID(jidStr)
				if parseErr != nil {
					status = "failed"
					reason := fmt.Sprintf("invalid WhatsApp JID format: %v", parseErr)
					errMsg = &reason
					log.Printf("[❌ WHATSAPP MULTI-DEVICE -> ERROR] %s\n", reason)
				} else {
					client, clientErr := c.getOrCreateWhatsAppClient(ctx, jid)
					if clientErr != nil {
						status = "failed"
						reason := fmt.Sprintf("WhatsApp client connection failed: %v", clientErr)
						errMsg = &reason
						log.Printf("[❌ WHATSAPP MULTI-DEVICE -> ERROR] %s\n", reason)
					} else {
						recipientPhone := strings.TrimPrefix(strings.TrimSpace(task.RoutingValue), "+")
						recipientJID := types.NewJID(recipientPhone, types.DefaultUserServer)
						msg := &waE2E.Message{
							Conversation: proto.String(personalizedMsg),
						}
						resp, sendErr := client.SendMessage(ctx, recipientJID, msg)
						if sendErr != nil {
							status = "failed"
							reason := fmt.Sprintf("failed to send WhatsApp message: %v", sendErr)
							errMsg = &reason
							log.Printf("[❌ WHATSAPP MULTI-DEVICE -> ERROR] %s\n", reason)
						} else {
							log.Printf("[📲 WHATSAPP MULTI-DEVICE] Dispatched cleanly to %s (%s) [MsgID: %s]\n", task.FirstName, task.RoutingValue, resp.ID)
						}
					}
				}
			} else {
				// Cloud API Fallback
				phoneID, _ := creds["phone_number_id"].(string)
				token, _ := creds["access_token"].(string)

				if phoneID == "" || token == "" {
					status = "failed"
					reason := "WhatsApp channel credentials missing"
					errMsg = &reason
					log.Printf("[❌ WHATSAPP API -> ERROR] %s\n", reason)
				} else {
					waPayload := map[string]interface{}{
						"messaging_product": "whatsapp",
						"recipient_type":    "individual",
						"to":                task.RoutingValue,
						"type":              "text",
						"text": map[string]interface{}{
							"preview_url": false,
							"body":        personalizedMsg,
						},
					}

					payloadBytes, _ := json.Marshal(waPayload)
					waURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/messages", phoneID)

					req, _ := http.NewRequestWithContext(ctx, "POST", waURL, bytes.NewBuffer(payloadBytes))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer "+token)

					client := &http.Client{Timeout: 10 * time.Second}
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
							reason := fmt.Sprintf("WhatsApp API rejected message (status %d)", resp.StatusCode)
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

	// Publish return receipt onto NATS stream
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

func getNatsOptions(natsCreds string) []nats.Option {
	var opts []nats.Option
	trimmed := strings.TrimSpace(natsCreds)
	trimmed = strings.ReplaceAll(trimmed, "\\n", "\n")
	if trimmed != "" {
		if strings.Contains(trimmed, "-----BEGIN NATS USER JWT-----") {
			tmpFile, err := os.CreateTemp("", "nats-*.creds")
			if err == nil {
				_, _ = tmpFile.WriteString(trimmed)
				_ = tmpFile.Close()
				opts = append(opts, nats.UserCredentials(tmpFile.Name()))
			}
		} else {
			opts = append(opts, nats.UserCredentials(trimmed))
		}
	}
	return opts
}

func (c *BroadcastConsumer) getOrCreateWhatsAppClient(ctx context.Context, jid types.JID) (*whatsmeow.Client, error) {
	jidKey := jid.String()
	if val, ok := c.waClients.Load(jidKey); ok {
		client := val.(*whatsmeow.Client)
		if client.IsConnected() && client.IsLoggedIn() {
			return client, nil
		}
		if !client.IsConnected() {
			_ = client.Connect()
			if client.WaitForConnection(8 * time.Second) {
				return client, nil
			}
		}
	}

	dev, devErr := c.waContainer.GetDevice(ctx, jid)
	if devErr != nil || dev == nil {
		return nil, fmt.Errorf("device session not found in database: %v", devErr)
	}

	client := whatsmeow.NewClient(dev, waLog.Stdout("WA-Worker", "WARN", true))
	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("failed to initiate connect: %w", err)
	}

	if !client.WaitForConnection(10 * time.Second) {
		return nil, fmt.Errorf("timeout waiting for WhatsApp client connection")
	}

	c.waClients.Store(jidKey, client)
	return client, nil
}
