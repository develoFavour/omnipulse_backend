package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

var ErrCampaignNotFound = errors.New("campaign not found")

type PostgresCampaignRepository struct {
	db *sql.DB
}

func NewPostgresCampaignRepository(db *sql.DB) domain.CampaignRepository {
	return &PostgresCampaignRepository{db: db}
}

func (r *PostgresCampaignRepository) Create(ctx context.Context, c *domain.Campaign) error {
	query := `
		INSERT INTO campaigns (tenant_id, title, message_body, external_template_code, media_url, delivery_type, selected_channels, selected_telegram_destination_ids, status, total_targets)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at, updated_at;
	`
	err := r.db.QueryRowContext(ctx, query,
		c.TenantID, c.Title, c.MessageBody, c.ExternalTemplateCode, c.MediaURL, c.DeliveryType, c.SelectedChannels, c.SelectedTelegramDestinationIDs, c.Status, c.TotalTargets,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert campaign record: %w", err)
	}
	return nil
}

func (r *PostgresCampaignRepository) ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]*domain.Campaign, error) {
	query := `
		SELECT id, tenant_id, title, message_body, external_template_code, media_url, delivery_type, selected_channels, selected_telegram_destination_ids, status, total_targets, processed_targets, created_at, updated_at
		FROM campaigns
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to execute campaign list query: %w", err)
	}
	defer rows.Close()

	campaigns := make([]*domain.Campaign, 0, limit)
	for rows.Next() {
		var c domain.Campaign
		err := rows.Scan(
			&c.ID, &c.TenantID, &c.Title, &c.MessageBody, &c.ExternalTemplateCode, &c.MediaURL, &c.DeliveryType, &c.SelectedChannels, &c.SelectedTelegramDestinationIDs, &c.Status, &c.TotalTargets, &c.ProcessedTargets, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row into campaign domain: %w", err)
		}
		campaigns = append(campaigns, &c)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading campaign rows stream: %w", err)
	}

	return campaigns, nil
}

func (r *PostgresCampaignRepository) GetByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	query := `
		SELECT id, tenant_id, title, message_body, external_template_code, media_url, delivery_type, selected_channels, selected_telegram_destination_ids, status, total_targets, processed_targets, created_at, updated_at
		FROM campaigns
		WHERE tenant_id = $1 AND id = $2;
	`
	var c domain.Campaign
	err := r.db.QueryRowContext(ctx, query, tenantID, id).Scan(
		&c.ID, &c.TenantID, &c.Title, &c.MessageBody, &c.ExternalTemplateCode, &c.MediaURL, &c.DeliveryType, &c.SelectedChannels, &c.SelectedTelegramDestinationIDs, &c.Status, &c.TotalTargets, &c.ProcessedTargets, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCampaignNotFound
		}
		return nil, fmt.Errorf("failed to fetch campaign row: %w", err)
	}
	return &c, nil
}

func (r *PostgresCampaignRepository) UpdateStatus(ctx context.Context, tenantID, id string, status string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tx for status transition: %w", err)
	}
	defer tx.Rollback()

	query := `
		UPDATE campaigns 
		SET status = $1, updated_at = CURRENT_TIMESTAMP 
		WHERE tenant_id = $2 AND id = $3;
	`
	_, err = tx.ExecContext(ctx, query, status, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to transition campaign status state: %w", err)
	}
	return tx.Commit()
}

// SetDispatching atomically marks the campaign as 'processing' and records total_targets.
// Called once when all tasks are queued to NATS — NOT when they're delivered.
func (r *PostgresCampaignRepository) SetDispatching(ctx context.Context, tenantID, id string, totalTargets int) error {
	query := `
		UPDATE campaigns
		SET status = 'processing', total_targets = $1, processed_targets = 0, updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $2 AND id = $3;
	`
	_, err := r.db.ExecContext(ctx, query, totalTargets, tenantID, id)
	return err
}

func (r *PostgresCampaignRepository) RecordDeliveryResult(ctx context.Context, res *contracts.TargetDeliveryResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	auditQuery := `
		INSERT INTO campaign_deliveries (campaign_id, contact_id, target_type, platform, routing_value, status, error_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7);
	`
	_, err = tx.ExecContext(ctx, auditQuery, res.CampaignID, nullableContactID(res), normalizedTargetType(res.TargetType), res.Platform, res.RoutingValue, res.Status, res.ErrorMessage)
	if err != nil {
		return fmt.Errorf("failed to insert audit line record: %w", err)
	}

	// Increment the processed counter and check if all targets are done in one atomic query.
	// If processed_targets reaches total_targets, auto-transition to 'completed'.
	counterQuery := `
		UPDATE campaigns
		SET processed_targets = processed_targets + 1, updated_at = CURRENT_TIMESTAMP,
		    status = CASE
		        WHEN (processed_targets + 1) >= total_targets AND total_targets > 0 THEN 'completed'
		        ELSE status
		    END
		WHERE id = $1;
	`
	_, err = tx.ExecContext(ctx, counterQuery, res.CampaignID)
	if err != nil {
		return fmt.Errorf("failed to increment aggregator counter: %w", err)
	}

	return tx.Commit()
}

func (r *PostgresCampaignRepository) GetCampaignStats(ctx context.Context, tenantID, campaignID string) (*domain.CampaignStats, error) {
	var status string
	var totalTargets, processedTargets int
	err := r.db.QueryRowContext(ctx, "SELECT status, total_targets, processed_targets FROM campaigns WHERE id = $1 AND tenant_id = $2", campaignID, tenantID).Scan(&status, &totalTargets, &processedTargets)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCampaignNotFound
		}
		return nil, fmt.Errorf("failed to fetch campaign stats header: %w", err)
	}

	query := `
		SELECT status, COUNT(*) 
		FROM campaign_deliveries 
		WHERE campaign_id = $1 
		GROUP BY status;
	`
	rows, err := r.db.QueryContext(ctx, query, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{"sent": 0, "delivered": 0, "failed": 0}
	for rows.Next() {
		var st string
		var cnt int
		if err := rows.Scan(&st, &cnt); err != nil {
			return nil, err
		}
		counts[st] = cnt
	}

	progress := 0.0
	if totalTargets > 0 {
		progress = (float64(processedTargets) / float64(totalTargets)) * 100.0
		if progress > 100.0 {
			progress = 100.0
		}
	} else if status == "completed" {
		progress = 100.0
	}

	return &domain.CampaignStats{
		CampaignID:       campaignID,
		Status:           status,
		TotalTargets:     totalTargets,
		ProcessedTargets: processedTargets,
		Sent:             counts["sent"],
		Delivered:        counts["delivered"],
		Failed:           counts["failed"],
		ProgressPercent:  math.Round(progress*10) / 10,
	}, nil
}

func (r *PostgresCampaignRepository) ListDeliveriesByCampaign(ctx context.Context, tenantID, campaignID string, limit, offset int) ([]*domain.CampaignDelivery, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM campaigns WHERE id = $1 AND tenant_id = $2)", campaignID, tenantID).Scan(&exists)
	if err != nil || !exists {
		return nil, ErrCampaignNotFound
	}

	query := `
		SELECT d.id, d.campaign_id, d.contact_id, d.target_type, d.platform, d.routing_value, d.status, d.error_message, d.created_at
		FROM campaign_deliveries d
		WHERE d.campaign_id = $1
		ORDER BY d.created_at DESC
		LIMIT $2 OFFSET $3;
	`
	rows, err := r.db.QueryContext(ctx, query, campaignID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query campaign deliveries: %w", err)
	}
	defer rows.Close()

	deliveries := make([]*domain.CampaignDelivery, 0, limit)
	for rows.Next() {
		var d domain.CampaignDelivery
		var contactID sql.NullString
		var errMsg sql.NullString
		if err := rows.Scan(&d.ID, &d.CampaignID, &contactID, &d.TargetType, &d.Platform, &d.RoutingValue, &d.Status, &errMsg, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan campaign delivery: %w", err)
		}
		if contactID.Valid {
			d.ContactID = &contactID.String
		}
		if errMsg.Valid {
			d.ErrorMessage = &errMsg.String
		}
		deliveries = append(deliveries, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during delivery rows iteration: %w", err)
	}
	return deliveries, nil
}
func nullableContactID(res *contracts.TargetDeliveryResult) interface{} {
	if res.TargetType == "telegram_destination" || res.ContactID == "" {
		return nil
	}
	return res.ContactID
}

func normalizedTargetType(targetType string) string {
	if targetType == "telegram_destination" {
		return targetType
	}
	return "contact"
}
