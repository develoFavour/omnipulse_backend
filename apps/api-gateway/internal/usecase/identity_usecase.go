package usecase

import (
	"context"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
)

type IdentityUseCase struct {
	repo     domain.IdentityRepository
	chanRepo domain.ChannelRepository
}

func NewIdentityUseCase(repo domain.IdentityRepository, chanRepo domain.ChannelRepository) *IdentityUseCase {
	return &IdentityUseCase{repo: repo, chanRepo: chanRepo}
}

type SyncResult struct {
	Tenant              *domain.Tenant `json:"tenant"`
	User                *domain.User   `json:"user"`
	OnboardingCompleted bool           `json:"onboarding_completed"`
}

func (u *IdentityUseCase) SyncUser(ctx context.Context, clerkUserID, email string) (*SyncResult, error) {
	user, err := u.repo.FindUserByClerkID(ctx, clerkUserID)
	if err != nil {
		return nil, err
	}

	if user != nil {
		// Existing user, load tenant
		tenant, err := u.repo.FindTenantByID(ctx, user.TenantID)
		if err != nil {
			return nil, err
		}
		return &SyncResult{
			Tenant:              tenant,
			User:                user,
			OnboardingCompleted: tenant.OnboardingCompleted,
		}, nil
	}

	// JIT Provisioning: New User -> New Tenant
	newTenant := &domain.Tenant{
		CompanyName:         "My Workspace",
		OnboardingCompleted: false,
	}
	newUser := &domain.User{
		ID:    clerkUserID,
		Email: email,
		Role:  "admin",
	}

	if err := u.repo.CreateTenantWithUser(ctx, newTenant, newUser); err != nil {
		return nil, err
	}

	return &SyncResult{
		Tenant:              newTenant,
		User:                newUser,
		OnboardingCompleted: false,
	}, nil
}

func (u *IdentityUseCase) UpdateBrandName(ctx context.Context, tenantID string, name string) error {
	if name == "" {
		return fmt.Errorf("company name cannot be empty")
	}
	return u.repo.UpdateTenantName(ctx, tenantID, name)
}

func (u *IdentityUseCase) CompleteOnboarding(ctx context.Context, tenantID string) error {
	count, err := u.chanRepo.CountActiveByTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("cannot complete onboarding without at least one active channel")
	}

	return u.repo.SetOnboardingCompleted(ctx, tenantID)
}
