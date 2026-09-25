package domain

import (
	"context"
	"time"
)

type Template struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Title     string    `json:"title"`
	Category  string    `json:"category"`
	Body      string    `json:"body"`
	MediaURL  *string   `json:"media_url,omitempty"`
	Variables []string  `json:"variables"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TemplateRepository interface {
	Create(ctx context.Context, tmpl *Template) error
	ListByTenant(ctx context.Context, tenantID, category string) ([]*Template, error)
	GetByID(ctx context.Context, tenantID, id string) (*Template, error)
	Update(ctx context.Context, tmpl *Template) error
	Delete(ctx context.Context, tenantID, id string) error
}

type TemplateUseCase interface {
	CreateTemplate(ctx context.Context, tenantID, title, category, body string, mediaURL *string, variables []string) (*Template, error)
	ListTemplates(ctx context.Context, tenantID, category string) ([]*Template, error)
	GetTemplate(ctx context.Context, tenantID, id string) (*Template, error)
	UpdateTemplate(ctx context.Context, tenantID, id, title, category, body string, mediaURL *string, variables []string) (*Template, error)
	DeleteTemplate(ctx context.Context, tenantID, id string) error
}
