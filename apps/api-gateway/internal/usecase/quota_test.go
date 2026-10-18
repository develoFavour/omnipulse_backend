package usecase

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

// TestContactQuota_Enforcement tests plan contact capacity limits
func TestContactQuota_Enforcement(t *testing.T) {
	tenantID := "tenant-quota-contacts"

	t.Run("Rejects new contact when starter contact limit (500) reached", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:      tID,
					PlanTier:      domain.PlanStarter,
					ContactsLimit: 500,
				}, nil
			},
		}

		contactRepo := &mockContactRepo{
			getByRoutingValueFunc: func(ctx context.Context, tID, channel, routingValue string) (*domain.Contact, error) {
				// Contact is brand new
				return nil, nil
			},
			listWithFilterFunc: func(ctx context.Context, tID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
				// 500 existing contacts in workspace
				return nil, 500, nil
			},
			createFunc: func(ctx context.Context, contact *domain.Contact) error {
				t.Fatalf("createFunc should NOT be called when quota exceeded")
				return nil
			},
		}

		uc := NewContactUseCase(contactRepo, nil, subRepo)
		newContact := &domain.Contact{
			TenantID:     tenantID,
			FirstName:    "John",
			Channel:      "whatsapp",
			RoutingValue: "+1234567890",
		}

		err := uc.RegisterContact(context.Background(), newContact)
		if err == nil {
			t.Fatal("expected ErrQuotaExceeded, got nil")
		}
		if !errors.Is(err, domain.ErrQuotaExceeded) {
			t.Fatalf("expected error wrapping domain.ErrQuotaExceeded, got: %v", err)
		}
		if !strings.Contains(err.Error(), "contact capacity limit of 500 reached for starter plan") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("Allows update to existing contact even when contact quota is maxed out", func(t *testing.T) {
		existingID := "existing-contact-uuid-123"
		createdCalled := false

		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:      tID,
					PlanTier:      domain.PlanStarter,
					ContactsLimit: 500,
				}, nil
			},
		}

		contactRepo := &mockContactRepo{
			getByRoutingValueFunc: func(ctx context.Context, tID, channel, routingValue string) (*domain.Contact, error) {
				// Existing contact found with same routing value
				return &domain.Contact{
					ID:           existingID,
					TenantID:     tID,
					FirstName:    "Johnny",
					Channel:      channel,
					RoutingValue: routingValue,
				}, nil
			},
			listWithFilterFunc: func(ctx context.Context, tID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
				// Maxed out at 500
				return nil, 500, nil
			},
			createFunc: func(ctx context.Context, contact *domain.Contact) error {
				createdCalled = true
				if contact.ID != existingID {
					t.Errorf("expected contact ID %s preserved on upsert, got %s", existingID, contact.ID)
				}
				return nil
			},
		}

		uc := NewContactUseCase(contactRepo, nil, subRepo)
		updateContact := &domain.Contact{
			TenantID:     tenantID,
			FirstName:    "John",
			LastName:     "Updated",
			Channel:      "whatsapp",
			RoutingValue: "+1234567890",
		}

		err := uc.RegisterContact(context.Background(), updateContact)
		if err != nil {
			t.Fatalf("unexpected error updating existing contact: %v", err)
		}
		if !createdCalled {
			t.Fatal("expected createFunc (upsert) to be called for existing contact")
		}
	})

	t.Run("Permits new contact when tenant upgraded to Growth tier (5,000 capacity)", func(t *testing.T) {
		createdCalled := false

		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:      tID,
					PlanTier:      domain.PlanGrowth,
					ContactsLimit: 5000,
				}, nil
			},
		}

		contactRepo := &mockContactRepo{
			getByRoutingValueFunc: func(ctx context.Context, tID, channel, routingValue string) (*domain.Contact, error) {
				return nil, nil
			},
			listWithFilterFunc: func(ctx context.Context, tID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
				// Currently has 750 contacts (past starter 500 limit, but well under 5000)
				return nil, 750, nil
			},
			createFunc: func(ctx context.Context, contact *domain.Contact) error {
				createdCalled = true
				return nil
			},
		}

		uc := NewContactUseCase(contactRepo, nil, subRepo)
		newContact := &domain.Contact{
			TenantID:     tenantID,
			FirstName:    "Sarah",
			Channel:      "telegram",
			RoutingValue: "@sarah_tech",
		}

		err := uc.RegisterContact(context.Background(), newContact)
		if err != nil {
			t.Fatalf("unexpected error adding contact on Growth plan: %v", err)
		}
		if !createdCalled {
			t.Fatal("expected contact to be created on Growth plan")
		}
	})
}

