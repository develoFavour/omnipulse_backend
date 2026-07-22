package repository

import (
	"context"
	"database/sql"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresChannelRepository struct {
	db *sql.DB
}

func NewPostgresChannelRepository(db *sql.DB) domain.ChannelRepository {
	return &PostgresChannelRepository{db: db}
}

func (r *PostgresChannelRepository) CreateChannel(ctx context.Context, channel *domain.TenantChannel) error {
	query := `
		INSERT INTO tenant_channels (tenant_id, platform_name, sender_identity, encrypted_credentials, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, platform_name) DO UPDATE
		SET sender_identity = EXCLUDED.sender_identity,
		    encrypted_credentials = EXCLUDED.encrypted_credentials,
		    status = EXCLUDED.status,
		    updated_at = CURRENT_TIMESTAMP
		RETURNING id, created_at, updated_at;
	`
	err := r.db.QueryRowContext(ctx, query,
		channel.TenantID, channel.PlatformName, channel.SenderIdentity, channel.EncryptedCredentials, channel.Status,
	).Scan(&channel.ID, &channel.CreatedAt, &channel.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert/update channel: %w", err)
	}
	return nil
}

func (r *PostgresChannelRepository) ListByTenant(ctx context.Context, tenantID string) ([]domain.TenantChannel, error) {
	query := `
		SELECT id, tenant_id, platform_name, sender_identity, encrypted_credentials, status, created_at, updated_at
		FROM tenant_channels
		WHERE tenant_id = $1
		ORDER BY created_at DESC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to execute channel list query: %w", err)
	}
	defer rows.Close()

	var channels []domain.TenantChannel
	for rows.Next() {
		var c domain.TenantChannel
		err := rows.Scan(
			&c.ID, &c.TenantID, &c.PlatformName, &c.SenderIdentity, &c.EncryptedCredentials, &c.Status, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row into channel domain: %w", err)
		}
		channels = append(channels, c)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading sequence rows stream: %w", err)
	}

	return channels, nil
}

func (r *PostgresChannelRepository) CountActiveByTenant(ctx context.Context, tenantID string) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM tenant_channels
		WHERE tenant_id = $1 AND status = 'active';
	`
	var count int
	err := r.db.QueryRowContext(ctx, query, tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count active channels: %w", err)
	}
	return count, nil
}

func (r *PostgresChannelRepository) FindActiveByPlatform(ctx context.Context, tenantID, platform string) (*domain.TenantChannel, error) {
	query := `
		SELECT id, tenant_id, platform_name, sender_identity, encrypted_credentials, status, created_at, updated_at
		FROM tenant_channels
		WHERE tenant_id = $1 AND platform_name = $2 AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1;
	`
	var c domain.TenantChannel
	err := r.db.QueryRowContext(ctx, query, tenantID, platform).Scan(
		&c.ID, &c.TenantID, &c.PlatformName, &c.SenderIdentity, &c.EncryptedCredentials, &c.Status, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("active %s channel not found", platform)
		}
		return nil, fmt.Errorf("failed to fetch active channel: %w", err)
	}
	return &c, nil
}
