package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresConsentRepository struct {
	db *sql.DB
}

func NewPostgresConsentRepository(db *sql.DB) domain.ConsentRepository {
	return &PostgresConsentRepository{db: db}
}

// ApplyEvent appends the event then projects it onto contacts.status. The
// projection only fires when this event is the latest for the contact, so a
// delayed webhook cannot resurrect a contact who later opted out.
func (r *PostgresConsentRepository) ApplyEvent(ctx context.Context, event *domain.ConsentEvent) (bool, error) {
	evidence := event.Evidence
	if evidence == "" {
		evidence = "{}"
	}

	// An empty key must be stored as NULL: a unique index treats NULLs as
	// distinct, so keyless operator events never collide with each other.
	var idempotencyKey interface{}
	if event.IdempotencyKey != "" {
		idempotencyKey = event.IdempotencyKey
	}
	var occurredAt interface{}
	if !event.OccurredAt.IsZero() {
		occurredAt = event.OccurredAt
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO consent_events (
			tenant_id, contact_id, event_type, source, channel, evidence, occurred_at, idempotency_key
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, COALESCE($7, CURRENT_TIMESTAMP), $8)
		ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
		RETURNING id;
	`,
		event.TenantID,
		event.ContactID,
		string(event.EventType),
		string(event.Source),
		event.Channel,
		evidence,
		occurredAt,
		idempotencyKey,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to append consent event: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE contacts c
		SET status = CASE WHEN $3 = 'revoked' THEN 'opted_out' ELSE 'active' END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE c.id = $1
		  AND c.tenant_id = $2
		  AND (
		      SELECT e.event_type
		      FROM consent_events e
		      WHERE e.contact_id = c.id
		      ORDER BY e.occurred_at DESC, e.recorded_at DESC
		      LIMIT 1
		  ) = $3::text;
	`, event.ContactID, event.TenantID, string(event.EventType)); err != nil {
		return false, fmt.Errorf("failed to project consent onto contact: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *PostgresConsentRepository) LatestForContact(ctx context.Context, tenantID, contactID string) (*domain.ConsentEvent, error) {
	var e domain.ConsentEvent
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, contact_id, event_type, source, channel, evidence::text,
		       occurred_at, recorded_at, COALESCE(idempotency_key, '')
		FROM consent_events
		WHERE tenant_id = $1 AND contact_id = $2
		ORDER BY occurred_at DESC, recorded_at DESC
		LIMIT 1;
	`, tenantID, contactID).Scan(
		&e.ID, &e.TenantID, &e.ContactID, &e.EventType, &e.Source, &e.Channel,
		&e.Evidence, &e.OccurredAt, &e.RecordedAt, &e.IdempotencyKey,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *PostgresConsentRepository) ListForContacts(ctx context.Context, tenantID string, contactIDs []string) ([]*domain.ConsentEvent, error) {
	if len(contactIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, contact_id, event_type, source, channel, evidence::text,
		       occurred_at, recorded_at, COALESCE(idempotency_key, '')
		FROM consent_events
		WHERE tenant_id = $1 AND contact_id = ANY($2::uuid[])
		ORDER BY occurred_at ASC, recorded_at ASC;
	`, tenantID, pq.Array(contactIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*domain.ConsentEvent
	for rows.Next() {
		var e domain.ConsentEvent
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.ContactID, &e.EventType, &e.Source, &e.Channel,
			&e.Evidence, &e.OccurredAt, &e.RecordedAt, &e.IdempotencyKey,
		); err != nil {
			return nil, err
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}
