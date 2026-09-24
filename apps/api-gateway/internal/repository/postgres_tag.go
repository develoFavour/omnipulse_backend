package repository

import (
	"context"
	"database/sql"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
	"strings"

	"github.com/lib/pq"
)

type PostgresTagRepository struct {
	db *sql.DB
}

func NewPostgresTagRepository(db *sql.DB) domain.TagRepository {
	return &PostgresTagRepository{db: db}
}

func (r *PostgresTagRepository) Create(ctx context.Context, tag *domain.Tag) error {
	query := `
		INSERT INTO tags (tenant_id, name, color)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at;
	`
	err := r.db.QueryRowContext(ctx, query, tag.TenantID, tag.Name, tag.Color).Scan(
		&tag.ID, &tag.CreatedAt, &tag.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert tag: %w", err)
	}
	return nil
}

func (r *PostgresTagRepository) ListByTenant(ctx context.Context, tenantID string) ([]*domain.Tag, error) {
	query := `
		SELECT 
			t.id, t.tenant_id, t.name, t.color, t.created_at, t.updated_at,
			count(ct.contact_id) as contact_count
		FROM tags t
		LEFT JOIN contact_tags ct ON t.id = ct.tag_id
		WHERE t.tenant_id = $1
		GROUP BY t.id
		ORDER BY t.name ASC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags: %w", err)
	}
	defer rows.Close()

	tags := make([]*domain.Tag, 0)
	for rows.Next() {
		var t domain.Tag
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt, &t.ContactCount); err != nil {
			return nil, fmt.Errorf("failed to scan tag: %w", err)
		}
		tags = append(tags, &t)
	}
	return tags, nil
}

func (r *PostgresTagRepository) GetByID(ctx context.Context, tenantID, id string) (*domain.Tag, error) {
	query := `
		SELECT id, tenant_id, name, color, created_at, updated_at
		FROM tags
		WHERE tenant_id = $1 AND id = $2;
	`
	var t domain.Tag
	err := r.db.QueryRowContext(ctx, query, tenantID, id).Scan(
		&t.ID, &t.TenantID, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get tag: %w", err)
	}
	return &t, nil
}

func (r *PostgresTagRepository) Update(ctx context.Context, tag *domain.Tag) error {
	query := `
		UPDATE tags
		SET name = $1, color = $2, updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $3 AND id = $4
		RETURNING updated_at;
	`
	err := r.db.QueryRowContext(ctx, query, tag.Name, tag.Color, tag.TenantID, tag.ID).Scan(&tag.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update tag: %w", err)
	}
	return nil
}

func (r *PostgresTagRepository) Delete(ctx context.Context, tenantID, id string) error {
	query := `DELETE FROM tags WHERE tenant_id = $1 AND id = $2;`
	_, err := r.db.ExecContext(ctx, query, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to delete tag: %w", err)
	}
	return nil
}

func (r *PostgresTagRepository) AssignTagToContact(ctx context.Context, tenantID, contactID, tagID string) error {
	// Verify both belong to the tenant
	query := `
		INSERT INTO contact_tags (contact_id, tag_id)
		SELECT c.id, t.id
		FROM contacts c, tags t
		WHERE c.id = $1 AND c.tenant_id = $2
		  AND t.id = $3 AND t.tenant_id = $2
		ON CONFLICT (contact_id, tag_id) DO NOTHING;
	`
	res, err := r.db.ExecContext(ctx, query, contactID, tenantID, tagID)
	if err != nil {
		return fmt.Errorf("failed to assign tag: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		// Could be duplicate or invalid ID
		return nil
	}
	return nil
}

func (r *PostgresTagRepository) RemoveTagFromContact(ctx context.Context, tenantID, contactID, tagID string) error {
	query := `
		DELETE FROM contact_tags ct
		USING contacts c
		WHERE ct.contact_id = c.id
		  AND c.tenant_id = $1
		  AND ct.contact_id = $2
		  AND ct.tag_id = $3;
	`
	_, err := r.db.ExecContext(ctx, query, tenantID, contactID, tagID)
	if err != nil {
		return fmt.Errorf("failed to remove tag: %w", err)
	}
	return nil
}

func (r *PostgresTagRepository) AssignTagToContacts(ctx context.Context, tenantID, tagID string, contactIDs []string) error {
	if len(contactIDs) == 0 {
		return nil
	}
	query := `
		INSERT INTO contact_tags (contact_id, tag_id)
		SELECT c.id, t.id
		FROM contacts c, tags t
		WHERE c.id = ANY($1) AND c.tenant_id = $2
		  AND t.id = $3 AND t.tenant_id = $2
		ON CONFLICT (contact_id, tag_id) DO NOTHING;
	`
	_, err := r.db.ExecContext(ctx, query, pq.Array(contactIDs), tenantID, tagID)
	if err != nil {
		return fmt.Errorf("failed to bulk assign tag: %w", err)
	}
	return nil
}

func (r *PostgresTagRepository) RemoveTagFromContacts(ctx context.Context, tenantID, tagID string, contactIDs []string) error {
	if len(contactIDs) == 0 {
		return nil
	}
	query := `
		DELETE FROM contact_tags ct
		USING contacts c
		WHERE ct.contact_id = c.id
		  AND c.tenant_id = $1
		  AND ct.tag_id = $2
		  AND ct.contact_id = ANY($3);
	`
	_, err := r.db.ExecContext(ctx, query, tenantID, tagID, pq.Array(contactIDs))
	if err != nil {
		return fmt.Errorf("failed to bulk remove tag: %w", err)
	}
	return nil
}

func (r *PostgresTagRepository) GetTagsByContactIDs(ctx context.Context, tenantID string, contactIDs []string) (map[string][]*domain.Tag, error) {
	tagMap := make(map[string][]*domain.Tag)
	if len(contactIDs) == 0 {
		return tagMap, nil
	}

	query := `
		SELECT ct.contact_id, t.id, t.tenant_id, t.name, t.color, t.created_at, t.updated_at
		FROM contact_tags ct
		JOIN tags t ON ct.tag_id = t.id
		WHERE t.tenant_id = $1 AND ct.contact_id = ANY($2)
		ORDER BY t.name ASC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, pq.Array(contactIDs))
	if err != nil {
		// If pq.Array has an issue, fallback or return error
		if strings.Contains(err.Error(), "ANY") {
			return tagMap, nil
		}
		return nil, fmt.Errorf("failed to query contact tags: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var contactID string
		var t domain.Tag
		if err := rows.Scan(&contactID, &t.ID, &t.TenantID, &t.Name, &t.Color, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tagMap[contactID] = append(tagMap[contactID], &t)
	}
	return tagMap, nil
}
