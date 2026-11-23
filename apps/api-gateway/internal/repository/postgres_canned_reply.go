package repository

import (
	"context"
	"database/sql"
	"fmt"

	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresCannedReplyRepository struct {
	db *sql.DB
}

func NewPostgresCannedReplyRepository(db *sql.DB) domain.CannedReplyRepository {
	return &PostgresCannedReplyRepository{db: db}
}

func (r *PostgresCannedReplyRepository) Create(ctx context.Context, reply *domain.CannedReply) error {
	query := `
		INSERT INTO canned_replies (tenant_id, shortcut, title, body, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at;
	`
	return r.db.QueryRowContext(
		ctx,
		query,
		reply.TenantID,
		reply.Shortcut,
		reply.Title,
		reply.Body,
		reply.CreatedBy,
	).Scan(&reply.ID, &reply.CreatedAt, &reply.UpdatedAt)
}

func (r *PostgresCannedReplyRepository) ListByTenant(ctx context.Context, tenantID string) ([]*domain.CannedReply, error) {
	query := `
		SELECT id, tenant_id, shortcut, title, body, created_by, created_at, updated_at
		FROM canned_replies
		WHERE tenant_id = $1
		ORDER BY shortcut ASC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query canned replies: %w", err)
	}
	defer rows.Close()

	replies := make([]*domain.CannedReply, 0)
	for rows.Next() {
		var cr domain.CannedReply
		if err := rows.Scan(
			&cr.ID,
			&cr.TenantID,
			&cr.Shortcut,
			&cr.Title,
			&cr.Body,
			&cr.CreatedBy,
			&cr.CreatedAt,
			&cr.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan canned reply: %w", err)
		}
		replies = append(replies, &cr)
	}
	return replies, nil
}

func (r *PostgresCannedReplyRepository) GetByID(ctx context.Context, tenantID, id string) (*domain.CannedReply, error) {
	query := `
		SELECT id, tenant_id, shortcut, title, body, created_by, created_at, updated_at
		FROM canned_replies
		WHERE tenant_id = $1 AND id = $2;
	`
	var cr domain.CannedReply
	err := r.db.QueryRowContext(ctx, query, tenantID, id).Scan(
		&cr.ID,
		&cr.TenantID,
		&cr.Shortcut,
		&cr.Title,
		&cr.Body,
		&cr.CreatedBy,
		&cr.CreatedAt,
		&cr.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get canned reply: %w", err)
	}
	return &cr, nil
}

func (r *PostgresCannedReplyRepository) Update(ctx context.Context, reply *domain.CannedReply) error {
	query := `
		UPDATE canned_replies
		SET shortcut = $1, title = $2, body = $3, updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $4 AND id = $5
		RETURNING updated_at;
	`
	return r.db.QueryRowContext(
		ctx,
		query,
		reply.Shortcut,
		reply.Title,
		reply.Body,
		reply.TenantID,
		reply.ID,
	).Scan(&reply.UpdatedAt)
}

func (r *PostgresCannedReplyRepository) Delete(ctx context.Context, tenantID, id string) error {
	query := `DELETE FROM canned_replies WHERE tenant_id = $1 AND id = $2;`
	_, err := r.db.ExecContext(ctx, query, tenantID, id)
	return err
}
