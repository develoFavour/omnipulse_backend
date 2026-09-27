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
	Tags         []*Tag    `json:"tags"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ContactFilter encapsulates multi-dimensional search, tag, and sorting vectors
type ContactFilter struct {
	Channel  string `json:"channel"`
	TagID    string `json:"tag_id"`
	Search   string `json:"search"`
	SortKey  string `json:"sort_key"`
	SortDir  string `json:"sort_dir"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Limit    int    `json:"limit"`
	Offset   int    `json:"offset"`
}

// PaginatedContacts wraps a sliced collection with rich pagination metadata
type PaginatedContacts struct {
	Data       []*Contact `json:"data"`
	Total      int        `json:"total"`
	Page       int        `json:"page"`
	PageSize   int        `json:"pageSize"`
	TotalPages int        `json:"totalPages"`
}

// ContactRepository defines the storage operations (Driven/Outbound Port)
type ContactRepository interface {
	GetByID(ctx context.Context, tenantID, id string) (*Contact, error)
	Create(ctx context.Context, contact *Contact) error
	ListByTenant(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*Contact, error)
	ListWithFilter(ctx context.Context, tenantID string, filter ContactFilter) ([]*Contact, int, error)
}

// ContactUseCase defines the business rules orchestration (Driving/Inbound Port)
type ContactUseCase interface {
	FetchContact(ctx context.Context, tenantID, id string) (*Contact, error)
	RegisterContact(ctx context.Context, contact *Contact) error
	GetAllContacts(ctx context.Context, tenantID string, filter ContactFilter) (*PaginatedContacts, error)
}
