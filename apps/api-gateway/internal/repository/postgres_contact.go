package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

// ListWithFilter executes a high-performance indexed query with filtering, search, sorting, and pagination
func (r *PostgresContactRepository) ListWithFilter(ctx context.Context, tenantID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	conditions = append(conditions, fmt.Sprintf("c.tenant_id = $%d", argIdx))
	args = append(args, tenantID)
	argIdx++

	if filter.Channel != "" && !strings.EqualFold(filter.Channel, "all") {
		conditions = append(conditions, fmt.Sprintf("c.channel = $%d", argIdx))
		args = append(args, strings.ToLower(filter.Channel))
		argIdx++
	}

	if filter.TagID != "" {
		conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM contact_tags ct WHERE ct.contact_id = c.id AND ct.tag_id = $%d)", argIdx))
		args = append(args, filter.TagID)
		argIdx++
	}

	if strings.TrimSpace(filter.Search) != "" {
		searchPattern := "%" + strings.TrimSpace(filter.Search) + "%"
		conditions = append(conditions, fmt.Sprintf(`(
			c.first_name ILIKE $%d
			OR COALESCE(c.last_name, '') ILIKE $%d
			OR (c.first_name || ' ' || COALESCE(c.last_name, '')) ILIKE $%d
			OR c.routing_value ILIKE $%d
			OR EXISTS (
				SELECT 1 FROM contact_tags ct
				JOIN tags t ON ct.tag_id = t.id
				WHERE ct.contact_id = c.id AND t.name ILIKE $%d
			)
		)`, argIdx, argIdx, argIdx, argIdx, argIdx))
		args = append(args, searchPattern)
		argIdx++
	}

	whereClause := strings.Join(conditions, " AND ")

	// 1. Get exact matching total count
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM contacts c WHERE %s;", whereClause)
	var total int
	err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count contacts: %w", err)
	}

	if total == 0 {
		return []*domain.Contact{}, 0, nil
	}

	// 2. Safe sorting validation
	orderCol := "c.created_at"
	orderDir := "DESC"

	switch strings.ToLower(filter.SortKey) {
	case "name":
		orderCol = "c.first_name"
		orderDir = "ASC"
	case "channel":
		orderCol = "c.channel"
		orderDir = "ASC"
	case "source":
		orderCol = "c.source"
		orderDir = "ASC"
	case "created_at":
		orderCol = "c.created_at"
		orderDir = "DESC"
	}

	if strings.EqualFold(filter.SortDir, "asc") {
		orderDir = "ASC"
	} else if strings.EqualFold(filter.SortDir, "desc") {
		orderDir = "DESC"
	}

	// 3. Query paginated slice
	dataArgs := append([]interface{}{}, args...)
	dataQuery := fmt.Sprintf(`
		SELECT c.id, c.tenant_id, c.first_name, c.last_name, c.channel, c.routing_value, c.source, c.status, c.created_at, c.updated_at
		FROM contacts c
		WHERE %s
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d;
	`, whereClause, orderCol, orderDir, argIdx, argIdx+1)

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	dataArgs = append(dataArgs, limit, offset)

	rows, err := r.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to execute filtered contacts query: %w", err)
	}
	defer rows.Close()

	contacts := make([]*domain.Contact, 0, limit)
	for rows.Next() {
		var c domain.Contact
		err := rows.Scan(
			&c.ID, &c.TenantID, &c.FirstName, &c.LastName, &c.Channel, &c.RoutingValue, &c.Source, &c.Status, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan contact: %w", err)
		}
		contacts = append(contacts, &c)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error reading contact rows: %w", err)
	}

	return contacts, total, nil
}
