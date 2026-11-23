package usecase

import (
	"context"
	"fmt"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
)

type cannedReplyUseCase struct {
	repo domain.CannedReplyRepository
}

func NewCannedReplyUseCase(repo domain.CannedReplyRepository) domain.CannedReplyUseCase {
	return &cannedReplyUseCase{repo: repo}
}

func (uc *cannedReplyUseCase) Create(ctx context.Context, tenantID, shortcut, title, body, createdBy string) (*domain.CannedReply, error) {
	shortcut = strings.ToLower(strings.TrimSpace(shortcut))
	shortcut = strings.TrimPrefix(shortcut, "/")
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if shortcut == "" || title == "" || body == "" {
		return nil, fmt.Errorf("shortcut, title, and body are required")
	}
	r := &domain.CannedReply{
		TenantID:  tenantID,
		Shortcut:  shortcut,
		Title:     title,
		Body:      body,
		CreatedBy: createdBy,
	}
	if err := uc.repo.Create(ctx, r); err != nil {
		return nil, fmt.Errorf("failed to create canned reply: %w", err)
	}
	return r, nil
}

func (uc *cannedReplyUseCase) List(ctx context.Context, tenantID string) ([]*domain.CannedReply, error) {
	return uc.repo.ListByTenant(ctx, tenantID)
}

func (uc *cannedReplyUseCase) Update(ctx context.Context, tenantID, id, shortcut, title, body string) (*domain.CannedReply, error) {
	shortcut = strings.ToLower(strings.TrimSpace(shortcut))
	shortcut = strings.TrimPrefix(shortcut, "/")
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if shortcut == "" || title == "" || body == "" {
		return nil, fmt.Errorf("shortcut, title, and body are required")
	}
	r, err := uc.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, fmt.Errorf("canned reply not found: %w", err)
	}
	r.Shortcut = shortcut
	r.Title = title
	r.Body = body
	if err := uc.repo.Update(ctx, r); err != nil {
		return nil, fmt.Errorf("failed to update canned reply: %w", err)
	}
	return r, nil
}

func (uc *cannedReplyUseCase) Delete(ctx context.Context, tenantID, id string) error {
	return uc.repo.Delete(ctx, tenantID, id)
}
