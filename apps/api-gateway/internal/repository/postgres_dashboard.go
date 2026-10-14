package repository

import (
	"context"
	"database/sql"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
	"strings"
	"time"
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
		return nil, fmt.Errorf("failed to count audience: %w", err)
	}

	// 2. Broadcasts Sent
	err = r.db.QueryRowContext(ctx, "SELECT count(*) FROM campaigns WHERE tenant_id = $1", tenantID).Scan(&stats.BroadcastsSent)
	if err != nil {
		return nil, fmt.Errorf("failed to count campaigns: %w", err)
	}

	// 3. Delivery Stats (Total & Failed)
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
		return nil, fmt.Errorf("failed to aggregate deliveries: %w", err)
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
		return nil, fmt.Errorf("failed to count channels: %w", err)
	}

	// 5. Channel Distribution (Contacts by Channel)
	channelQuery := `
		SELECT channel, count(*) FROM contacts
		WHERE tenant_id = $1 AND status = 'active'
		GROUP BY channel
	`
	rows, err := r.db.QueryContext(ctx, channelQuery, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channel distribution: %w", err)
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
			d.created_at,
			d.routing_value,
			c.id,
			d.target_type
		FROM campaign_deliveries d
		JOIN campaigns c ON d.campaign_id = c.id
		LEFT JOIN contacts co ON d.contact_id = co.id
		WHERE c.tenant_id = $1
		ORDER BY d.created_at DESC
		LIMIT 10
	`
	rRows, err := r.db.QueryContext(ctx, recentQuery, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recent deliveries: %w", err)
	}
	defer rRows.Close()

	for rRows.Next() {
		var act domain.DashboardDeliveryActivity
		var errMsg, routingVal, campID, targetType sql.NullString

		if err := rRows.Scan(
			&act.ID, &act.CampaignName, &act.ContactName, &act.Platform, &act.Status, &errMsg, &act.CreatedAt,
			&routingVal, &campID, &targetType,
		); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			act.ErrorMessage = errMsg.String
		}
		if routingVal.Valid {
			act.RoutingValue = routingVal.String
		}
		if campID.Valid {
			act.CampaignID = campID.String
		}
		if targetType.Valid {
			act.TargetType = targetType.String
		}
		stats.RecentDeliveries = append(stats.RecentDeliveries, act)
	}

	// 7. Latest Campaign Stats
	latestCampQuery := `
		SELECT id, title, status, total_targets, processed_targets, created_at
		FROM campaigns
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`
	var campID, campTitle, campStatus string
	var totalTargets, processedTargets int
	var campCreatedAt time.Time
	err = r.db.QueryRowContext(ctx, latestCampQuery, tenantID).Scan(
		&campID, &campTitle, &campStatus, &totalTargets, &processedTargets, &campCreatedAt,
	)
	if err == nil {
		lc := &domain.LatestCampaignInfo{
			ID:               campID,
			Title:            campTitle,
			Status:           campStatus,
			TotalTargets:     totalTargets,
			ProcessedTargets: processedTargets,
			CreatedAt:        campCreatedAt,
		}

		// Count deliveries for this campaign
		var delCount, failCount sql.NullInt64
		_ = r.db.QueryRowContext(ctx, `
			SELECT 
				sum(case when status in ('delivered', 'sent') then 1 else 0 end),
				sum(case when status = 'failed' then 1 else 0 end)
			FROM campaign_deliveries
			WHERE campaign_id = $1
		`, campID).Scan(&delCount, &failCount)

		if delCount.Valid {
			lc.DeliveredCount = int(delCount.Int64)
		}
		if failCount.Valid {
			lc.FailedCount = int(failCount.Int64)
		}

		if lc.TotalTargets > 0 {
			lc.DeliveryRate = float64(lc.DeliveredCount) / float64(lc.TotalTargets) * 100
		} else if lc.DeliveredCount+lc.FailedCount > 0 {
			lc.DeliveryRate = float64(lc.DeliveredCount) / float64(lc.DeliveredCount+lc.FailedCount) * 100
		}

		stats.LatestCampaign = lc
	}

	// 8. Audience Health Stats
	healthQuery := `
		SELECT 
			count(*) as total,
			sum(case when status = 'active' then 1 else 0 end) as active,
			sum(case when status = 'opted_out' then 1 else 0 end) as opted_out,
			sum(case when created_at >= NOW() - INTERVAL '7 days' then 1 else 0 end) as new_7d
		FROM contacts
		WHERE tenant_id = $1
	`
	var hTotal, hActive, hOptOut, hNew7d sql.NullInt64
	err = r.db.QueryRowContext(ctx, healthQuery, tenantID).Scan(&hTotal, &hActive, &hOptOut, &hNew7d)
	if err == nil {
		if hTotal.Valid {
			stats.AudienceHealth.TotalContacts = int(hTotal.Int64)
		}
		if hActive.Valid {
			stats.AudienceHealth.ActiveContacts = int(hActive.Int64)
		}
		if hOptOut.Valid {
			stats.AudienceHealth.OptOutCount = int(hOptOut.Int64)
		}
		if hNew7d.Valid {
			stats.AudienceHealth.NewContactsThisWeek = int(hNew7d.Int64)
		}

		if stats.AudienceHealth.TotalContacts > 0 {
			stats.AudienceHealth.OptOutRate = float64(stats.AudienceHealth.OptOutCount) / float64(stats.AudienceHealth.TotalContacts) * 100
		}

		if stats.AudienceHealth.OptOutRate <= 2.0 {
			stats.AudienceHealth.Status = "Optimal"
		} else if stats.AudienceHealth.OptOutRate <= 5.0 {
			stats.AudienceHealth.Status = "Healthy"
		} else {
			stats.AudienceHealth.Status = "Attention"
		}
	}

	// 9. Onboarding Progress
	completedSteps := 1 // Step 1: Workspace created (always true)
	stats.OnboardingProgress.WorkspaceCreated = true

	if stats.ActiveChannels > 0 {
		stats.OnboardingProgress.ChannelsConnected = true
		completedSteps++
	}
	if stats.TotalAudience > 0 {
		stats.OnboardingProgress.ContactsImported = true
		completedSteps++
	}
	if stats.BroadcastsSent > 0 {
		stats.OnboardingProgress.FirstBroadcastSent = true
		completedSteps++
	}

	stats.OnboardingProgress.CompletedSteps = completedSteps
	stats.OnboardingProgress.TotalSteps = 4
	stats.OnboardingProgress.CompletionPercentage = completedSteps * 25

	// 10. Plan Usage
	var monthlyMsgs sql.NullInt64
	_ = r.db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM campaign_deliveries d
		JOIN campaigns c ON d.campaign_id = c.id
		WHERE c.tenant_id = $1 AND d.created_at >= date_trunc('month', CURRENT_TIMESTAMP)
	`, tenantID).Scan(&monthlyMsgs)

	msgsThisMonth := 0
	if monthlyMsgs.Valid {
		msgsThisMonth = int(monthlyMsgs.Int64)
	}

	var planTier sql.NullString
	var msgLimit, contactLimit, channelLimit sql.NullInt64
	_ = r.db.QueryRowContext(ctx, `
		SELECT plan_tier, monthly_message_limit, contacts_limit, channels_limit
		FROM subscriptions
		WHERE tenant_id = $1
	`, tenantID).Scan(&planTier, &msgLimit, &contactLimit, &channelLimit)

	tier := "starter"
	if planTier.Valid && planTier.String != "" {
		tier = planTier.String
	}

	mLimit := 1000
	cLimit := 500
	chLimit := 2

	if msgLimit.Valid {
		mLimit = int(msgLimit.Int64)
	}
	if contactLimit.Valid {
		cLimit = int(contactLimit.Int64)
	}
	if channelLimit.Valid {
		chLimit = int(channelLimit.Int64)
	}

	badge := strings.ToUpper(tier)
	isUnl := mLimit == -1 && cLimit == -1 && chLimit == -1

	stats.PlanUsage = domain.PlanUsageInfo{
		PlanTier:              tier,
		PlanBadge:             badge,
		MonthlyMessageLimit:   mLimit,
		MessagesSentThisMonth: msgsThisMonth,
		ContactsStored:        stats.TotalAudience,
		ContactsLimit:         cLimit,
		ChannelsConnected:     stats.ActiveChannels,
		ChannelsLimit:         chLimit,
		IsUnlimited:           isUnl,
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
			d.created_at,
			d.routing_value,
			c.id,
			d.target_type
		FROM campaign_deliveries d
		JOIN campaigns c ON d.campaign_id = c.id
		LEFT JOIN contacts co ON d.contact_id = co.id
		WHERE c.tenant_id = $1
		ORDER BY d.created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list deliveries: %w", err)
	}
	defer rows.Close()

	var deliveries []domain.DashboardDeliveryActivity
	for rows.Next() {
		var act domain.DashboardDeliveryActivity
		var errMsg, routingVal, campID, targetType sql.NullString

		if err := rows.Scan(
			&act.ID, &act.CampaignName, &act.ContactName, &act.Platform, &act.Status, &errMsg, &act.CreatedAt,
			&routingVal, &campID, &targetType,
		); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			act.ErrorMessage = errMsg.String
		}
		if routingVal.Valid {
			act.RoutingValue = routingVal.String
		}
		if campID.Valid {
			act.CampaignID = campID.String
		}
		if targetType.Valid {
			act.TargetType = targetType.String
		}
		deliveries = append(deliveries, act)
	}

	return deliveries, nil
}

