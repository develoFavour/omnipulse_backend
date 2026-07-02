package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"omnipulse/apps/api-gateway/internals/domain"
)

// PostgresContactRepository implements domain.ContactRepository using a SQL connection pool
type PostgresContactRepository struct {
	db *sql.DB
}

// NewPostgresContactRepository is our explicit dependency injection constructor
func NewPostgresContactRepository(db *sql.DB) domain.ContactRepository {
	return &PostgresContactRepository{db: db}
}

// GetByID pulls a single contact out of PostgreSQL by its UUID
func (r *PostgresContactRepository) GetByID(ctx context.Context, id string) (*domain.Contact, error) {
	query := `
		SELECT id, first_name, last_name, whatsapp_phone, telegram_chat_id, x_username, is_opted_in, created_at, updated_at
		FROM contacts
		WHERE id = $1;
	`

	var c domain.Contact
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&c.ID,
		&c.FirstName,
		&c.LastName,
		&c.WhatsAppPhone,  // Handled cleanly because domain uses *string pointers for nullability
		&c.TelegramChatID, // Handled cleanly because domain uses *int64 pointers for nullability
		&c.XUsername,      // Handled cleanly because domain uses *string pointers for nullability
		&c.IsOptedIn,
		&c.CreatedAt,
		&c.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrContactNotFound
		}
		return nil, fmt.Errorf("failed to execute select query: %w", err)
	}

	return &c, nil
}

// Create inserts a brand new unified contact record into the persistent table
func (r *PostgresContactRepository) Create(ctx context.Context, c *domain.Contact) error {
	query := `
		INSERT INTO contacts (first_name, last_name, whatsapp_phone, telegram_chat_id, x_username, is_opted_in)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at;
	`

	// Using QueryRowContext instead of Exec because we want to grab the database-generated UUID and timestamps back
	err := r.db.QueryRowContext(ctx, query,
		c.FirstName,
		c.LastName,
		c.WhatsAppPhone,
		c.TelegramChatID,
		c.XUsername,
		c.IsOptedIn,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to execute insert query: %w", err)
	}

	return nil
}

// List handles paginated read lookups for massive audience panels
func (r *PostgresContactRepository) List(ctx context.Context, limit, offset int) ([]*domain.Contact, error) {
	query := `
		SELECT id, first_name, last_name, whatsapp_phone, telegram_chat_id, x_username, is_opted_in, created_at, updated_at
		FROM contacts
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2;
	`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to execute list query: %w", err)
	}
	defer rows.Close() // Critical: Prevents leaking open database cursor memory handles

	contacts := make([]*domain.Contact, 0, limit) // Pre-allocate slice capacity to optimize RAM footprint

	for rows.Next() {
		var c domain.Contact
		err := rows.Scan(
			&c.ID,
			&c.FirstName,
			&c.LastName,
			&c.WhatsAppPhone,
			&c.TelegramChatID,
			&c.XUsername,
			&c.IsOptedIn,
			&c.CreatedAt,
			&c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row into contact domain: %w", err)
		}
		contacts = append(contacts, &c)
	}

	// Always double check rows.Err() after loops finish to catch hidden asynchronous stream evaluation errors
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading sequence rows stream: %w", err)
	}

	return contacts, nil
}
