package domain

import (
	"context"
	"time"
)

// Campaign represents the structural definition of a messaging blast
type Campaign struct {
	ID                   string    `json:"id"`
	Title                string    `json:"title"`
	MessageBody          string    `json:"message_body"`
	ExternalTemplateCode *string   `json:"external_template_code,omitempty"`
	MediaURL             *string   `json:"media_url,omitempty"`
	Status               string    `json:"status"`
	TotalTargets         int       `json:"total_targets"`
	ProcessedTargets     int       `json:"processed_targets"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// TargetDispatchTask represents the thin event payload transmitted over NATS JetStream.
// Keeping this payload lightweight ensures ultra-high throughput across the network wire.
type TargetDispatchTask struct {
	CampaignID     string  `json:"campaign_id"`
	ContactID      string  `json:"contact_id"`
	FirstName      string  `json:"first_name"`
	TargetPlatform string  `json:"target_platform"` // whatsapp, telegram, x
	RoutingValue   string  `json:"routing_value"`   // Phone number, ChatID, or Handle
	MessageBody    string  `json:"message_body"`
	MediaURL       *string `json:"media_url,omitempty"`
}

// CampaignRepository handles SQL transactions for orchestrating campaign lifecycle boundaries
type CampaignRepository interface {
	GetByID(ctx context.Context, id string) (*Campaign, error)
	UpdateStatus(ctx context.Context, id string, status string) error
	RecordDeliveryResult(ctx context.Context, res *TargetDeliveryResult) error
	GetCampaignStats(ctx context.Context, campaignID string) (map[string]int, error)
}

// EventPublisher defines our outbound streaming Port (DIP)
type EventPublisher interface {
	PublishDispatchTask(ctx context.Context, task *TargetDispatchTask) error
}

// CampaignDelivery represents an individual target execution audit line item
type CampaignDelivery struct {
	ID           string    `json:"id"`
	CampaignID   string    `json:"campaign_id"`
	ContactID    string    `json:"contact_id"`
	Platform     string    `json:"platform"`
	RoutingValue string    `json:"routing_value"`
	Status       string    `json:"status"` // sent, delivered, failed
	ErrorMessage *string   `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// TargetDeliveryResult represents the event payload streaming back from workers
type TargetDeliveryResult struct {
	CampaignID   string  `json:"campaign_id"`
	ContactID    string  `json:"contact_id"`
	Platform     string  `json:"platform"`
	RoutingValue string  `json:"routing_value"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message,omitempty"`
}

// Update the CampaignRepository interface definition to include our new telemetry methods:
