package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresNotificationRepository struct {
	db *sql.DB
}

func NewPostgresNotificationRepository(db *sql.DB) domain.NotificationRepository {
	return &PostgresNotificationRepository{db: db}
}

func (r *PostgresNotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	meta := n.Metadata
	if meta == nil {
		meta = json.RawMessage("{}")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notifications (tenant_id, type, title, body, metadata)
		VALUES ($1, $2, $3, $4, $5)`,
		n.TenantID, string(n.Type), n.Title, n.Body, meta,
	)
	return err
}

func (r *PostgresNotificationRepository) ListForTenant(ctx context.Context, tenantID string, limit int) ([]*domain.Notification, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, type, title, body, metadata, is_read, created_at
		FROM notifications
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2`,
		tenantID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	var out []*domain.Notification
	for rows.Next() {
		var n domain.Notification
		var rawMeta []byte
		if err := rows.Scan(
			&n.ID, &n.TenantID, &n.Type, &n.Title, &n.Body,
			&rawMeta, &n.IsRead, &n.CreatedAt,
		); err != nil {
			return nil, err
		}
		n.Metadata = json.RawMessage(rawMeta)
		out = append(out, &n)
	}
	return out, rows.Err()
}

func (r *PostgresNotificationRepository) UnreadCount(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM notifications
		WHERE tenant_id = $1 AND is_read = FALSE`,
		tenantID,
	).Scan(&count)
	return count, err
}

func (r *PostgresNotificationRepository) MarkRead(ctx context.Context, tenantID, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE notifications SET is_read = TRUE
		WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	return err
}

func (r *PostgresNotificationRepository) MarkAllRead(ctx context.Context, tenantID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE notifications SET is_read = TRUE
		WHERE tenant_id = $1 AND is_read = FALSE`,
		tenantID,
	)
	return err
}
