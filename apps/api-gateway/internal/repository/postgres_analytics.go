package repository

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresAnalyticsRepository struct {
	db *sql.DB
}

func NewPostgresAnalyticsRepository(db *sql.DB) domain.AnalyticsRepository {
	return &PostgresAnalyticsRepository{db: db}
}

func (r *PostgresAnalyticsRepository) GetAggregateReport(ctx context.Context, tenantID string, days int) (*domain.AggregateAnalyticsReport, error) {
	if days <= 0 || days > 365 {
		days = 30
	}

	now := time.Now().UTC()
	startDate := now.AddDate(0, 0, -days)

	report := &domain.AggregateAnalyticsReport{
		Days:               days,
		StartDate:          startDate,
		EndDate:            now,
		DailyTrend:         make([]domain.DailyDeliveryPoint, 0),
		PlatformBreakdown:  make([]domain.PlatformAnalytics, 0),
		HourlyDistribution: make([]domain.HourlyPerformance, 0),
		TopCampaigns:       make([]domain.TopCampaignMetric, 0),
	}

	// 1. Current Period Overview
	overviewQuery := `
		SELECT 
			COALESCE(COUNT(*), 0) AS total_sent,
			COALESCE(SUM(CASE WHEN d.status IN ('delivered', 'sent') THEN 1 ELSE 0 END), 0) AS total_delivered,
			COALESCE(SUM(CASE WHEN d.status = 'failed' THEN 1 ELSE 0 END), 0) AS total_failed,
			COALESCE(COUNT(DISTINCT d.campaign_id), 0) AS campaigns_count,
			COALESCE(COUNT(DISTINCT d.routing_value), 0) AS audience_reach
		FROM campaign_deliveries d
		JOIN campaigns c ON c.id = d.campaign_id
		WHERE c.tenant_id = $1 AND d.created_at >= $2;
	`
	err := r.db.QueryRowContext(ctx, overviewQuery, tenantID, startDate).Scan(
		&report.Overview.TotalSent,
		&report.Overview.TotalDelivered,
		&report.Overview.TotalFailed,
		&report.Overview.CampaignsCount,
		&report.Overview.TotalAudienceReach,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to fetch analytics overview: %w", err)
	}

	if report.Overview.TotalSent > 0 {
		rate := (float64(report.Overview.TotalDelivered) / float64(report.Overview.TotalSent)) * 100.0
		report.Overview.DeliveryRate = math.Round(rate*10) / 10
	}

	// 2. Previous Period Overview (for growth calculations)
	prevStartDate := startDate.AddDate(0, 0, -days)
	prevQuery := `
		SELECT 
			COALESCE(COUNT(*), 0) AS prev_sent,
			COALESCE(SUM(CASE WHEN d.status IN ('delivered', 'sent') THEN 1 ELSE 0 END), 0) AS prev_delivered
		FROM campaign_deliveries d
		JOIN campaigns c ON c.id = d.campaign_id
		WHERE c.tenant_id = $1 AND d.created_at >= $2 AND d.created_at < $3;
	`
	var prevSent, prevDelivered int
	_ = r.db.QueryRowContext(ctx, prevQuery, tenantID, prevStartDate, startDate).Scan(&prevSent, &prevDelivered)

	if prevSent > 0 {
		growth := ((float64(report.Overview.TotalSent) - float64(prevSent)) / float64(prevSent)) * 100.0
		report.Overview.PeriodGrowthSent = math.Round(growth*10) / 10

		prevRate := (float64(prevDelivered) / float64(prevSent)) * 100.0
		report.Overview.PeriodGrowthRate = math.Round((report.Overview.DeliveryRate-prevRate)*10) / 10
	} else if report.Overview.TotalSent > 0 {
		report.Overview.PeriodGrowthSent = 100.0
		report.Overview.PeriodGrowthRate = report.Overview.DeliveryRate
	}

	// 3. Daily Delivery Trend (Generate continuous series across all days in range)
	trendQuery := `
		WITH date_series AS (
			SELECT generate_series(
				$2::DATE,
				$3::DATE,
				'1 day'::INTERVAL
			)::DATE AS day
		)
		SELECT 
			TO_CHAR(ds.day, 'YYYY-MM-DD') AS date_str,
			COALESCE(COUNT(d.id), 0) AS sent,
			COALESCE(COUNT(d.id) FILTER (WHERE d.status IN ('delivered', 'sent')), 0) AS delivered,
			COALESCE(COUNT(d.id) FILTER (WHERE d.status = 'failed'), 0) AS failed
		FROM date_series ds
		LEFT JOIN (
			campaign_deliveries d
			JOIN campaigns c ON c.id = d.campaign_id AND c.tenant_id = $1
		) ON d.created_at::DATE = ds.day
		GROUP BY ds.day
		ORDER BY ds.day ASC;
	`
	trendRows, err := r.db.QueryContext(ctx, trendQuery, tenantID, startDate, now)
	if err == nil {
		defer trendRows.Close()
		for trendRows.Next() {
			var p domain.DailyDeliveryPoint
			if scanErr := trendRows.Scan(&p.Date, &p.Sent, &p.Delivered, &p.Failed); scanErr == nil {
				if p.Sent > 0 {
					p.DeliveryRate = math.Round((float64(p.Delivered)/float64(p.Sent))*1000) / 10
				}
				report.DailyTrend = append(report.DailyTrend, p)
			}
		}
	}

	// 4. Platform Breakdown
	platformQuery := `
		SELECT 
			d.platform,
			COUNT(*) AS total,
			COALESCE(COUNT(*) FILTER (WHERE d.status IN ('delivered', 'sent')), 0) AS delivered,
			COALESCE(COUNT(*) FILTER (WHERE d.status = 'failed'), 0) AS failed
		FROM campaign_deliveries d
		JOIN campaigns c ON c.id = d.campaign_id
		WHERE c.tenant_id = $1 AND d.created_at >= $2
		GROUP BY d.platform
		ORDER BY total DESC;
	`
	platRows, err := r.db.QueryContext(ctx, platformQuery, tenantID, startDate)
	if err == nil {
		defer platRows.Close()
		for platRows.Next() {
			var p domain.PlatformAnalytics
			if scanErr := platRows.Scan(&p.Platform, &p.Total, &p.Delivered, &p.Failed); scanErr == nil {
				if p.Total > 0 {
					p.DeliveryRate = math.Round((float64(p.Delivered)/float64(p.Total))*1000) / 10
				}
				if report.Overview.TotalSent > 0 {
					p.SharePercentage = math.Round((float64(p.Total)/float64(report.Overview.TotalSent))*1000) / 10
				}
				report.PlatformBreakdown = append(report.PlatformBreakdown, p)
			}
		}
	}

	// 5. Hourly Performance Distribution (0 to 23 hours)
	hourlyMap := make(map[int]domain.HourlyPerformance)
	for h := 0; h < 24; h++ {
		hourlyMap[h] = domain.HourlyPerformance{Hour: h, Total: 0, Delivered: 0, DeliveryRate: 0}
	}

	hourlyQuery := `
		SELECT 
			EXTRACT(HOUR FROM d.created_at)::INT AS hr,
			COUNT(*) AS total,
			COALESCE(COUNT(*) FILTER (WHERE d.status IN ('delivered', 'sent')), 0) AS delivered
		FROM campaign_deliveries d
		JOIN campaigns c ON c.id = d.campaign_id
		WHERE c.tenant_id = $1 AND d.created_at >= $2
		GROUP BY hr
		ORDER BY hr ASC;
	`
	hourlyRows, err := r.db.QueryContext(ctx, hourlyQuery, tenantID, startDate)
	if err == nil {
		defer hourlyRows.Close()
		for hourlyRows.Next() {
			var hr, total, delivered int
			if scanErr := hourlyRows.Scan(&hr, &total, &delivered); scanErr == nil && hr >= 0 && hr < 24 {
				rate := 0.0
				if total > 0 {
					rate = math.Round((float64(delivered)/float64(total))*1000) / 10
				}
				hourlyMap[hr] = domain.HourlyPerformance{
					Hour:         hr,
					Total:        total,
					Delivered:    delivered,
					DeliveryRate: rate,
				}
			}
		}
	}
	for h := 0; h < 24; h++ {
		report.HourlyDistribution = append(report.HourlyDistribution, hourlyMap[h])
	}

	// 6. Top Campaigns in Period
	topCampQuery := `
		SELECT 
			c.id,
			c.title,
			c.total_targets,
			c.status,
			c.created_at,
			COALESCE(COUNT(d.id) FILTER (WHERE d.status IN ('delivered', 'sent')), 0) AS delivered,
			COALESCE(COUNT(d.id) FILTER (WHERE d.status = 'failed'), 0) AS failed
		FROM campaigns c
		LEFT JOIN campaign_deliveries d ON d.campaign_id = c.id
		WHERE c.tenant_id = $1 AND c.created_at >= $2
		GROUP BY c.id, c.title, c.total_targets, c.status, c.created_at
		ORDER BY c.created_at DESC
		LIMIT 10;
	`
	campRows, err := r.db.QueryContext(ctx, topCampQuery, tenantID, startDate)
	if err == nil {
		defer campRows.Close()
		for campRows.Next() {
			var tc domain.TopCampaignMetric
			if scanErr := campRows.Scan(&tc.ID, &tc.Title, &tc.TotalTargets, &tc.Status, &tc.CreatedAt, &tc.Delivered, &tc.Failed); scanErr == nil {
				total := tc.Delivered + tc.Failed
				if total > 0 {
					tc.DeliveryRate = math.Round((float64(tc.Delivered)/float64(total))*1000) / 10
				} else if tc.Status == "completed" {
					tc.DeliveryRate = 100.0
				}
				report.TopCampaigns = append(report.TopCampaigns, tc)
			}
		}
	}

	return report, nil
}
