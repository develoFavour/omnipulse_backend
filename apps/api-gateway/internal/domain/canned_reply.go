package domain

import (
	"context"
	"time"
)

type CannedReply struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Shortcut  string    `json:"shortcut"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CannedReplyRepository interface {
	Create(ctx context.Context, reply *CannedReply) error
	ListByTenant(ctx context.Context, tenantID string) ([]*CannedReply, error)
	GetByID(ctx context.Context, tenantID, id string) (*CannedReply, error)
	Update(ctx context.Context, reply *CannedReply) error
	Delete(ctx context.Context, tenantID, id string) error
}

type CannedReplyUseCase interface {
	Create(ctx context.Context, tenantID, shortcut, title, body, createdBy string) (*CannedReply, error)
	List(ctx context.Context, tenantID string) ([]*CannedReply, error)
	Update(ctx context.Context, tenantID, id, shortcut, title, body string) (*CannedReply, error)
	Delete(ctx context.Context, tenantID, id string) error
}
