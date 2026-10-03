package domain

import (
	"context"
	"encoding/json"
	"time"
)

// Tenant represents an isolated business or personal creator workspace
type Tenant struct {
	ID                  string    `json:"id"`
	CompanyName         string    `json:"company_name"`
	OnboardingCompleted bool      `json:"onboarding_completed"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// User represents an authenticated individual tied to a specific workspace
type User struct {
	ID        string    `json:"id"` // Maps directly to Clerk External ID
	TenantID  string    `json:"tenant_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"` // "admin" or "member"
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TenantChannel represents an omnichannel credential container
type TenantChannel struct {
	ID                   string          `json:"id"`
	TenantID             string          `json:"tenant_id"`
	PlatformName         string          `json:"platform_name"`
	SenderIdentity       string          `json:"sender_identity"`
	EncryptedCredentials json.RawMessage `json:"encrypted_credentials"`
	Status               string          `json:"status"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// UserWorkspace represents a workspace the user belongs to, with role and active status
type UserWorkspace struct {
	ID                  string    `json:"id"`
	CompanyName         string    `json:"company_name"`
	Role                string    `json:"role"`
	IsActive            bool      `json:"is_active"`
	OnboardingCompleted bool      `json:"onboarding_completed"`
	CreatedAt           time.Time `json:"created_at"`
}

// TenantMember represents a user's membership and permissions within a workspace
type TenantMember struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"` // "owner", "admin", "member"
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TeamInvitation represents a pending, accepted, or revoked invitation
type TeamInvitation struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Email        string    `json:"email"`
	Role         string    `json:"role"` // "admin", "member"
	Token        string    `json:"token,omitempty"`
	InvitedBy    string    `json:"invited_by"`
	InviterEmail string    `json:"inviter_email,omitempty"`
	Status       string    `json:"status"` // "pending", "accepted", "revoked", "expired"
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IdentityRepository defines data access for Tenants, Users, Memberships, and Invitations
type IdentityRepository interface {
	FindUserByClerkID(ctx context.Context, clerkID string) (*User, error)
	FindTenantByID(ctx context.Context, tenantID string) (*Tenant, error)
	CreateTenantWithUser(ctx context.Context, tenant *Tenant, user *User) error
	UpdateTenantName(ctx context.Context, tenantID string, name string) error
	UpdateUserEmail(ctx context.Context, userID string, email string) error
	SetOnboardingCompleted(ctx context.Context, tenantID string) error

	// Multi-workspace & Team Memberships
	ListMembers(ctx context.Context, tenantID string) ([]TenantMember, error)
	GetMemberRole(ctx context.Context, tenantID, userID string) (string, error)
	AddMember(ctx context.Context, tenantID, userID, role string) error
	UpdateMemberRole(ctx context.Context, tenantID, memberID, newRole string) error
	RemoveMember(ctx context.Context, tenantID, memberID string) error
	CountOwners(ctx context.Context, tenantID string) (int, error)

	// Team Invitations
	CreateInvitation(ctx context.Context, inv *TeamInvitation) error
	FindInvitationByToken(ctx context.Context, token string) (*TeamInvitation, error)
	ListInvitations(ctx context.Context, tenantID string) ([]TeamInvitation, error)
	RevokeInvitation(ctx context.Context, tenantID, invitationID string) error
	AcceptInvitation(ctx context.Context, token, userID string) (*Tenant, error)

	// Multi-workspace management & switching
	ListUserWorkspaces(ctx context.Context, userID string) ([]UserWorkspace, error)
	SwitchUserWorkspace(ctx context.Context, userID, targetTenantID string) (*Tenant, string, error)
	CreateWorkspace(ctx context.Context, userID, companyName string) (*Tenant, error)
}

// ChannelRepository defines data access for Workspace channels
type ChannelRepository interface {
	CreateChannel(ctx context.Context, channel *TenantChannel) error
	ListByTenant(ctx context.Context, tenantID string) ([]TenantChannel, error)
	CountActiveByTenant(ctx context.Context, tenantID string) (int, error)
	FindActiveByPlatform(ctx context.Context, tenantID, platform string) (*TenantChannel, error)
	DeleteByPlatform(ctx context.Context, tenantID, platform string) error
}
