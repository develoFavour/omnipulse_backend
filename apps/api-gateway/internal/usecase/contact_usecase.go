package usecase

import (
	"context"
	"fmt"
	"math"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
)

// ContactUseCase implements domain.ContactUseCase and orchestrates the business rules
type ContactUseCase struct {
	repo    domain.ContactRepository
	tagRepo domain.TagRepository
	subRepo domain.SubscriptionRepository
}

// NewContactUseCase injects our driven database port interface, tag repository, and subscription repository
func NewContactUseCase(repo domain.ContactRepository, tagRepo domain.TagRepository, subRepo domain.SubscriptionRepository) domain.ContactUseCase {
	return &ContactUseCase{
		repo:    repo,
		tagRepo: tagRepo,
		subRepo: subRepo,
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

// RegisterContact enforces data validation invariants and plan capacity limits before saving a user profile
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

	// Enforce Plan Contact Limit
	if u.subRepo != nil && c.TenantID != "" {
		sub, err := u.subRepo.GetByTenantID(ctx, c.TenantID)
		if err == nil && sub != nil && sub.ContactsLimit != -1 {
			_, total, err := u.repo.ListWithFilter(ctx, c.TenantID, domain.ContactFilter{Page: 1, PageSize: 1, Limit: 1})
			if err == nil && total >= sub.ContactsLimit {
				return fmt.Errorf("%w: contact capacity limit of %d reached for %s plan. Upgrade to increase capacity.", domain.ErrQuotaExceeded, sub.ContactsLimit, sub.PlanTier)
			}
		}
	}

	if c.Source == "" {
		c.Source = "manual"
	}
	c.Status = "active"

	// Commit pure sanitized domain model downstream to the database layer
	return u.repo.Create(ctx, c)
}

// GetAllContacts computes the pagination boundaries and filters for mass reads
func (u *ContactUseCase) GetAllContacts(ctx context.Context, tenantID string, filter domain.ContactFilter) (*domain.PaginatedContacts, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 500 {
		filter.PageSize = 50
	}

	filter.Limit = filter.PageSize
	filter.Offset = (filter.Page - 1) * filter.PageSize

	contacts, total, err := u.repo.ListWithFilter(ctx, tenantID, filter)
	if err != nil {
		return nil, err
	}

	if u.tagRepo != nil && len(contacts) > 0 {
		contactIDs := make([]string, len(contacts))
		for i, c := range contacts {
			contactIDs[i] = c.ID
			c.Tags = []*domain.Tag{} // default empty slice instead of null
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

	totalPages := 0
	if total > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(filter.PageSize)))
	}

	return &domain.PaginatedContacts{
		Data:       contacts,
		Total:      total,
		Page:       filter.Page,
		PageSize:   filter.PageSize,
		TotalPages: totalPages,
	}, nil
}

func (u *ContactUseCase) StreamContacts(ctx context.Context, tenantID string, filter domain.ContactFilter, yield func(c *domain.Contact, tagNames string) error) error {
	return u.repo.StreamContacts(ctx, tenantID, filter, yield)
}
