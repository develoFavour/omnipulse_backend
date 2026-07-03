package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"

	"github.com/nats-io/nats.go"
)

// TelemetryConsumer listens for delivery receipts coming back from outbound networks
type TelemetryConsumer struct {
	nc   *nats.Conn
	js   nats.JetStreamContext
	sub  *nats.Subscription
	repo domain.CampaignRepository
}

// NewTelemetryConsumer initializes the background database-writer event node
func NewTelemetryConsumer(natsURL string, repo domain.CampaignRepository) (*TelemetryConsumer, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}

	return &TelemetryConsumer{
		nc:   nc,
		js:   js,
		repo: repo,
	}, nil
}

// Start hooks the worker up to the live NATS dispatch stream
func (c *TelemetryConsumer) Start(ctx context.Context) error {
	sub, err := c.js.QueueSubscribe(
		"dispatch.result",
		"telemetry-gateway-group", // Shared group ensuring singular processing line reads
		func(msg *nats.Msg) {
			c.processReceipt(ctx, msg)
		},
		nats.ManualAck(),
	)
	if err != nil {
		return err
	}

	c.sub = sub
	log.Println("[TELEMETRY-WORKER] Relational audit processor active and listening to delivery metrics...")
	return nil
}

// Stop safely cuts network stream attachments during server termination
func (c *TelemetryConsumer) Stop() {
	if c.sub != nil {
		_ = c.sub.Unsubscribe()
	}
	if c.nc != nil {
		c.nc.Close()
	}
	log.Println("[TELEMETRY-WORKER] Relational audit processor cleanly disconnected.")
}

func (c *TelemetryConsumer) processReceipt(ctx context.Context, msg *nats.Msg) {
	// Allocate a strict execution window per database entry write
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var result domain.TargetDeliveryResult
	if err := json.Unmarshal(msg.Data, &result); err != nil {
		log.Printf("[TELEMETRY-ERROR] Corrupted return receipt payload: %v\n", err)
		_ = msg.Term()
		return
	}

	// Persist the transaction line record into PostgreSQL
	err := c.repo.RecordDeliveryResult(dbCtx, &result)
	if err != nil {
		log.Printf("[TELEMETRY-ERROR] Transaction failed to commit to SQL ledger: %v. Retrying stream...\n", err)
		_ = msg.Nak()
		return
	}

	// Acknowledge receipt: Safe from the message queue queue loop!
	_ = msg.Ack()
}
