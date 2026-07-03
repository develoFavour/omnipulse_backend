package usecase

import (
	"context"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
	"strings"
)

// ContactUseCase implements domain.ContactUseCase and orchestrates the business rules
type ContactUseCase struct {
	repo domain.ContactRepository
}

// NewContactUseCase injects our driven database port interface
func NewContactUseCase(repo domain.ContactRepository) domain.ContactUseCase {
	return &ContactUseCase{repo: repo}
}

// FetchContact orchestrates the retrieval of an audience profile
func (u *ContactUseCase) FetchContact(ctx context.Context, id string) (*domain.Contact, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: contact ID cannot be blank", domain.ErrInvalidContact)
	}

	// Delegate directly to the persistence adapter layer
	return u.repo.GetByID(ctx, id)
}

// RegisterContact enforces data validation invariants before saving a user profile
func (u *ContactUseCase) RegisterContact(ctx context.Context, c *domain.Contact) error {
	// 1. Structural Sanity Validations (Domain Invariants)
	c.FirstName = strings.TrimSpace(c.FirstName)
	if c.FirstName == "" {
		return fmt.Errorf("%w: first name is a mandatory field", domain.ErrInvalidContact)
	}

	// 2. Multi-Platform Identity Invariant Rule:
	// A unified contact MUST possess at least one reachable platform communication key.
	hasWhatsApp := c.WhatsAppPhone != nil && strings.TrimSpace(*c.WhatsAppPhone) != ""
	hasTelegram := c.TelegramChatID != nil && *c.TelegramChatID != 0
	hasX := c.XUsername != nil && strings.TrimSpace(*c.XUsername) != ""

	if !hasWhatsApp && !hasTelegram && !hasX {
		return fmt.Errorf("%w: contact must provide at least one platform key (WhatsApp, Telegram, or X)", domain.ErrInvalidContact)
	}

	// Sanitize string inputs if they exist to prevent whitespace database pollution
	if hasWhatsApp {
		cleaned := strings.TrimSpace(*c.WhatsAppPhone)
		c.WhatsAppPhone = &cleaned
	}
	if hasX {
		cleaned := strings.TrimSpace(*c.XUsername)
		c.XUsername = &cleaned
	}

	// 3. Commit pure sanitized domain model downstream to the database layer
	return u.repo.Create(ctx, c)
}

// GetAllContacts computes the pagination boundaries for mass reads
func (u *ContactUseCase) GetAllContacts(ctx context.Context, page, pageSize int) ([]*domain.Contact, error) {
	// Defend against malicious or invalid pagination payloads from the web
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20 // Enforce a safe maximum default block size
	}

	// Math Formula: Translate user-friendly page numbers into low-level SQL database parameters
	limit := pageSize
	offset := (page - 1) * pageSize

	return u.repo.List(ctx, limit, offset)
}