func (r *PostgresDashboardRepository) StreamDeliveries(ctx context.Context, tenantID string, statusFilter, platformFilter, search string, yield func(act *domain.DashboardDeliveryActivity) error) error {
	conditions := []string{"c.tenant_id = $1"}
	args := []interface{}{tenantID}
	argIdx := 2

	if statusFilter != "" && !strings.EqualFold(statusFilter, "all") {
		if strings.EqualFold(statusFilter, "delivered") {
			conditions = append(conditions, "(d.status = 'delivered' OR d.status = 'sent')")
		} else if strings.EqualFold(statusFilter, "failed") {
			conditions = append(conditions, "d.status = 'failed'")
		} else {
			conditions = append(conditions, fmt.Sprintf("d.status = $%d", argIdx))
			args = append(args, strings.ToLower(statusFilter))
			argIdx++
		}
	}

	if platformFilter != "" && !strings.EqualFold(platformFilter, "all") {
		conditions = append(conditions, fmt.Sprintf("d.platform = $%d", argIdx))
		args = append(args, strings.ToLower(platformFilter))
		argIdx++
	}

	if strings.TrimSpace(search) != "" {
		searchPattern := "%" + strings.TrimSpace(search) + "%"
		conditions = append(conditions, fmt.Sprintf(`(
			c.title ILIKE $%d
			OR d.routing_value ILIKE $%d
			OR COALESCE(co.first_name, '') ILIKE $%d
			OR COALESCE(d.error_message, '') ILIKE $%d
			OR d.id::text ILIKE $%d
		)`, argIdx, argIdx, argIdx, argIdx, argIdx))
		args = append(args, searchPattern)
		argIdx++
	}

	whereClause := strings.Join(conditions, " AND ")

	query := fmt.Sprintf(`
		SELECT 
			d.id,
			c.title,
			COALESCE(co.first_name, d.routing_value),
			d.platform,
			d.status,
			d.error_message,
			d.created_at,
			d.routing_value,
			c.id,
			d.target_type
		FROM campaign_deliveries d
		JOIN campaigns c ON d.campaign_id = c.id
		LEFT JOIN contacts co ON d.contact_id = co.id
		WHERE %s
		ORDER BY d.created_at DESC;
	`, whereClause)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to execute stream deliveries query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var act domain.DashboardDeliveryActivity
		var errMsg, routingVal, campID, targetType sql.NullString

		if err := rows.Scan(
			&act.ID, &act.CampaignName, &act.ContactName, &act.Platform, &act.Status, &errMsg, &act.CreatedAt,
			&routingVal, &campID, &targetType,
		); err != nil {
			return fmt.Errorf("failed to scan streaming delivery: %w", err)
		}
		if errMsg.Valid {
			act.ErrorMessage = errMsg.String
		}
		if routingVal.Valid {
			act.RoutingValue = routingVal.String
		}
		if campID.Valid {
			act.CampaignID = campID.String
		}
		if targetType.Valid {
			act.TargetType = targetType.String
		}

		if err := yield(&act); err != nil {
			return err
		}
	}

	return rows.Err()
}
