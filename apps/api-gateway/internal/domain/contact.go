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

// ContactImportMaxRows bounds a single import so one request cannot stall the
// gateway; larger directories should be split across multiple imports.
const ContactImportMaxRows = 1000

// ContactImportRow is one parsed line from a pasted list or CSV file. Empty
// channel and consent values fall back to the request-level defaults.
type ContactImportRow struct {
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Channel      string `json:"channel"`
	RoutingValue string `json:"routing_value"`
	Consent      string `json:"consent"`
	ConsentNote  string `json:"consent_note"`
}

// ContactImportRequest carries parsed rows plus the defaults chosen by the
// operator. DefaultConsent is intentionally empty by default: an import must
// not imply consent the operator never asserted.
type ContactImportRequest struct {
	DefaultChannel string             `json:"default_channel"`
	DefaultConsent string             `json:"default_consent"`
	ConsentNote    string             `json:"consent_note"`
	Rows           []ContactImportRow `json:"rows"`
}

// ContactImportError identifies a row that could not be imported. Row is the
// 1-based position among the request rows, matching what the preview showed.
type ContactImportError struct {
	Row          int    `json:"row"`
	RoutingValue string `json:"routing_value,omitempty"`
	Message      string `json:"message"`
}

// ContactImportSummary reports what happened so the operator can reconcile the
// file against the directory.
type ContactImportSummary struct {
	Total           int                  `json:"total"`
	Created         int                  `json:"created"`
	Existing        int                  `json:"existing"`
	Duplicates      int                  `json:"duplicates"`
	Invalid         int                  `json:"invalid"`
	ConsentGranted  int                  `json:"consent_granted"`
	ConsentRevoked  int                  `json:"consent_revoked"`
	NoConsent       int                  `json:"no_consent"`
	Errors          []ContactImportError `json:"errors"`
	TruncatedErrors bool                 `json:"truncated_errors"`
}

// ContactRepository defines the storage operations (Driven/Outbound Port)
type ContactRepository interface {
	GetByID(ctx context.Context, tenantID, id string) (*Contact, error)
	GetByRoutingValue(ctx context.Context, tenantID, channel, routingValue string) (*Contact, error)
	Create(ctx context.Context, contact *Contact) error
	ListByTenant(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*Contact, error)
	ListWithFilter(ctx context.Context, tenantID string, filter ContactFilter) ([]*Contact, int, error)
	StreamContacts(ctx context.Context, tenantID string, filter ContactFilter, yield func(c *Contact, tagNames string) error) error
}

// ContactUseCase defines the business rules orchestration (Driving/Inbound Port)
type ContactUseCase interface {
	FetchContact(ctx context.Context, tenantID, id string) (*Contact, error)
	GetContactByRoutingValue(ctx context.Context, tenantID, channel, routingValue string) (*Contact, error)
	RegisterContact(ctx context.Context, contact *Contact) error
	ImportContacts(ctx context.Context, tenantID string, req ContactImportRequest) (*ContactImportSummary, error)
	GetAllContacts(ctx context.Context, tenantID string, filter ContactFilter) (*PaginatedContacts, error)
	StreamContacts(ctx context.Context, tenantID string, filter ContactFilter, yield func(c *Contact, tagNames string) error) error
}
