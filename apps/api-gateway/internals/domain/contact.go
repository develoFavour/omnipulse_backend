package domain

import (
	"context"
	"errors"
	"time"
)

// Sentinel domain errors for consistent transport-layer mapping
var (
	ErrContactNotFound = errors.New("contact not found")
	ErrInvalidContact  = errors.New("contact validation failed")
)

// Contact represents the unified multi-platform user profile
type Contact struct {
	ID             string    `json: "id"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name,omitempty"`
	WhatsAppPhone  *string   `json:"whatsapp_phone,omitempty"` // Pointer handles nullable DB columns
	TelegramChatID *int64    `json:"telegram_chat_id,omitempty"`
	XUsername      *string   `json:"x_username,omitempty"`
	IsOptedIn      bool      `json:"is_opted_in"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ContactRepository defines the storage operations (Driven/Outbound Port)
type ContactRepository interface {
	GetByID(ctx context.Context, id string) (*Contact, error)
	Create(ctx context.Context, contact *Contact) error
	List(ctx context.Context, limit, offset int) ([]*Contact, error)
}

// ContactUseCase defines the business rules orchestration (Driving/Inbound Port)
type ContactUseCase interface {
	FetchContact(ctx context.Context, id string) (*Contact, error)
	RegisterContact(ctx context.Context, contact *Contact) error
	GetAllContacts(ctx context.Context, page, pageSize int) ([]*Contact, error)
}
