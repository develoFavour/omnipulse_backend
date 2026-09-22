package domain

import (
	"context"
	"time"

	"omnipulse/shared/contracts"
)

// Campaign represents the structural definition of a messaging blast
type Campaign struct {
	ID                             string     `json:"id"`
	TenantID                       string     `json:"tenant_id"`
	Title                          string     `json:"title"`
	MessageBody                    string     `json:"message_body"`
	ExternalTemplateCode           *string    `json:"external_template_code,omitempty"`
	MediaURL                       *string    `json:"media_url,omitempty"`
	DeliveryType                   string     `json:"delivery_type"`
	SelectedChannels               string     `json:"selected_channels"`
	SelectedTelegramDestinationIDs string     `json:"selected_telegram_destination_ids"`
	SelectedContactIDs             string     `json:"selected_contact_ids"`
	Status                         string     `json:"status"`
	ScheduledAt                    *time.Time `json:"scheduled_at,omitempty"`
	TotalTargets                   int        `json:"total_targets"`
	ProcessedTargets               int        `json:"processed_targets"`
	CreatedAt                      time.Time  `json:"created_at"`
	UpdatedAt                      time.Time  `json:"updated_at"`
}

// CampaignStats models real-time execution progress and telemetry for a campaign
type CampaignStats struct {
	CampaignID       string  `json:"campaign_id"`
	Status           string  `json:"status"`
	TotalTargets     int     `json:"total_targets"`
	ProcessedTargets int     `json:"processed_targets"`
	Sent             int     `json:"sent"`
	Delivered        int     `json:"delivered"`
	Failed           int     `json:"failed"`
	ProgressPercent  float64 `json:"progress_percent"`
}

// CampaignRepository handles SQL transactions for orchestrating campaign lifecycle boundaries
type CampaignRepository interface {
	Create(ctx context.Context, campaign *Campaign) error
	GetByID(ctx context.Context, tenantID, id string) (*Campaign, error)
	ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]*Campaign, error)
	UpdateStatus(ctx context.Context, tenantID, id string, status string) error
	SetDispatching(ctx context.Context, tenantID, id string, totalTargets int) error
	SetScheduled(ctx context.Context, tenantID, id string, scheduledAt time.Time) error
	RecordDeliveryResult(ctx context.Context, res *contracts.TargetDeliveryResult) error
	GetCampaignStats(ctx context.Context, tenantID, campaignID string) (*CampaignStats, error)
	ListDeliveriesByCampaign(ctx context.Context, tenantID, campaignID string, limit, offset int) ([]*CampaignDelivery, error)
	FindDueScheduledCampaigns(ctx context.Context) ([]*Campaign, error)
	CancelScheduledCampaign(ctx context.Context, tenantID, id string) error
}

// EventPublisher defines our outbound streaming Port (DIP)
type EventPublisher interface {
	PublishDispatchTask(ctx context.Context, task *contracts.TargetDispatchTask) error
}

// CampaignDelivery represents an individual target execution audit line item
type CampaignDelivery struct {
	ID           string    `json:"id"`
	CampaignID   string    `json:"campaign_id"`
	ContactID    *string   `json:"contact_id,omitempty"`
	TargetType   string    `json:"target_type"`
	Platform     string    `json:"platform"`
	RoutingValue string    `json:"routing_value"`
	Status       string    `json:"status"` // sent, delivered, failed
	ErrorMessage *string   `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