// TestMessageQuota_Enforcement tests monthly message limits when triggering broadcast dispatches
func TestMessageQuota_Enforcement(t *testing.T) {
	tenantID := "tenant-quota-messages"
	campaignID := "camp-quota-msg-1"

	campaign := &domain.Campaign{
		ID:               campaignID,
		TenantID:         tenantID,
		Title:            "Flash Sale",
		MessageBody:      "Limited stock available!",
		Status:           "approved",
		SelectedChannels: `["whatsapp"]`,
	}

	t.Run("Rejects broadcast dispatch when monthly message limit is exhausted", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:            tID,
					PlanTier:            domain.PlanStarter,
					MonthlyMessageLimit: 1000,
					CurrentPeriodStart:  time.Now().Add(-15 * 24 * time.Hour),
				}, nil
			},
		}

		campaignRepo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return campaign, nil
			},
			countDeliveriesSinceFunc: func(ctx context.Context, tID string, since time.Time) (int, error) {
				// Already dispatched 1,000 messages this billing cycle
				return 1000, nil
			},
			setDispatchingFunc: func(ctx context.Context, tID, id string, totalTargets int) error {
				t.Fatalf("setDispatching should NOT be called when monthly quota is exhausted")
				return nil
			},
		}

		tasksEmitted := 0
		publisher := &mockPublisher{
			publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
				tasksEmitted++
				return nil
			},
		}

		uc := NewCampaignUseCase(campaignRepo, &mockContactRepo{}, &mockDestinationRepo{}, publisher, subRepo)
		err := uc.TriggerDispatch(context.Background(), tenantID, campaignID)
		if err == nil {
			t.Fatal("expected ErrQuotaExceeded for exhausted monthly limit, got nil")
		}
		if !errors.Is(err, domain.ErrQuotaExceeded) {
			t.Fatalf("expected domain.ErrQuotaExceeded, got: %v", err)
		}
		if !strings.Contains(err.Error(), "monthly broadcast limit of 1000 messages reached for starter tier") {
			t.Errorf("unexpected error message: %v", err)
		}
		if tasksEmitted != 0 {
			t.Errorf("expected 0 published tasks, got %d", tasksEmitted)
		}
	})

	t.Run("Permits broadcast dispatch when under monthly message limit", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:            tID,
					PlanTier:            domain.PlanStarter,
					MonthlyMessageLimit: 1000,
					CurrentPeriodStart:  time.Now().Add(-10 * 24 * time.Hour),
				}, nil
			},
		}

		campaignRepo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return campaign, nil
			},
			countDeliveriesSinceFunc: func(ctx context.Context, tID string, since time.Time) (int, error) {
				// Dispatched 450 out of 1000 messages
				return 450, nil
			},
			setDispatchingFunc: func(ctx context.Context, tID, id string, totalTargets int) error {
				if totalTargets != 1 {
					t.Errorf("expected 1 target set in processing, got %d", totalTargets)
				}
				return nil
			},
		}

		contactRepo := &mockContactRepo{
			listByTenantFunc: func(ctx context.Context, tID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
				if offset == 0 {
					return []*domain.Contact{
						{ID: "c-active-1", TenantID: tID, FirstName: "Alice", Channel: "whatsapp", Status: "active", RoutingValue: "+111111111"},
					}, nil
				}
				return nil, nil
			},
		}

		var mu sync.Mutex
		tasksCount := 0
		publisher := &mockPublisher{
			publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
				mu.Lock()
				tasksCount++
				mu.Unlock()
				return nil
			},
		}

		uc := NewCampaignUseCase(campaignRepo, contactRepo, &mockDestinationRepo{}, publisher, subRepo)
		err := uc.TriggerDispatch(context.Background(), tenantID, campaignID)
		if err != nil {
			t.Fatalf("unexpected error during dispatch under quota: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()
		if tasksCount != 1 {
			t.Errorf("expected 1 task published, got %d", tasksCount)
		}
	})

	t.Run("Permits broadcast dispatch when monthly limit is unlimited (-1 on Agency tier)", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:            tID,
					PlanTier:            domain.PlanAgency,
					MonthlyMessageLimit: -1, // unlimited
				}, nil
			},
		}

		campaignRepo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return campaign, nil
			},
			countDeliveriesSinceFunc: func(ctx context.Context, tID string, since time.Time) (int, error) {
				// High dispatch count (e.g. 50,000 sent)
				return 50000, nil
			},
			setDispatchingFunc: func(ctx context.Context, tID, id string, totalTargets int) error {
				return nil
			},
		}

		contactRepo := &mockContactRepo{
			listByTenantFunc: func(ctx context.Context, tID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
				if offset == 0 {
					return []*domain.Contact{
						{ID: "c-active-vip", TenantID: tID, FirstName: "VIP", Channel: "whatsapp", Status: "active", RoutingValue: "+222222222"},
					}, nil
				}
				return nil, nil
			},
		}

		tasksCount := 0
		publisher := &mockPublisher{
			publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
				tasksCount++
				return nil
			},
		}

		uc := NewCampaignUseCase(campaignRepo, contactRepo, &mockDestinationRepo{}, publisher, subRepo)
		err := uc.TriggerDispatch(context.Background(), tenantID, campaignID)
		if err != nil {
			t.Fatalf("unexpected error for unlimited message quota: %v", err)
		}
		if tasksCount != 1 {
			t.Errorf("expected 1 task published, got %d", tasksCount)
		}
	})
}

