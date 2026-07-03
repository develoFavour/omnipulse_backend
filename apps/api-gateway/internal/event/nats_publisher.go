package event

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"omnipulse/apps/api-gateway/internal/domain"

	"github.com/nats-io/nats.go"
)

// JetStreamPublisher implements domain.EventPublisher using the native NATS framework
type JetStreamPublisher struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

// NewJetStreamPublisher sets up the connection and provisions the streaming topic boundary
func NewJetStreamPublisher(natsURL string) (domain.EventPublisher, error) {
	// 1. Initialize core TCP connection to the NATS broker cluster
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to NATS network core: %w", err)
	}

	// 2. Bind the connection to the JetStream persistence layer engine
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to initialize NATS JetStream subsystem: %w", err)
	}

	// 3. Declaratively provision the Stream if it does not already exist.
	// This acts exactly like a database schema migration but for our message bus.
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "CAMPAIGNS",
		Subjects: []string{"campaign.dispatched"},
		Storage:  nats.FileStorage, // Guarantees message durability even if NATS crashes or restarts
	})
	if err != nil {
		// If the stream already exists, this command safely skips provisioning
		log.Printf("[NATS-STREAM] Stream metadata validation resolved: %v\n", err)
	}

	return &JetStreamPublisher{nc: nc, js: js}, nil
}

// PublishDispatchTask serializes and drops a single execution instruction onto the message bus
func (p *JetStreamPublisher) PublishDispatchTask(ctx context.Context, task *domain.TargetDispatchTask) error {
	// Transmit raw bytes over the network wire
	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to serialize target task payload: %w", err)
	}

	// Subject-based routing key
	subject := "campaign.dispatched"

	// Publish asynchronously with a context deadline tracking validation check
	_, err = p.js.Publish(subject, payload, nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("nats stream rejected dispatch acknowledgment: %w", err)
	}

	return nil
}
