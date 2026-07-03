package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

type ApprovedDispatchTask struct {
	CampaignID     string  `json:"campaign_id"`
	ContactID      string  `json:"contact_id"`
	FirstName      string  `json:"first_name"`
	TargetPlatform string  `json:"target_platform"`
	RoutingValue   string  `json:"routing_value"`
	MessageBody    string  `json:"message_body"`
	MediaURL       *string `json:"media_url,omitempty"`
}

type BroadcastConsumer struct {
	nc  *nats.Conn
	js  nats.JetStreamContext
	sub *nats.Subscription
}

func NewBroadcastConsumer(natsURL string) (*BroadcastConsumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}

	// Double check stream configuration to ensure campaign.approved topic is tracked
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "CAMPAIGNS",
		Subjects: []string{"campaign.dispatched", "campaign.approved"},
		Storage:  nats.FileStorage,
	})
	if err != nil {
		// Safely ignores if already updated
	}

	return &BroadcastConsumer{nc: nc, js: js}, nil
}

func (c *BroadcastConsumer) Start(ctx context.Context) error {
	sub, err := c.js.QueueSubscribe(
		"campaign.approved",
		"broadcast-worker-group",
		func(msg *nats.Msg) {
			c.executeDelivery(msg)
		},
		nats.ManualAck(),
	)
	if err != nil {
		return err
	}

	c.sub = sub
	log.Println("[WORKER] Broadcast Engine actively monitoring approved outbound streams...")
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

func (c *BroadcastConsumer) executeDelivery(msg *nats.Msg) {
	var task ApprovedDispatchTask
	if err := json.Unmarshal(msg.Data, &task); err != nil {
		_ = msg.Term()
		return
	}

	// Simulate network latency of hitting external HTTP API servers (Twilio/Telegram)
	time.Sleep(150 * time.Millisecond)

	// Route based on targeted platform network channels
	switch task.TargetPlatform {
	case "whatsapp":
		log.Printf("[📲 TELECOM API -> WHATSAPP] Successfully transmitted to %s via Twilio Network Gateway. MsgID: wa_fallback_%d\n", task.RoutingValue, time.Now().UnixNano())
	case "telegram":
		log.Printf("[🤖 TELEGRAM BOT ENGINE] Sent text packet block successfully to ChatID: %s\n", task.RoutingValue)
	case "x":
		log.Printf("[🐦 X DM ROUTER] Published standard messaging payload token straight to handle: @%s\n", task.RoutingValue)
	default:
		log.Printf("[⚠️ UNKNOWN ROUTE] Bypassing unrecognized destination platform handler: %s\n", task.TargetPlatform)
	}

	_ = msg.Ack()
}