type mockTeamIdentityRepo struct {
	domain.IdentityRepository
	members []domain.TenantMember
}

func (m *mockTeamIdentityRepo) ListMembers(ctx context.Context, tenantID string) ([]domain.TenantMember, error) {
	return m.members, nil
}

func (m *mockTeamIdentityRepo) UpdateMemberRole(ctx context.Context, tenantID, memberID, newRole string) error {
	for i, mem := range m.members {
		if mem.ID == memberID {
			m.members[i].Role = newRole
			return nil
		}
	}
	return nil
}

func (m *mockTeamIdentityRepo) CreateInvitation(ctx context.Context, inv *domain.TeamInvitation) error {
	return nil
}

func TestTeamPlanQuota_Enforcement(t *testing.T) {
	tenantID := "tenant-test-team"

	t.Run("Rejects team invite when on Starter or Growth plan", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{TenantID: tID, PlanTier: domain.PlanStarter}, nil
			},
		}
		teamRepo := &mockTeamIdentityRepo{
			members: []domain.TenantMember{{ID: "m1", TenantID: tenantID, UserID: "u1", Role: "owner"}},
		}
		uc := NewTeamUseCase(teamRepo, nil, "", nil, subRepo)

		_, err := uc.InviteMember(context.Background(), tenantID, "u1", "owner", "colleague@test.com", "member")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, domain.ErrQuotaExceeded) {
			t.Fatalf("expected domain.ErrQuotaExceeded, got: %v", err)
		}
	})

	t.Run("Rejects role update when not on Agency plan", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{TenantID: tID, PlanTier: domain.PlanGrowth}, nil
			},
		}
		teamRepo := &mockTeamIdentityRepo{
			members: []domain.TenantMember{
				{ID: "m1", TenantID: tenantID, UserID: "u1", Role: "owner"},
				{ID: "m2", TenantID: tenantID, UserID: "u2", Role: "member"},
			},
		}
		uc := NewTeamUseCase(teamRepo, nil, "", nil, subRepo)

		err := uc.UpdateMemberRole(context.Background(), tenantID, "owner", "m2", "admin")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, domain.ErrQuotaExceeded) {
			t.Fatalf("expected domain.ErrQuotaExceeded, got: %v", err)
		}
	})

	t.Run("Rejects role update when user is the sole workspace member", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{TenantID: tID, PlanTier: domain.PlanAgency}, nil
			},
		}
		teamRepo := &mockTeamIdentityRepo{
			members: []domain.TenantMember{
				{ID: "m1", TenantID: tenantID, UserID: "u1", Role: "owner"},
			},
		}
		uc := NewTeamUseCase(teamRepo, nil, "", nil, subRepo)

		err := uc.UpdateMemberRole(context.Background(), tenantID, "owner", "m1", "member")
		if err == nil {
			t.Fatal("expected error for sole member, got nil")
		}
		if !strings.Contains(err.Error(), "sole workspace member") {
			t.Fatalf("expected sole workspace member error, got: %v", err)
		}
	})

	t.Run("Permits role update on Agency plan with multiple members", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{TenantID: tID, PlanTier: domain.PlanAgency}, nil
			},
		}
		teamRepo := &mockTeamIdentityRepo{
			members: []domain.TenantMember{
				{ID: "m1", TenantID: tenantID, UserID: "u1", Role: "owner"},
				{ID: "m2", TenantID: tenantID, UserID: "u2", Role: "member"},
			},
		}
		uc := NewTeamUseCase(teamRepo, nil, "", nil, subRepo)

		err := uc.UpdateMemberRole(context.Background(), tenantID, "owner", "m2", "admin")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
