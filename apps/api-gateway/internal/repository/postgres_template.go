package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresTemplateRepository struct {
	db *sql.DB
}

func NewPostgresTemplateRepository(db *sql.DB) domain.TemplateRepository {
	return &PostgresTemplateRepository{db: db}
}

func (r *PostgresTemplateRepository) Create(ctx context.Context, tmpl *domain.Template) error {
	varsJSON, err := json.Marshal(tmpl.Variables)
	if err != nil {
		varsJSON = []byte("[]")
	}

	query := `
		INSERT INTO message_templates (tenant_id, title, category, body, media_url, variables)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at;
	`
	return r.db.QueryRowContext(
		ctx,
		query,
		tmpl.TenantID,
		tmpl.Title,
		tmpl.Category,
		tmpl.Body,
		tmpl.MediaURL,
		varsJSON,
	).Scan(&tmpl.ID, &tmpl.CreatedAt, &tmpl.UpdatedAt)
}

func (r *PostgresTemplateRepository) ListByTenant(ctx context.Context, tenantID, category string) ([]*domain.Template, error) {
	var query string
	var rows *sql.Rows
	var err error

	if category != "" && category != "all" {
		query = `
			SELECT id, tenant_id, title, category, body, media_url, variables, created_at, updated_at
			FROM message_templates
			WHERE tenant_id = $1 AND category = $2
			ORDER BY updated_at DESC;
		`
		rows, err = r.db.QueryContext(ctx, query, tenantID, category)
	} else {
		query = `
			SELECT id, tenant_id, title, category, body, media_url, variables, created_at, updated_at
			FROM message_templates
			WHERE tenant_id = $1
			ORDER BY updated_at DESC;
		`
		rows, err = r.db.QueryContext(ctx, query, tenantID)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query templates: %w", err)
	}
	defer rows.Close()

	templates := make([]*domain.Template, 0)
	for rows.Next() {
		var t domain.Template
		var varsBytes []byte
		var mediaURL sql.NullString

		if err := rows.Scan(
			&t.ID,
			&t.TenantID,
			&t.Title,
			&t.Category,
			&t.Body,
			&mediaURL,
			&varsBytes,
			&t.CreatedAt,
			&t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan template: %w", err)
		}

		if mediaURL.Valid {
			t.MediaURL = &mediaURL.String
		}

		if len(varsBytes) > 0 {
			_ = json.Unmarshal(varsBytes, &t.Variables)
		}
		if t.Variables == nil {
			t.Variables = []string{}
		}

		templates = append(templates, &t)
	}

	return templates, nil
}

func (r *PostgresTemplateRepository) GetByID(ctx context.Context, tenantID, id string) (*domain.Template, error) {
	query := `
		SELECT id, tenant_id, title, category, body, media_url, variables, created_at, updated_at
		FROM message_templates
		WHERE tenant_id = $1 AND id = $2;
	`
	var t domain.Template
	var varsBytes []byte
	var mediaURL sql.NullString

	err := r.db.QueryRowContext(ctx, query, tenantID, id).Scan(
		&t.ID,
		&t.TenantID,
		&t.Title,
		&t.Category,
		&t.Body,
		&mediaURL,
		&varsBytes,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	if mediaURL.Valid {
		t.MediaURL = &mediaURL.String
	}
	if len(varsBytes) > 0 {
		_ = json.Unmarshal(varsBytes, &t.Variables)
	}
	if t.Variables == nil {
		t.Variables = []string{}
	}

	return &t, nil
}

func (r *PostgresTemplateRepository) Update(ctx context.Context, tmpl *domain.Template) error {
	varsJSON, err := json.Marshal(tmpl.Variables)
	if err != nil {
		varsJSON = []byte("[]")
	}

	query := `
		UPDATE message_templates
		SET title = $1, category = $2, body = $3, media_url = $4, variables = $5, updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $6 AND id = $7
		RETURNING updated_at;
	`
	return r.db.QueryRowContext(
		ctx,
		query,
		tmpl.Title,
		tmpl.Category,
		tmpl.Body,
		tmpl.MediaURL,
		varsJSON,
		tmpl.TenantID,
		tmpl.ID,
	).Scan(&tmpl.UpdatedAt)
}

func (r *PostgresTemplateRepository) Delete(ctx context.Context, tenantID, id string) error {
	query := `DELETE FROM message_templates WHERE tenant_id = $1 AND id = $2;`
	_, err := r.db.ExecContext(ctx, query, tenantID, id)
	return err
}
