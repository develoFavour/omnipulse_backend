package domain

import (
	"context"
	"time"
)

type DashboardStats struct {
	TotalAudience    int                         `json:"total_audience"`
	BroadcastsSent   int                         `json:"broadcasts_sent"`
	DeliveryRate     float64                     `json:"delivery_rate"`
	TotalDeliveries  int                         `json:"total_deliveries"`
	FailedDeliveries int                         `json:"failed_deliveries"` // Included per user request
	ActiveChannels   int                         `json:"active_channels"`
	ChannelData      []ChannelStat               `json:"channel_data"`
	RecentDeliveries []DashboardDeliveryActivity `json:"recent_activities"`
}

type ChannelStat struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
	Color string `json:"color"`
	Icon  string `json:"icon"`
}

type DashboardDeliveryActivity struct {
	ID           string    `json:"id"`
	CampaignName string    `json:"campaign_name"`
	ContactName  string    `json:"contact_name"`
	Platform     string    `json:"platform"`
	Status       string    `json:"status"` // delivered, failed
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type DashboardRepository interface {
	GetStats(ctx context.Context, tenantID string) (*DashboardStats, error)
	ListDeliveries(ctx context.Context, tenantID string, limit int, offset int) ([]DashboardDeliveryActivity, error)
}
