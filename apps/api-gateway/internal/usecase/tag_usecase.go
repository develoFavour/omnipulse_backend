package usecase

import (
	"context"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
	"strings"
)

type TagUseCase struct {
	tagRepo domain.TagRepository
}

func NewTagUseCase(tagRepo domain.TagRepository) domain.TagUseCase {
	return &TagUseCase{
		tagRepo: tagRepo,
	}
}

func (u *TagUseCase) CreateTag(ctx context.Context, tenantID, name, color string) (*domain.Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tag name cannot be empty")
	}
	if color == "" {
		color = "#6366f1"
	}

	tag := &domain.Tag{
		TenantID: tenantID,
		Name:     name,
		Color:    color,
	}

	if err := u.tagRepo.Create(ctx, tag); err != nil {
		return nil, err
	}

	return tag, nil
}

func (u *TagUseCase) ListTags(ctx context.Context, tenantID string) ([]*domain.Tag, error) {
	return u.tagRepo.ListByTenant(ctx, tenantID)
}

func (u *TagUseCase) DeleteTag(ctx context.Context, tenantID, id string) error {
	return u.tagRepo.Delete(ctx, tenantID, id)
}

func (u *TagUseCase) TagContact(ctx context.Context, tenantID, contactID, tagID string) error {
	return u.tagRepo.AssignTagToContact(ctx, tenantID, contactID, tagID)
}

func (u *TagUseCase) UntagContact(ctx context.Context, tenantID, contactID, tagID string) error {
	return u.tagRepo.RemoveTagFromContact(ctx, tenantID, contactID, tagID)
}

func (u *TagUseCase) GetTagsForContacts(ctx context.Context, tenantID string, contactIDs []string) (map[string][]*domain.Tag, error) {
	return u.tagRepo.GetTagsByContactIDs(ctx, tenantID, contactIDs)
}

// BulkTagContacts assigns or removes a tag from multiple contacts at once.
// action must be "assign" or "remove".
func (u *TagUseCase) BulkTagContacts(ctx context.Context, tenantID, tagID string, contactIDs []string, action string) error {
	if len(contactIDs) == 0 {
		return fmt.Errorf("contact_ids cannot be empty")
	}
	switch action {
	case "assign":
		return u.tagRepo.AssignTagToContacts(ctx, tenantID, tagID, contactIDs)
	case "remove":
		return u.tagRepo.RemoveTagFromContacts(ctx, tenantID, tagID, contactIDs)
	default:
		return fmt.Errorf("invalid action %q: must be 'assign' or 'remove'", action)
	}
}

// UpdateTag renames or recolors an existing tag.
func (u *TagUseCase) UpdateTag(ctx context.Context, tenantID, id, name, color string) (*domain.Tag, error) {
	tag, err := u.tagRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if name != "" {
		tag.Name = strings.TrimSpace(name)
	}
	if color != "" {
		tag.Color = color
	}
	if err := u.tagRepo.Update(ctx, tag); err != nil {
		return nil, err
	}
	return tag, nil
}
