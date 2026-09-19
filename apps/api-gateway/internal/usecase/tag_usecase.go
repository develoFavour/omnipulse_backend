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
