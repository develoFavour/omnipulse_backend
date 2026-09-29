package usecase

import (
	"context"
	"errors"

	"omnipulse/apps/api-gateway/internal/domain"
)

type AnalyticsUseCase struct {
	repo domain.AnalyticsRepository
}

func NewAnalyticsUseCase(repo domain.AnalyticsRepository) domain.AnalyticsUseCase {
	return &AnalyticsUseCase{repo: repo}
}

func (u *AnalyticsUseCase) GetReport(ctx context.Context, tenantID string, days int) (*domain.AggregateAnalyticsReport, error) {
	if tenantID == "" {
		return nil, errors.New("tenant_id is required")
	}
	if days <= 0 {
		days = 30
	}
	return u.repo.GetAggregateReport(ctx, tenantID, days)
}
