package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresReviewRepository struct {
	db *sql.DB
}

func NewPostgresReviewRepository(db *sql.DB) domain.ReviewRepository {
	return &PostgresReviewRepository{db: db}
}

func (r *PostgresReviewRepository) CreateToken(ctx context.Context, token *domain.ReviewToken, tokenHash string) error {
	return r.db.QueryRowContext(ctx, `
		INSERT INTO campaign_review_tokens (tenant_id, campaign_id, token_hash, label, recipient_email, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, token.TenantID, token.CampaignID, tokenHash, token.Label, token.RecipientEmail, token.CreatedBy, token.ExpiresAt).Scan(&token.ID, &token.CreatedAt)
}

func (r *PostgresReviewRepository) ListTokens(ctx context.Context, tenantID, campaignID string) ([]*domain.ReviewToken, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.tenant_id, t.campaign_id, t.label, t.recipient_email, t.created_by,
		       t.expires_at, t.revoked_at, t.last_viewed_at, t.created_at, e.decision
		FROM campaign_review_tokens t
		LEFT JOIN LATERAL (
			SELECT ev.decision
			FROM campaign_review_events ev
			WHERE ev.token_id = t.id
			ORDER BY ev.created_at DESC
			LIMIT 1
		) e ON TRUE
		WHERE t.tenant_id = $1 AND t.campaign_id = $2
		ORDER BY t.created_at DESC
	`, tenantID, campaignID)
	if err != nil {
		return nil, fmt.Errorf("failed to list review tokens: %w", err)
	}
	defer rows.Close()

	tokens := make([]*domain.ReviewToken, 0)
	for rows.Next() {
		token := &domain.ReviewToken{}
		var revokedAt, lastViewedAt sql.NullTime
		var decision sql.NullString
		if err := rows.Scan(
			&token.ID, &token.TenantID, &token.CampaignID, &token.Label, &token.RecipientEmail, &token.CreatedBy,
			&token.ExpiresAt, &revokedAt, &lastViewedAt, &token.CreatedAt, &decision,
		); err != nil {
			return nil, fmt.Errorf("failed to scan review token: %w", err)
		}
		if revokedAt.Valid {
			token.RevokedAt = &revokedAt.Time
		}
		if lastViewedAt.Valid {
			token.LastViewedAt = &lastViewedAt.Time
		}
		if decision.Valid {
			token.Decision = decision.String
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (r *PostgresReviewRepository) RevokeToken(ctx context.Context, tenantID, campaignID, tokenID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE campaign_review_tokens
		SET revoked_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND campaign_id = $3 AND revoked_at IS NULL
	`, tokenID, tenantID, campaignID)
	if err != nil {
		return fmt.Errorf("failed to revoke review token: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return domain.ErrReviewLinkNotFound
	}
	return nil
}

func (r *PostgresReviewRepository) FindTokenByHash(ctx context.Context, tokenHash string) (*domain.ReviewToken, error) {
	token := &domain.ReviewToken{}
	var revokedAt, lastViewedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, campaign_id, label, created_by,
		       expires_at, revoked_at, last_viewed_at, created_at
		FROM campaign_review_tokens
		WHERE token_hash = $1
	`, tokenHash).Scan(
		&token.ID, &token.TenantID, &token.CampaignID, &token.Label, &token.CreatedBy,
		&token.ExpiresAt, &revokedAt, &lastViewedAt, &token.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to fetch review token: %w", err)
	}
	if revokedAt.Valid {
		token.RevokedAt = &revokedAt.Time
	}
	if lastViewedAt.Valid {
		token.LastViewedAt = &lastViewedAt.Time
	}
	return token, nil
}

func (r *PostgresReviewRepository) TouchToken(ctx context.Context, tokenID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE campaign_review_tokens SET last_viewed_at = NOW() WHERE id = $1`, tokenID)
	return err
}

func (r *PostgresReviewRepository) LatestDecision(ctx context.Context, tenantID, campaignID string) (*domain.ReviewEvent, error) {
	event := &domain.ReviewEvent{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, campaign_id, token_id, decision, COALESCE(note, ''), COALESCE(reviewer_ip, ''), created_at
		FROM campaign_review_events
		WHERE tenant_id = $1 AND campaign_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, tenantID, campaignID).Scan(
		&event.ID, &event.CampaignID, &event.TokenID, &event.Decision, &event.Note, &event.ReviewerIP, &event.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to fetch latest review decision: %w", err)
	}
	return event, nil
}

// RecordDecision appends a brand decision. A token may be used more than once
// while the campaign is still open for review: revising the campaign changes
// what the brand sees, so they must be able to answer again. Every decision is
// kept and the most recent one governs.
func (r *PostgresReviewRepository) RecordDecision(ctx context.Context, event *domain.ReviewEvent) error {
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO campaign_review_events (tenant_id, campaign_id, token_id, decision, note, reviewer_ip)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, event.TenantID, event.CampaignID, event.TokenID, event.Decision, event.Note, event.ReviewerIP).Scan(&event.ID, &event.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to record review decision: %w", err)
	}
	return nil
}

func (r *PostgresReviewRepository) GetBranding(ctx context.Context, tenantID string) (*domain.ReviewBrand, error) {
	brand := &domain.ReviewBrand{}
	err := r.db.QueryRowContext(ctx, `
		SELECT t.company_name, COALESCE(s.logo_url, '')
		FROM tenants t
		LEFT JOIN tenant_settings s ON s.tenant_id = t.id
		WHERE t.id = $1
	`, tenantID).Scan(&brand.Name, &brand.LogoURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.ReviewBrand{}, nil
		}
		return nil, fmt.Errorf("failed to fetch tenant branding: %w", err)
	}
	return brand, nil
}

// ResolveStaffNames scopes the lookup to one workspace so a campaign can never
// expose a name from a workspace the reviewer does not belong to.
func (r *PostgresReviewRepository) ResolveStaffNames(ctx context.Context, tenantID string, userIDs []string) (map[string]string, error) {
	names := make(map[string]string, len(userIDs))
	if len(userIDs) == 0 {
		return names, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT u.id, TRIM(CONCAT_WS(' ', NULLIF(u.first_name, ''), NULLIF(u.last_name, ''))), COALESCE(u.email, '')
		FROM users u
		JOIN tenant_members tm ON tm.user_id = u.id
		WHERE tm.tenant_id = $1::uuid AND u.id = ANY($2)
	`, tenantID, pq.Array(userIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve staff names: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, email string
		if err := rows.Scan(&id, &name, &email); err != nil {
			return nil, fmt.Errorf("failed to scan staff name: %w", err)
		}
		if name == "" {
			name = email
		}
		names[id] = name
	}
	return names, rows.Err()
}
