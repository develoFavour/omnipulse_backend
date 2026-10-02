package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
		SELECT id::text, tenant_id, email, role, created_at, updated_at
		FROM users
		WHERE id::text = $1::text;
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

func (r *PostgresIdentityRepository) UpdateUserEmail(ctx context.Context, userID, email string) error {
	query := `
		UPDATE users
		SET email = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2;
	`
	_, err := r.db.ExecContext(ctx, query, email, userID)
	return err
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

	// Insert primary membership into tenant_members
	memberQuery := `
		INSERT INTO tenant_members (tenant_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, user_id) DO NOTHING;
	`
	if _, err := tx.ExecContext(ctx, memberQuery, tenant.ID, user.ID, user.Role); err != nil {
		return fmt.Errorf("failed to record workspace membership: %w", err)
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

// ListMembers fetches all users joined to a specific workspace
func (r *PostgresIdentityRepository) ListMembers(ctx context.Context, tenantID string) ([]domain.TenantMember, error) {
	query := `
		SELECT 
			tm.id::text, 
			tm.tenant_id::text, 
			tm.user_id, 
			tm.role, 
			COALESCE(u.email, '') as email, 
			tm.created_at, 
			tm.updated_at
		FROM tenant_members tm
		LEFT JOIN users u ON tm.user_id = u.id
		WHERE tm.tenant_id = $1::uuid
		ORDER BY 
			CASE tm.role 
				WHEN 'owner' THEN 1 
				WHEN 'admin' THEN 2 
				ELSE 3 
			END, 
			tm.created_at ASC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list tenant members: %w", err)
	}
	defer rows.Close()

	var members []domain.TenantMember
	for rows.Next() {
		var m domain.TenantMember
		if err := rows.Scan(&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.Email, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan tenant member: %w", err)
		}
		members = append(members, m)
	}
	return members, nil
}

// GetMemberRole retrieves the explicit workspace role for a user
func (r *PostgresIdentityRepository) GetMemberRole(ctx context.Context, tenantID, userID string) (string, error) {
	query := `
		SELECT role 
		FROM tenant_members 
		WHERE tenant_id = $1::uuid AND user_id = $2;
	`
	var role string
	err := r.db.QueryRowContext(ctx, query, tenantID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Fallback to checking users table for legacy backward compatibility
			var fallbackRole string
			fErr := r.db.QueryRowContext(ctx, `SELECT role FROM users WHERE id = $1 AND tenant_id = $2::uuid;`, userID, tenantID).Scan(&fallbackRole)
			if fErr == nil {
				return fallbackRole, nil
			}
			return "", nil
		}
		return "", fmt.Errorf("failed to query member role: %w", err)
	}
	return role, nil
}

// AddMember attaches an authenticated user to a workspace
func (r *PostgresIdentityRepository) AddMember(ctx context.Context, tenantID, userID, role string) error {
	query := `
		INSERT INTO tenant_members (tenant_id, user_id, role)
		VALUES ($1::uuid, $2, $3)
		ON CONFLICT (tenant_id, user_id) 
		DO UPDATE SET role = EXCLUDED.role, updated_at = CURRENT_TIMESTAMP;
	`
	_, err := r.db.ExecContext(ctx, query, tenantID, userID, role)
	if err != nil {
		return fmt.Errorf("failed to add tenant member: %w", err)
	}
	return nil
}

// UpdateMemberRole changes a member's role
func (r *PostgresIdentityRepository) UpdateMemberRole(ctx context.Context, tenantID, memberID, newRole string) error {
	query := `
		UPDATE tenant_members
		SET role = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2::uuid AND tenant_id = $3::uuid;
	`
	res, err := r.db.ExecContext(ctx, query, newRole, memberID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to update member role: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("member not found in workspace")
	}
	return nil
}

// RemoveMember removes a user from a workspace
func (r *PostgresIdentityRepository) RemoveMember(ctx context.Context, tenantID, memberID string) error {
	query := `
		DELETE FROM tenant_members
		WHERE id = $1::uuid AND tenant_id = $2::uuid;
	`
	res, err := r.db.ExecContext(ctx, query, memberID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete tenant member: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("member not found in workspace")
	}
	return nil
}

// CountOwners returns the number of owners remaining in a workspace
func (r *PostgresIdentityRepository) CountOwners(ctx context.Context, tenantID string) (int, error) {
	query := `
		SELECT COUNT(*) 
		FROM tenant_members 
		WHERE tenant_id = $1::uuid AND role = 'owner';
	`
	var count int
	if err := r.db.QueryRowContext(ctx, query, tenantID).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count owners: %w", err)
	}
	return count, nil
}

// CreateInvitation writes a new invite record to the database
func (r *PostgresIdentityRepository) CreateInvitation(ctx context.Context, inv *domain.TeamInvitation) error {
	query := `
		INSERT INTO team_invitations (tenant_id, email, role, token, invited_by, status, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, created_at, updated_at;
	`
	return r.db.QueryRowContext(
		ctx, query,
		inv.TenantID, inv.Email, inv.Role, inv.Token, inv.InvitedBy, inv.Status, inv.ExpiresAt,
	).Scan(&inv.ID, &inv.CreatedAt, &inv.UpdatedAt)
}

// FindInvitationByToken fetches an active invitation by its cryptographic token
func (r *PostgresIdentityRepository) FindInvitationByToken(ctx context.Context, token string) (*domain.TeamInvitation, error) {
	query := `
		SELECT 
			ti.id::text, 
			ti.tenant_id::text, 
			ti.email, 
			ti.role, 
			ti.token, 
			ti.invited_by, 
			COALESCE(u.email, '') as inviter_email,
			ti.status, 
			ti.expires_at, 
			ti.created_at, 
			ti.updated_at
		FROM team_invitations ti
		LEFT JOIN users u ON ti.invited_by = u.id
		WHERE ti.token = $1;
	`
	var inv domain.TeamInvitation
	err := r.db.QueryRowContext(ctx, query, token).Scan(
		&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Token, &inv.InvitedBy,
		&inv.InviterEmail, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to fetch invitation: %w", err)
	}
	return &inv, nil
}

// ListInvitations returns all pending invitations for a workspace
func (r *PostgresIdentityRepository) ListInvitations(ctx context.Context, tenantID string) ([]domain.TeamInvitation, error) {
	query := `
		SELECT 
			ti.id::text, 
			ti.tenant_id::text, 
			ti.email, 
			ti.role, 
			ti.invited_by, 
			COALESCE(u.email, '') as inviter_email,
			ti.status, 
			ti.expires_at, 
			ti.created_at, 
			ti.updated_at
		FROM team_invitations ti
		LEFT JOIN users u ON ti.invited_by = u.id
		WHERE ti.tenant_id = $1::uuid AND ti.status = 'pending' AND ti.expires_at > CURRENT_TIMESTAMP
		ORDER BY ti.created_at DESC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list invitations: %w", err)
	}
	defer rows.Close()

	var invitations []domain.TeamInvitation
	for rows.Next() {
		var inv domain.TeamInvitation
		if err := rows.Scan(
			&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.InvitedBy,
			&inv.InviterEmail, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan invitation: %w", err)
		}
		invitations = append(invitations, inv)
	}
	return invitations, nil
}

// RevokeInvitation invalidates a pending invitation
func (r *PostgresIdentityRepository) RevokeInvitation(ctx context.Context, tenantID, invitationID string) error {
	query := `
		UPDATE team_invitations
		SET status = 'revoked', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1::uuid AND tenant_id = $2::uuid AND status = 'pending';
	`
	res, err := r.db.ExecContext(ctx, query, invitationID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to revoke invitation: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("pending invitation not found")
	}
	return nil
}

// AcceptInvitation atomically joins the user to the workspace and marks the invite as accepted
func (r *PostgresIdentityRepository) AcceptInvitation(ctx context.Context, token, userID string) (*domain.Tenant, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 1. Fetch and lock invitation
	var inv domain.TeamInvitation
	invQuery := `
		SELECT id::text, tenant_id::text, email, role, status, expires_at
		FROM team_invitations
		WHERE token = $1
		FOR UPDATE;
	`
	err = tx.QueryRowContext(ctx, invQuery, token).Scan(
		&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Status, &inv.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("invitation not found")
		}
		return nil, fmt.Errorf("failed to lock invitation: %w", err)
	}

	if inv.Status != "pending" {
		return nil, fmt.Errorf("invitation has already been %s", inv.Status)
	}
	if time.Now().After(inv.ExpiresAt) {
		_, _ = tx.ExecContext(ctx, `UPDATE team_invitations SET status = 'expired' WHERE id = $1::uuid`, inv.ID)
		_ = tx.Commit()
		return nil, fmt.Errorf("invitation has expired")
	}

	// 2. Insert or update tenant membership
	memberQuery := `
		INSERT INTO tenant_members (tenant_id, user_id, role)
		VALUES ($1::uuid, $2, $3)
		ON CONFLICT (tenant_id, user_id) 
		DO UPDATE SET role = EXCLUDED.role, updated_at = CURRENT_TIMESTAMP;
	`
	if _, err := tx.ExecContext(ctx, memberQuery, inv.TenantID, userID, inv.Role); err != nil {
		return nil, fmt.Errorf("failed to add member to workspace: %w", err)
	}

	// 3. Update user's active tenant to this workspace
	_, _ = tx.ExecContext(ctx, `UPDATE users SET tenant_id = $1::uuid, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, inv.TenantID, userID)

	// 4. Mark invitation accepted
	if _, err := tx.ExecContext(ctx, `UPDATE team_invitations SET status = 'accepted', updated_at = CURRENT_TIMESTAMP WHERE id = $1::uuid`, inv.ID); err != nil {
		return nil, fmt.Errorf("failed to update invitation status: %w", err)
	}

	// 5. Fetch joined tenant details
	var t domain.Tenant
	tenantQuery := `
		SELECT id::text, company_name, onboarding_completed, created_at, updated_at
		FROM tenants
		WHERE id = $1::uuid;
	`
	if err := tx.QueryRowContext(ctx, tenantQuery, inv.TenantID).Scan(
		&t.ID, &t.CompanyName, &t.OnboardingCompleted, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to fetch joined tenant: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &t, nil
}
