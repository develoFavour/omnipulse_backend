package domain

import (
	"context"
	"errors"
	"time"
)

// Sentinel domain errors
var (
	ErrContactNotFound = errors.New("contact not found")
	ErrInvalidContact  = errors.New("contact validation failed")
)

// Contact represents an audience member inside a workspace directory
type Contact struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	FirstName    string    `json:"first_name"`
	LastName     string    `json:"last_name,omitempty"`
	Channel      string    `json:"channel"`
	RoutingValue string    `json:"routing_value"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ContactRepository defines the storage operations (Driven/Outbound Port)
type ContactRepository interface {
	GetByID(ctx context.Context, tenantID, id string) (*Contact, error)
	Create(ctx context.Context, contact *Contact) error
	ListByTenant(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*Contact, error)
}

// ContactUseCase defines the business rules orchestration (Driving/Inbound Port)
type ContactUseCase interface {
	FetchContact(ctx context.Context, tenantID, id string) (*Contact, error)
	RegisterContact(ctx context.Context, contact *Contact) error
	GetAllContacts(ctx context.Context, tenantID, channelFilter string, page, pageSize int) ([]*Contact, error)
}
