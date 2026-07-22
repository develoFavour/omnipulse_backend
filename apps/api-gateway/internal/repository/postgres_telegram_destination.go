package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresTelegramDestinationRepository struct {
	db *sql.DB
}

func NewPostgresTelegramDestinationRepository(db *sql.DB) domain.TelegramDestinationRepository {
	return &PostgresTelegramDestinationRepository{db: db}
}

func (r *PostgresTelegramDestinationRepository) Upsert(ctx context.Context, d *domain.TelegramDestination) error {
	query := `
		INSERT INTO telegram_destinations (tenant_id, channel_id, telegram_chat_id, title, type, status, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, channel_id, telegram_chat_id)
		DO UPDATE SET title = EXCLUDED.title, type = EXCLUDED.type, status = EXCLUDED.status, updated_at = CURRENT_TIMESTAMP
		RETURNING id, created_at, updated_at;
	`
	return r.db.QueryRowContext(ctx, query,
		d.TenantID, d.ChannelID, d.TelegramChatID, d.Title, d.Type, d.Status, d.Source,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
}

func (r *PostgresTelegramDestinationRepository) ListByTenant(ctx context.Context, tenantID string) ([]domain.TelegramDestination, error) {
	query := `
		SELECT id, tenant_id, channel_id, telegram_chat_id, title, type, status, source, created_at, updated_at
		FROM telegram_destinations
		WHERE tenant_id = $1
		ORDER BY created_at DESC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list telegram destinations: %w", err)
	}
	defer rows.Close()
	return scanTelegramDestinations(rows)
}

func (r *PostgresTelegramDestinationRepository) ListByIDs(ctx context.Context, tenantID string, ids []string) ([]domain.TelegramDestination, error) {
	if len(ids) == 0 {
		return []domain.TelegramDestination{}, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, tenantID)
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT id, tenant_id, channel_id, telegram_chat_id, title, type, status, source, created_at, updated_at
		FROM telegram_destinations
		WHERE tenant_id = $1 AND status = 'active' AND id IN (%s)
		ORDER BY created_at DESC;
	`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list telegram destinations by ids: %w", err)
	}
	defer rows.Close()
	return scanTelegramDestinations(rows)
}

func scanTelegramDestinations(rows *sql.Rows) ([]domain.TelegramDestination, error) {
	destinations := []domain.TelegramDestination{}
	for rows.Next() {
		var d domain.TelegramDestination
		if err := rows.Scan(&d.ID, &d.TenantID, &d.ChannelID, &d.TelegramChatID, &d.Title, &d.Type, &d.Status, &d.Source, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan telegram destination: %w", err)
		}
		destinations = append(destinations, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return destinations, nil
}
