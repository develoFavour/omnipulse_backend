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

// IdentityRepository defines data access for Tenants and Users
type IdentityRepository interface {
	FindUserByClerkID(ctx context.Context, clerkID string) (*User, error)
	FindTenantByID(ctx context.Context, tenantID string) (*Tenant, error)
	CreateTenantWithUser(ctx context.Context, tenant *Tenant, user *User) error
	UpdateTenantName(ctx context.Context, tenantID string, name string) error
	SetOnboardingCompleted(ctx context.Context, tenantID string) error
}

// ChannelRepository defines data access for Workspace channels
type ChannelRepository interface {
	CreateChannel(ctx context.Context, channel *TenantChannel) error
	ListByTenant(ctx context.Context, tenantID string) ([]TenantChannel, error)
	CountActiveByTenant(ctx context.Context, tenantID string) (int, error)
	FindActiveByPlatform(ctx context.Context, tenantID, platform string) (*TenantChannel, error)
}
