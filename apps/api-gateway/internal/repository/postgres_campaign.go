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
