package usecase

import (
	"context"
	"omnipulse/apps/api-gateway/internal/domain"
)

type DashboardUseCase struct {
	dashboardRepo domain.DashboardRepository
}

func NewDashboardUseCase(dashboardRepo domain.DashboardRepository) *DashboardUseCase {
	return &DashboardUseCase{
		dashboardRepo: dashboardRepo,
	}
}

func (u *DashboardUseCase) GetStats(ctx context.Context, tenantID string) (*domain.DashboardStats, error) {
	return u.dashboardRepo.GetStats(ctx, tenantID)
}

func (u *DashboardUseCase) ListDeliveries(ctx context.Context, tenantID string, limit, offset int) ([]domain.DashboardDeliveryActivity, error) {
	return u.dashboardRepo.ListDeliveries(ctx, tenantID, limit, offset)
}
