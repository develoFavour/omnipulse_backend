package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"omnipulse/apps/compliance-engine/internal/domain"

	"github.com/nats-io/nats.go"
)

// TargetDispatchTask is our local copy of the incoming event layout.
// This keeps this microservice completely decoupled from the API Gateway's code.
type TargetDispatchTask struct {
	CampaignID     string  `json:"campaign_id"`
	ContactID      string  `json:"contact_id"`
	FirstName      string  `json:"first_name"`
	TargetPlatform string  `json:"target_platform"`
	RoutingValue   string  `json:"routing_value"`
	MessageBody    string  `json:"message_body"`
	MediaURL       *string `json:"media_url,omitempty"`
}

// CampaignConsumer orchestrates the background message listening loop
type CampaignConsumer struct {
	nc         *nats.Conn
	js         nats.JetStreamContext
	sub        *nats.Subscription
	compliance domain.ComplianceRepository
}

// NewCampaignConsumer instantiates the streaming listener circuit
func NewCampaignConsumer(natsURL string, repo domain.ComplianceRepository) (*CampaignConsumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}

	return &CampaignConsumer{
		nc:         nc,
		js:         js,
		compliance: repo,
	}, nil
}

// Start launches a persistent worker pool subscription loop
func (c *CampaignConsumer) Start(ctx context.Context) error {
	// QueueSubscribe ensures that if we scale up to 5 instances of this worker container,
	// NATS will load-balance the messages evenly across them rather than giving every message to everyone.
	sub, err := c.js.QueueSubscribe(
		"campaign.dispatched",     // The streaming topic we are listening to
		"compliance-engine-group", // The queue group name for structural load balancing
		func(msg *nats.Msg) {
			c.processMessage(ctx, msg)
		},
		nats.ManualAck(), // Don't auto-acknowledge messages; only Ack if we process them successfully!
	)
	if err != nil {
		return err
	}

	c.sub = sub
	log.Println("[WORKER] Compliance Engine actively monitoring NATS campaign streams...")
	return nil
}

// Stop safely unwinds the network bindings during shutdowns
func (c *CampaignConsumer) Stop() {
	if c.sub != nil {
		_ = c.sub.Unsubscribe()
	}
	if c.nc != nil {
		c.nc.Close()
	}
	log.Println("[WORKER] NATS streaming consumer cleanly disconnected.")
}

// Private processing routine running inside our worker pool context
func (c *CampaignConsumer) processMessage(ctx context.Context, msg *nats.Msg) {
	// Set a defensive 5-second processing timeout per message line
	msgCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var task TargetDispatchTask
	if err := json.Unmarshal(msg.Data, &task); err != nil {
		log.Printf("[WORKER-ERROR] Failed to parse message bytes: %v. Dropping garbage message.\n", err)
		_ = msg.Term() // Terminate message: tells NATS to throw it away and never retry it
		return
	}

	// ⚡ CRITICAL RISK EVALUATION BLOCK ⚡
	// Query Redis at sub-millisecond speeds to check if this user has opted out
	isBlacklisted, err := c.compliance.IsOptedOut(msgCtx, task.TargetPlatform, task.RoutingValue)
	if err != nil {
		log.Printf("[WORKER-ERROR] Redis tracking cluster lookup failed for customer %s: %v. Re-queueing message.\n", task.ContactID, err)
		_ = msg.Nak() // Negative Acknowledgment: tells NATS to safely retry this message in a moment
		return
	}

	if isBlacklisted {
		log.Printf("[🛡️ COMPLIANCE BLOCKED] Suppressed message to %s (%s) on %s. Reason: Explicit Opt-Out detected.\n",
			task.FirstName, task.RoutingValue, task.TargetPlatform)

		// Safely acknowledge the message. We successfully defended the platform from sending an illegal spam message!
		_ = msg.Ack()
		return
	}

	// If the user is clean and opted in, we approve it for delivery!
	log.Printf("[✅ COMPLIANCE PASSED] Verified user %s (%s) for channel %s. Routing task forward...\n",
		task.FirstName, task.RoutingValue, task.TargetPlatform)

	// TODO: In the next phase, this line will publish the approved task onto a "campaign.approved" stream
	// where our platform delivery workers (WhatsApp/Telegram outbound nodes) are listening.

	_ = msg.Ack() // Complete the processing lifecycle loop
}
