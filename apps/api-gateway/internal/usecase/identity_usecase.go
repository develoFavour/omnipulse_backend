package usecase

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type IdentityUseCase struct {
	repo        domain.IdentityRepository
	chanRepo    domain.ChannelRepository
	provisionMu sync.Mutex
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
	// Several page requests can arrive simultaneously on a fresh session.
	// Serialize JIT provisioning so only one request creates the tenant/user;
	// following requests then find the user normally.
	u.provisionMu.Lock()
	defer u.provisionMu.Unlock()

	// If email is missing or placeholder, resolve verified email directly from Clerk API
	if email == "" || strings.Contains(email, "@placeholder.com") {
		if clerkEmail, _ := utils.ResolveClerkUserEmailAndName(ctx, clerkUserID); clerkEmail != "" {
			email = clerkEmail
		}
	}

	user, err := u.repo.FindUserByClerkID(ctx, clerkUserID)
	if err != nil {
		return nil, err
	}

	if user != nil {
		// If existing user has placeholder email but we now have real email, persist it immediately
		if email != "" && !strings.Contains(email, "@placeholder.com") && user.Email != email {
			if updateErr := u.repo.UpdateUserEmail(ctx, user.ID, email); updateErr == nil {
				user.Email = email
			}
		}

		// Existing user, load tenant
		tenant, err := u.repo.FindTenantByID(ctx, user.TenantID)
		if err != nil {
			return nil, err
		}
		if tenant == nil {
			return nil, fmt.Errorf("tenant %q referenced by user %q was not found", user.TenantID, clerkUserID)
		}

		// Resolve the authoritative workspace role from tenant_members.
		// If no membership row exists (e.g. legacy accounts provisioned before RBAC was introduced),
		// fall back to users.role and self-heal by upserting the membership record.
		activeRole, roleErr := u.repo.GetMemberRole(ctx, user.TenantID, user.ID)
		if roleErr == nil && activeRole != "" {
			user.Role = activeRole
		} else if activeRole == "" {
			// Self-heal: persist the membership row based on the users.role value so the
			// mismatch is resolved on first login and every subsequent read returns correctly.
			healRole := user.Role
			if healRole == "" {
				healRole = "member"
			}
			if addErr := u.repo.AddMember(ctx, user.TenantID, user.ID, healRole); addErr == nil {
				user.Role = healRole
			}
		}

		return &SyncResult{
			Tenant:              tenant,
			User:                user,
			OnboardingCompleted: tenant.OnboardingCompleted,
		}, nil
	}

	// JIT Provisioning: New User -> New Tenant
	if email == "" {
		email = clerkUserID + "@placeholder.com"
	}

	newTenant := &domain.Tenant{
		CompanyName:         "My Workspace",
		OnboardingCompleted: false,
	}
	newUser := &domain.User{
		ID:    clerkUserID,
		Email: email,
		Role:  "owner",
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
