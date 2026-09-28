package usecase

import (
	"context"

	"omnipulse/apps/api-gateway/internal/domain"
)

type NotificationUseCase struct {
	repo domain.NotificationRepository
}

func NewNotificationUseCase(repo domain.NotificationRepository) domain.NotificationUseCase {
	return &NotificationUseCase{repo: repo}
}

func (u *NotificationUseCase) Create(ctx context.Context, n *domain.Notification) error {
	return u.repo.Create(ctx, n)
}

func (u *NotificationUseCase) List(ctx context.Context, tenantID string, limit int) ([]*domain.Notification, error) {
	return u.repo.ListForTenant(ctx, tenantID, limit)
}

func (u *NotificationUseCase) UnreadCount(ctx context.Context, tenantID string) (int, error) {
	return u.repo.UnreadCount(ctx, tenantID)
}

func (u *NotificationUseCase) MarkRead(ctx context.Context, tenantID, id string) error {
	return u.repo.MarkRead(ctx, tenantID, id)
}

func (u *NotificationUseCase) MarkAllRead(ctx context.Context, tenantID string) error {
	return u.repo.MarkAllRead(ctx, tenantID)
}
