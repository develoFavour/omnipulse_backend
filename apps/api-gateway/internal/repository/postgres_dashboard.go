package repository

import (
	"context"
	"database/sql"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresDashboardRepository struct {
	db *sql.DB
}

func NewPostgresDashboardRepository(db *sql.DB) domain.DashboardRepository {
	return &PostgresDashboardRepository{db: db}
}

func (r *PostgresDashboardRepository) GetStats(ctx context.Context, tenantID string) (*domain.DashboardStats, error) {
	stats := &domain.DashboardStats{
		ChannelData:      []domain.ChannelStat{},
		RecentDeliveries: []domain.DashboardDeliveryActivity{},
	}

	// 1. Total Audience
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM contacts WHERE tenant_id = $1 AND status = 'active'", tenantID).Scan(&stats.TotalAudience)
	if err != nil {
		return nil, fmt.Errorf("failed to count audience: %v", err)
	}

	// 2. Broadcasts Sent
	err = r.db.QueryRowContext(ctx, "SELECT count(*) FROM campaigns WHERE tenant_id = $1", tenantID).Scan(&stats.BroadcastsSent)
	if err != nil {
		return nil, fmt.Errorf("failed to count campaigns: %v", err)
	}

	// 3. Delivery Stats (Total & Failed)
	// We join with campaigns to ensure we only count deliveries for this tenant.
	deliveryQuery := `
		SELECT 
			count(*) as total,
			sum(case when d.status = 'failed' then 1 else 0 end) as failed
		FROM campaign_deliveries d
		JOIN campaigns c ON d.campaign_id = c.id
		WHERE c.tenant_id = $1
	`
	var total sql.NullInt64
	var failed sql.NullInt64
	err = r.db.QueryRowContext(ctx, deliveryQuery, tenantID).Scan(&total, &failed)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate deliveries: %v", err)
	}

	if total.Valid {
		stats.TotalDeliveries = int(total.Int64)
	}
	if failed.Valid {
		stats.FailedDeliveries = int(failed.Int64)
	}

	if stats.TotalDeliveries > 0 {
		successCount := stats.TotalDeliveries - stats.FailedDeliveries
		stats.DeliveryRate = float64(successCount) / float64(stats.TotalDeliveries) * 100
	} else {
		stats.DeliveryRate = 0
	}

	// 4. Active Channels
	err = r.db.QueryRowContext(ctx, "SELECT count(*) FROM tenant_channels WHERE tenant_id = $1 AND status = 'active'", tenantID).Scan(&stats.ActiveChannels)
	if err != nil {
		return nil, fmt.Errorf("failed to count channels: %v", err)
	}

	// 5. Channel Distribution (Contacts by Channel)
	channelQuery := `
		SELECT channel, count(*) FROM contacts
		WHERE tenant_id = $1 AND status = 'active'
		GROUP BY channel
	`
	rows, err := r.db.QueryContext(ctx, channelQuery, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channel distribution: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var channel string
		var count int
		if err := rows.Scan(&channel, &count); err != nil {
			return nil, err
		}

		color := "#3b82f6" // default
		icon := "✈️"
		if channel == "whatsapp" {
			color = "#22c55e"
			icon = "💬"
		} else if channel == "instagram" {
			color = "#e879f9"
			icon = "📸"
		}

		stats.ChannelData = append(stats.ChannelData, domain.ChannelStat{
			Name:  channel,
			Value: count,
			Color: color,
			Icon:  icon,
		})
	}

	// 6. Recent Deliveries Activity Feed
	recentQuery := `
		SELECT 
			d.id,
			c.title,
			COALESCE(co.first_name, d.routing_value),
			d.platform,
			d.status,
			d.error_message,
			d.created_at
		FROM campaign_deliveries d
		JOIN campaigns c ON d.campaign_id = c.id
		LEFT JOIN contacts co ON d.contact_id = co.id
		WHERE c.tenant_id = $1
		ORDER BY d.created_at DESC
		LIMIT 10
	`
	rRows, err := r.db.QueryContext(ctx, recentQuery, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recent deliveries: %v", err)
	}
	defer rRows.Close()

	for rRows.Next() {
		var act domain.DashboardDeliveryActivity
		var errMsg sql.NullString

		if err := rRows.Scan(&act.ID, &act.CampaignName, &act.ContactName, &act.Platform, &act.Status, &errMsg, &act.CreatedAt); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			act.ErrorMessage = errMsg.String
		}
		stats.RecentDeliveries = append(stats.RecentDeliveries, act)
	}

	return stats, nil
}

func (r *PostgresDashboardRepository) ListDeliveries(ctx context.Context, tenantID string, limit int, offset int) ([]domain.DashboardDeliveryActivity, error) {
	query := `
		SELECT 
			d.id,
			c.title,
			COALESCE(co.first_name, d.routing_value),
			d.platform,
			d.status,
			d.error_message,
			d.created_at
		FROM campaign_deliveries d
		JOIN campaigns c ON d.campaign_id = c.id
		LEFT JOIN contacts co ON d.contact_id = co.id
		WHERE c.tenant_id = $1
		ORDER BY d.created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list deliveries: %v", err)
	}
	defer rows.Close()

	var deliveries []domain.DashboardDeliveryActivity
	for rows.Next() {
		var act domain.DashboardDeliveryActivity
		var errMsg sql.NullString

		if err := rows.Scan(&act.ID, &act.CampaignName, &act.ContactName, &act.Platform, &act.Status, &errMsg, &act.CreatedAt); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			act.ErrorMessage = errMsg.String
		}
		deliveries = append(deliveries, act)
	}

	return deliveries, nil
}
