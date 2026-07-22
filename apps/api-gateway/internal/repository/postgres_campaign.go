package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	query := `
		UPDATE campaigns 
		SET status = $1, updated_at = CURRENT_TIMESTAMP 
		WHERE tenant_id = $2 AND id = $3;
	`
	_, err := r.db.ExecContext(ctx, query, status, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to transition campaign status state: %w", err)
	}
	return nil
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

	counterQuery := `
		UPDATE campaigns 
		SET processed_targets = processed_targets + 1, updated_at = CURRENT_TIMESTAMP 
		WHERE id = $1;
	`
	_, err = tx.ExecContext(ctx, counterQuery, res.CampaignID)
	if err != nil {
		return fmt.Errorf("failed to increment aggregator counter: %w", err)
	}

	return tx.Commit()
}

func (r *PostgresCampaignRepository) GetCampaignStats(ctx context.Context, tenantID, campaignID string) (map[string]int, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM campaigns WHERE id = $1 AND tenant_id = $2)", campaignID, tenantID).Scan(&exists)
	if err != nil || !exists {
		return nil, ErrCampaignNotFound
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

	stats := map[string]int{"sent": 0, "delivered": 0, "failed": 0}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats[status] = count
	}
	return stats, nil
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
