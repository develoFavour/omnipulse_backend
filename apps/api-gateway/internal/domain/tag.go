package domain

import (
	"context"
	"time"
)

type Tag struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Name         string    `json:"name"`
	Color        string    `json:"color"`
	ContactCount int       `json:"contact_count,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TagRepository interface {
	Create(ctx context.Context, tag *Tag) error
	ListByTenant(ctx context.Context, tenantID string) ([]*Tag, error)
	GetByID(ctx context.Context, tenantID, id string) (*Tag, error)
	Delete(ctx context.Context, tenantID, id string) error
	AssignTagToContact(ctx context.Context, tenantID, contactID, tagID string) error
	RemoveTagFromContact(ctx context.Context, tenantID, contactID, tagID string) error
	GetTagsByContactIDs(ctx context.Context, tenantID string, contactIDs []string) (map[string][]*Tag, error)
}

type TagUseCase interface {
	CreateTag(ctx context.Context, tenantID, name, color string) (*Tag, error)
	ListTags(ctx context.Context, tenantID string) ([]*Tag, error)
	DeleteTag(ctx context.Context, tenantID, id string) error
	TagContact(ctx context.Context, tenantID, contactID, tagID string) error
	UntagContact(ctx context.Context, tenantID, contactID, tagID string) error
	GetTagsForContacts(ctx context.Context, tenantID string, contactIDs []string) (map[string][]*Tag, error)
}
