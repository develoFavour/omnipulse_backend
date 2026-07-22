package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresContactRepository struct {
	db *sql.DB
}

func NewPostgresContactRepository(db *sql.DB) domain.ContactRepository {
	return &PostgresContactRepository{db: db}
}

func (r *PostgresContactRepository) GetByID(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	query := `
		SELECT id, tenant_id, first_name, last_name, channel, routing_value, source, status, created_at, updated_at
		FROM contacts
		WHERE tenant_id = $1 AND id = $2;
	`
	var c domain.Contact
	err := r.db.QueryRowContext(ctx, query, tenantID, id).Scan(
		&c.ID, &c.TenantID, &c.FirstName, &c.LastName, &c.Channel, &c.RoutingValue, &c.Source, &c.Status, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrContactNotFound
		}
		return nil, fmt.Errorf("failed to fetch contact: %w", err)
	}
	return &c, nil
}

func (r *PostgresContactRepository) Create(ctx context.Context, c *domain.Contact) error {
	query := `
		INSERT INTO contacts (tenant_id, first_name, last_name, channel, routing_value, source, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, channel, routing_value) DO NOTHING
		RETURNING id, created_at, updated_at;
	`
	err := r.db.QueryRowContext(ctx, query,
		c.TenantID, c.FirstName, c.LastName, c.Channel, c.RoutingValue, c.Source, c.Status,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// This means ON CONFLICT DO NOTHING caught a duplicate, so no row was returned.
			// We can safely return nil or a domain error indicating a duplicate.
			return nil
		}
		return fmt.Errorf("failed to insert contact: %w", err)
	}
	return nil
}

func (r *PostgresContactRepository) ListByTenant(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
	var query string
	var rows *sql.Rows
	var err error

	if channelFilter != "" {
		query = `
			SELECT id, tenant_id, first_name, last_name, channel, routing_value, source, status, created_at, updated_at
			FROM contacts
			WHERE tenant_id = $1 AND channel = $2
			ORDER BY created_at DESC
			LIMIT $3 OFFSET $4;
		`
		rows, err = r.db.QueryContext(ctx, query, tenantID, channelFilter, limit, offset)
	} else {
		query = `
			SELECT id, tenant_id, first_name, last_name, channel, routing_value, source, status, created_at, updated_at
			FROM contacts
			WHERE tenant_id = $1
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3;
		`
		rows, err = r.db.QueryContext(ctx, query, tenantID, limit, offset)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to execute list query: %w", err)
	}
	defer rows.Close()

	contacts := make([]*domain.Contact, 0, limit)
	for rows.Next() {
		var c domain.Contact
		err := rows.Scan(
			&c.ID, &c.TenantID, &c.FirstName, &c.LastName, &c.Channel, &c.RoutingValue, &c.Source, &c.Status, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row into contact domain: %w", err)
		}
		contacts = append(contacts, &c)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading sequence rows stream: %w", err)
	}

	return contacts, nil
}
