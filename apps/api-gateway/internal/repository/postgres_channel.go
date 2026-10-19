package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"

	"github.com/lib/pq"
)

type PostgresChannelRepository struct {
	db            *sql.DB
	encryptionKey string
}

func NewPostgresChannelRepository(db *sql.DB, encryptionKey string) domain.ChannelRepository {
	return &PostgresChannelRepository{
		db:            db,
		encryptionKey: encryptionKey,
	}
}

func (r *PostgresChannelRepository) CreateChannel(ctx context.Context, channel *domain.TenantChannel) error {
	// Encrypt credentials at rest using AES-256-GCM before writing to database
	encryptedCreds, err := utils.EncryptBytes(channel.EncryptedCredentials, r.encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt channel credentials: %w", err)
	}
	accountIdentity := deriveWhatsAppAccountIdentity(channel)
	channel.AccountIdentity = accountIdentity

	query := `
		INSERT INTO tenant_channels (tenant_id, platform_name, sender_identity, account_identity, encrypted_credentials, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, platform_name) DO UPDATE
		SET sender_identity = EXCLUDED.sender_identity,
		    account_identity = EXCLUDED.account_identity,
		    encrypted_credentials = EXCLUDED.encrypted_credentials,
		    status = EXCLUDED.status,
		    updated_at = CURRENT_TIMESTAMP
		RETURNING id, created_at, updated_at;
	`
	err = r.db.QueryRowContext(ctx, query,
		channel.TenantID, channel.PlatformName, channel.SenderIdentity, accountIdentity, encryptedCreds, channel.Status,
	).Scan(&channel.ID, &channel.CreatedAt, &channel.UpdatedAt)

	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Constraint == "uq_active_whatsapp_account_identity" {
			return domain.ErrChannelIdentityAlreadyConnected
		}
		return fmt.Errorf("failed to insert/update channel: %w", err)
	}
	return nil
}

func (r *PostgresChannelRepository) ListByTenant(ctx context.Context, tenantID string) ([]domain.TenantChannel, error) {
	query := `
		SELECT id, tenant_id, platform_name, sender_identity, account_identity, encrypted_credentials, status, COALESCE(health_status, 'unknown'), last_heartbeat_at, last_successful_at, COALESCE(last_error, ''), last_error_at, created_at, updated_at
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
			&c.ID, &c.TenantID, &c.PlatformName, &c.SenderIdentity, &c.AccountIdentity, &c.EncryptedCredentials, &c.Status, &c.HealthStatus, &c.LastHeartbeatAt, &c.LastSuccessfulAt, &c.LastError, &c.LastErrorAt, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row into channel domain: %w", err)
		}

		// Decrypt credentials in memory. If legacy unencrypted, DecryptBytes safely preserves plain JSON
		if decrypted, decErr := utils.DecryptBytes(c.EncryptedCredentials, r.encryptionKey); decErr == nil {
			c.EncryptedCredentials = decrypted
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
		SELECT id, tenant_id, platform_name, sender_identity, account_identity, encrypted_credentials, status, COALESCE(health_status, 'unknown'), last_heartbeat_at, last_successful_at, COALESCE(last_error, ''), last_error_at, created_at, updated_at
		FROM tenant_channels
		WHERE tenant_id = $1 AND platform_name = $2 AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1;
	`
	var c domain.TenantChannel
	err := r.db.QueryRowContext(ctx, query, tenantID, platform).Scan(
		&c.ID, &c.TenantID, &c.PlatformName, &c.SenderIdentity, &c.AccountIdentity, &c.EncryptedCredentials, &c.Status, &c.HealthStatus, &c.LastHeartbeatAt, &c.LastSuccessfulAt, &c.LastError, &c.LastErrorAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("active %s channel not found", platform)
		}
		return nil, fmt.Errorf("failed to fetch active channel: %w", err)
	}

	// Decrypt credentials in memory
	if decrypted, decErr := utils.DecryptBytes(c.EncryptedCredentials, r.encryptionKey); decErr == nil {
		c.EncryptedCredentials = decrypted
	}

	return &c, nil
}

func deriveWhatsAppAccountIdentity(channel *domain.TenantChannel) string {
	if channel.PlatformName != "whatsapp" {
		return ""
	}

	var creds map[string]interface{}
	if json.Unmarshal(channel.EncryptedCredentials, &creds) == nil {
		for _, key := range []string{"phone", "display_phone_number", "display_phone"} {
			if value, ok := creds[key].(string); ok {
				if identity := domain.NormalizeWhatsAppPhoneIdentity(value); identity != "" {
					return identity
				}
			}
		}
	}

	// Both QR and Meta connections include the display phone in the sender
	// label, so this keeps the ownership key consistent across connection flows.
	if start := strings.LastIndex(channel.SenderIdentity, "("); start >= 0 {
		if end := strings.LastIndex(channel.SenderIdentity[start:], ")"); end >= 0 {
			if identity := domain.NormalizeWhatsAppPhoneIdentity(channel.SenderIdentity[start : start+end]); identity != "" {
				return identity
			}
		}
	}

	if json.Unmarshal(channel.EncryptedCredentials, &creds) == nil {
		for _, key := range []string{"phone_number_id", "waba_id", "jid"} {
			if value, ok := creds[key].(string); ok && strings.TrimSpace(value) != "" {
				value = strings.TrimSpace(value)
				if key == "jid" {
					value = strings.Split(value, "@")[0]
					value = strings.Split(value, ":")[0]
					if identity := domain.NormalizeWhatsAppPhoneIdentity(value); identity != "" {
						return identity
					}
				} else {
					return key + ":" + value
				}
			}
		}
	}

	return domain.NormalizeWhatsAppPhoneIdentity(channel.SenderIdentity)
}

func (r *PostgresChannelRepository) DeleteByPlatform(ctx context.Context, tenantID, platform string) error {
	query := `
		DELETE FROM tenant_channels
		WHERE tenant_id = $1 AND platform_name = $2;
	`
	result, err := r.db.ExecContext(ctx, query, tenantID, platform)
	if err != nil {
		return fmt.Errorf("failed to delete %s channel: %w", platform, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no active %s channel found to disconnect", platform)
	}

	return nil
}
