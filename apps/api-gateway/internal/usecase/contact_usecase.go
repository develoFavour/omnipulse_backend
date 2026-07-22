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
func (u *ContactUseCase) FetchContact(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: contact ID cannot be blank", domain.ErrInvalidContact)
	}

	// Delegate directly to the persistence adapter layer
	return u.repo.GetByID(ctx, tenantID, id)
}

// RegisterContact enforces data validation invariants before saving a user profile
func (u *ContactUseCase) RegisterContact(ctx context.Context, c *domain.Contact) error {
	c.FirstName = strings.TrimSpace(c.FirstName)
	if c.FirstName == "" {
		return fmt.Errorf("%w: first name is a mandatory field", domain.ErrInvalidContact)
	}

	c.Channel = strings.TrimSpace(strings.ToLower(c.Channel))
	if c.Channel != "whatsapp" && c.Channel != "telegram" && c.Channel != "instagram" && c.Channel != "x" {
		return fmt.Errorf("%w: channel must be whatsapp, telegram, instagram, or x", domain.ErrInvalidContact)
	}

	c.RoutingValue = strings.TrimSpace(c.RoutingValue)
	if c.RoutingValue == "" {
		return fmt.Errorf("%w: routing value cannot be empty", domain.ErrInvalidContact)
	}

	if c.Source == "" {
		c.Source = "manual"
	}
	c.Status = "active"

	// Commit pure sanitized domain model downstream to the database layer
	return u.repo.Create(ctx, c)
}

// GetAllContacts computes the pagination boundaries for mass reads
func (u *ContactUseCase) GetAllContacts(ctx context.Context, tenantID, channelFilter string, page, pageSize int) ([]*domain.Contact, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20 // Enforce a safe maximum default block size
	}

	limit := pageSize
	offset := (page - 1) * pageSize

	return u.repo.ListByTenant(ctx, tenantID, channelFilter, limit, offset)
}
