package usecase

import (
	"context"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
	"strings"
)

// ContactUseCase implements domain.ContactUseCase and orchestrates the business rules
type ContactUseCase struct {
	repo    domain.ContactRepository
	tagRepo domain.TagRepository
}

// NewContactUseCase injects our driven database port interface and optional tag repository
func NewContactUseCase(repo domain.ContactRepository, tagRepo domain.TagRepository) domain.ContactUseCase {
	return &ContactUseCase{
		repo:    repo,
		tagRepo: tagRepo,
	}
}

// FetchContact orchestrates the retrieval of an audience profile
func (u *ContactUseCase) FetchContact(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: contact ID cannot be blank", domain.ErrInvalidContact)
	}

	contact, err := u.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	if u.tagRepo != nil && contact != nil {
		tagMap, err := u.tagRepo.GetTagsByContactIDs(ctx, tenantID, []string{contact.ID})
		if err == nil {
			if tags, ok := tagMap[contact.ID]; ok {
				contact.Tags = tags
			} else {
				contact.Tags = []*domain.Tag{}
			}
		}
	}

	return contact, nil
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
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 500 // Fetch all contacts in one page by default
	}

	limit := pageSize
	offset := (page - 1) * pageSize

	contacts, err := u.repo.ListByTenant(ctx, tenantID, channelFilter, limit, offset)
	if err != nil {
		return nil, err
	}

	if u.tagRepo != nil && len(contacts) > 0 {
		contactIDs := make([]string, len(contacts))
		for i, c := range contacts {
			contactIDs[i] = c.ID
			c.Tags = []*domain.Tag{} // default empty array instead of null
		}

		tagMap, err := u.tagRepo.GetTagsByContactIDs(ctx, tenantID, contactIDs)
		if err == nil {
			for _, c := range contacts {
				if tags, ok := tagMap[c.ID]; ok {
					c.Tags = tags
				}
			}
		}
	}

	return contacts, nil
}
