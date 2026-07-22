package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresIdentityRepository struct {
	db *sql.DB
}

func NewPostgresIdentityRepository(db *sql.DB) domain.IdentityRepository {
	return &PostgresIdentityRepository{db: db}
}

func (r *PostgresIdentityRepository) FindUserByClerkID(ctx context.Context, clerkID string) (*domain.User, error) {
	query := `
		SELECT id, tenant_id, email, role, created_at, updated_at
		FROM users
		WHERE id = $1;
	`
	var u domain.User
	err := r.db.QueryRowContext(ctx, query, clerkID).Scan(
		&u.ID, &u.TenantID, &u.Email, &u.Role, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // Return nil if not found, let use case handle creation
		}
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}
	return &u, nil
}

func (r *PostgresIdentityRepository) FindTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error) {
	query := `
		SELECT id, company_name, onboarding_completed, created_at, updated_at
		FROM tenants
		WHERE id = $1;
	`
	var t domain.Tenant
	err := r.db.QueryRowContext(ctx, query, tenantID).Scan(
		&t.ID, &t.CompanyName, &t.OnboardingCompleted, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to fetch tenant: %w", err)
	}
	return &t, nil
}

func (r *PostgresIdentityRepository) CreateTenantWithUser(ctx context.Context, tenant *domain.Tenant, user *domain.User) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tenantQuery := `
		INSERT INTO tenants (company_name, onboarding_completed)
		VALUES ($1, $2)
		RETURNING id, created_at, updated_at;
	`
	err = tx.QueryRowContext(ctx, tenantQuery, tenant.CompanyName, tenant.OnboardingCompleted).
		Scan(&tenant.ID, &tenant.CreatedAt, &tenant.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert tenant: %w", err)
	}

	user.TenantID = tenant.ID
	userQuery := `
		INSERT INTO users (id, tenant_id, email, role)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at;
	`
	err = tx.QueryRowContext(ctx, userQuery, user.ID, user.TenantID, user.Email, user.Role).
		Scan(&user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert user: %w", err)
	}

	return tx.Commit()
}

func (r *PostgresIdentityRepository) UpdateTenantName(ctx context.Context, tenantID string, name string) error {
	query := `
		UPDATE tenants
		SET company_name = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2;
	`
	_, err := r.db.ExecContext(ctx, query, name, tenantID)
	if err != nil {
		return fmt.Errorf("failed to update tenant name: %w", err)
	}
	return nil
}

func (r *PostgresIdentityRepository) SetOnboardingCompleted(ctx context.Context, tenantID string) error {
	query := `
		UPDATE tenants
		SET onboarding_completed = TRUE, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1;
	`
	_, err := r.db.ExecContext(ctx, query, tenantID)
	if err != nil {
		return fmt.Errorf("failed to set onboarding completed: %w", err)
	}
	return nil
}
