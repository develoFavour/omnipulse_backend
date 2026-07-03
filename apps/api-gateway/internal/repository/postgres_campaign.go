package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"omnipulse/apps/api-gateway/internal/domain"
)

// Sentinel campaign error
var ErrCampaignNotFound = errors.New("campaign not found")

type PostgresCampaignRepository struct {
	db *sql.DB
}

func NewPostgresCampaignRepository(db *sql.DB) domain.CampaignRepository {
	return &PostgresCampaignRepository{db: db}
}

func (r *PostgresCampaignRepository) GetByID(ctx context.Context, id string) (*domain.Campaign, error) {
	query := `
		SELECT id, title, message_body, external_template_code, media_url, status, total_targets, processed_targets, created_at, updated_at
		FROM campaigns
		WHERE id = $1;
	`
	var c domain.Campaign
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&c.ID,
		&c.Title,
		&c.MessageBody,
		&c.ExternalTemplateCode,
		&c.MediaURL,
		&c.Status,
		&c.TotalTargets,
		&c.ProcessedTargets,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCampaignNotFound
		}
		return nil, fmt.Errorf("failed to fetch campaign row: %w", err)
	}
	return &c, nil
}

func (r *PostgresCampaignRepository) UpdateStatus(ctx context.Context, id string, status string) error {
	query := `
		UPDATE campaigns 
		SET status = $1, updated_at = CURRENT_TIMESTAMP 
		WHERE id = $2;
	`
	_, err := r.db.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("failed to transition campaign status state: %w", err)
	}
	return nil
}

func (r *PostgresCampaignRepository) RecordDeliveryResult(ctx context.Context, res *domain.TargetDeliveryResult) error {
	// Execute within a strict transaction block to maintain absolute data integrity
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Safely rolls back changes if an error occurs mid-flight

	// 1. Insert the detailed tracking audit row
	auditQuery := `
		INSERT INTO campaign_deliveries (campaign_id, contact_id, platform, routing_value, status, error_message)
		VALUES ($1, $2, $3, $4, $5, $6);
	`
	_, err = tx.ExecContext(ctx, auditQuery, res.CampaignID, res.ContactID, res.Platform, res.RoutingValue, res.Status, res.ErrorMessage)
	if err != nil {
		return fmt.Errorf("failed to insert audit line record: %w", err)
	}

	// 2. Increment the master campaign aggregator metrics counter
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

func (r *PostgresCampaignRepository) GetCampaignStats(ctx context.Context, campaignID string) (map[string]int, error) {
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
