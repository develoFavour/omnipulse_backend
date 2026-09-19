package domain

import (
	"context"
	"time"
)

type LatestCampaignInfo struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Status           string    `json:"status"`
	TotalTargets     int       `json:"total_targets"`
	ProcessedTargets int       `json:"processed_targets"`
	DeliveredCount   int       `json:"delivered_count"`
	FailedCount      int       `json:"failed_count"`
	DeliveryRate     float64   `json:"delivery_rate"`
	CreatedAt        time.Time `json:"created_at"`
}

type AudienceHealthInfo struct {
	TotalContacts       int     `json:"total_contacts"`
	ActiveContacts      int     `json:"active_contacts"`
	OptOutCount         int     `json:"opt_out_count"`
	OptOutRate          float64 `json:"opt_out_rate"`
	NewContactsThisWeek int     `json:"new_contacts_this_week"`
	Status              string  `json:"status"` // Optimal, Healthy, Attention
}

type OnboardingProgressInfo struct {
	WorkspaceCreated     bool `json:"workspace_created"`
	ChannelsConnected    bool `json:"channels_connected"`
	ContactsImported     bool `json:"contacts_imported"`
	FirstBroadcastSent   bool `json:"first_broadcast_sent"`
	CompletionPercentage int  `json:"completion_percentage"`
	CompletedSteps       int  `json:"completed_steps"`
	TotalSteps           int  `json:"total_steps"`
}

type PlanUsageInfo struct {
	PlanTier              string `json:"plan_tier"`
	PlanBadge             string `json:"plan_badge"`
	MonthlyMessageLimit   int    `json:"monthly_message_limit"` // -1 for unlimited
	MessagesSentThisMonth int    `json:"messages_sent_this_month"`
	ContactsStored        int    `json:"contacts_stored"`
	ContactsLimit         int    `json:"contacts_limit"` // -1 for unlimited
	ChannelsConnected     int    `json:"channels_connected"`
	ChannelsLimit         int    `json:"channels_limit"` // -1 for unlimited
	IsUnlimited           bool   `json:"is_unlimited"`
}

type DashboardStats struct {
	TotalAudience      int                         `json:"total_audience"`
	BroadcastsSent     int                         `json:"broadcasts_sent"`
	DeliveryRate       float64                     `json:"delivery_rate"`
	TotalDeliveries    int                         `json:"total_deliveries"`
	FailedDeliveries   int                         `json:"failed_deliveries"`
	ActiveChannels     int                         `json:"active_channels"`
	ChannelData        []ChannelStat               `json:"channel_data"`
	RecentDeliveries   []DashboardDeliveryActivity `json:"recent_activities"`
	LatestCampaign     *LatestCampaignInfo         `json:"latest_campaign,omitempty"`
	AudienceHealth     AudienceHealthInfo          `json:"audience_health"`
	OnboardingProgress OnboardingProgressInfo      `json:"onboarding_progress"`
	PlanUsage          PlanUsageInfo               `json:"plan_usage"`
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
	Status       string    `json:"status"` // delivered, failed, sent
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type DashboardRepository interface {
	GetStats(ctx context.Context, tenantID string) (*DashboardStats, error)
	ListDeliveries(ctx context.Context, tenantID string, limit int, offset int) ([]DashboardDeliveryActivity, error)
}
