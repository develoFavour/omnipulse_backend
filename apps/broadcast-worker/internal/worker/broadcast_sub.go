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
type TargetDeliveryResult struct {
	CampaignID   string  `json:"campaign_id"`
	ContactID    string  `json:"contact_id"`
	Platform     string  `json:"platform"`
	RoutingValue string  `json:"routing_value"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message,omitempty"`
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
		Subjects: []string{"campaign.dispatched", "campaign.approved", "dispatch.result"},
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

	time.Sleep(150 * time.Millisecond) // Simulate latency

	// Default state status values
	status := "delivered"
	var errMsg *string

	// For testing purposes, let's randomly simulate a failure if the name is "Charlie"
	// so we can witness our error metrics dashboard capturing faults realistically!
	if task.FirstName == "Charlie" {
		status = "failed"
		reason := "Telegram Network Timeout Protocol Code 503"
		errMsg = &reason
		log.Printf("[❌ TELECOM API -> ERROR] Failed delivery to %s on %s: %s\n", task.FirstName, task.TargetPlatform, reason)
	} else {
		log.Printf("[📲 TELECOM API] Dispatched cleanly to %s (%s) on channel %s\n", task.FirstName, task.RoutingValue, task.TargetPlatform)
	}

	// Pack the return receipt
	result := TargetDeliveryResult{
		CampaignID:   task.CampaignID,
		ContactID:    task.ContactID,
		Platform:     task.TargetPlatform,
		RoutingValue: task.RoutingValue,
		Status:       status,
		ErrorMessage: errMsg,
	}

	payload, _ := json.Marshal(result)

	// 🆕 Publish the return receipt right back onto the message bus stream!
	_, err := c.js.Publish("dispatch.result", payload)
	if err != nil {
		log.Printf("[WORKER-ERROR] Failed to publish return receipt: %v\n", err)
		_ = msg.Nak()
		return
	}

	_ = msg.Ack()
}
