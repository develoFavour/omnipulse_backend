package domain

import (
	"context"
	"time"
)

// AnalyticsOverview provides high-level KPIs for the chosen time range
type AnalyticsOverview struct {
	TotalSent          int     `json:"total_sent"`
	TotalDelivered     int     `json:"total_delivered"`
	TotalFailed        int     `json:"total_failed"`
	DeliveryRate       float64 `json:"delivery_rate"`
	CampaignsCount     int     `json:"campaigns_count"`
	TotalAudienceReach int     `json:"total_audience_reach"`
	PeriodGrowthSent   float64 `json:"period_growth_sent"` // % change vs previous equivalent window
	PeriodGrowthRate   float64 `json:"period_growth_rate"` // delivery rate diff (e.g. +2.4%)
}

// DailyDeliveryPoint represents one day in the time-series chart
type DailyDeliveryPoint struct {
	Date         string  `json:"date"` // "YYYY-MM-DD"
	Sent         int     `json:"sent"`
	Delivered    int     `json:"delivered"`
	Failed       int     `json:"failed"`
	DeliveryRate float64 `json:"delivery_rate"`
}

// PlatformAnalytics breaks down delivery volume and reliability by platform
type PlatformAnalytics struct {
	Platform        string  `json:"platform"` // "whatsapp", "telegram"
	Total           int     `json:"total"`
	Delivered       int     `json:"delivered"`
	Failed          int     `json:"failed"`
	DeliveryRate    float64 `json:"delivery_rate"`
	SharePercentage float64 `json:"share_percentage"`
}

// HourlyPerformance captures dispatch volume and deliverability across hours (0-23)
type HourlyPerformance struct {
	Hour         int     `json:"hour"` // 0 to 23
	Total        int     `json:"total"`
	Delivered    int     `json:"delivered"`
	DeliveryRate float64 `json:"delivery_rate"`
}

// TopCampaignMetric highlights top campaigns in the period
type TopCampaignMetric struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	TotalTargets int       `json:"total_targets"`
	Delivered    int       `json:"delivered"`
	Failed       int       `json:"failed"`
	DeliveryRate float64   `json:"delivery_rate"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// AggregateAnalyticsReport is the complete reporting payload for the analytics page
type AggregateAnalyticsReport struct {
	Days               int                  `json:"days"`
	StartDate          time.Time            `json:"start_date"`
	EndDate            time.Time            `json:"end_date"`
	Overview           AnalyticsOverview    `json:"overview"`
	DailyTrend         []DailyDeliveryPoint `json:"daily_trend"`
	PlatformBreakdown  []PlatformAnalytics  `json:"platform_breakdown"`
	HourlyDistribution []HourlyPerformance  `json:"hourly_distribution"`
	TopCampaigns       []TopCampaignMetric  `json:"top_campaigns"`
}

// AnalyticsRepository abstracts SQL queries for analytics
type AnalyticsRepository interface {
	GetAggregateReport(ctx context.Context, tenantID string, days int) (*AggregateAnalyticsReport, error)
}

// AnalyticsUseCase provides business logic for analytics
type AnalyticsUseCase interface {
	GetReport(ctx context.Context, tenantID string, days int) (*AggregateAnalyticsReport, error)
}
