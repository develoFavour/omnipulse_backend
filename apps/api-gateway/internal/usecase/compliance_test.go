package usecase

import (
	"context"
	"strings"
	"sync"
	"testing"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

type mockContactRepo struct {
	getByIDFunc           func(ctx context.Context, tenantID, id string) (*domain.Contact, error)
	getByRoutingValueFunc func(ctx context.Context, tenantID, channel, routingValue string) (*domain.Contact, error)
	createFunc            func(ctx context.Context, contact *domain.Contact) error
	listByTenantFunc      func(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*domain.Contact, error)
	listWithFilterFunc    func(ctx context.Context, tenantID string, filter domain.ContactFilter) ([]*domain.Contact, int, error)
	streamContactsFunc    func(ctx context.Context, tenantID string, filter domain.ContactFilter, yield func(c *domain.Contact, tagNames string) error) error
}

func (m *mockContactRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, tenantID, id)
	}
	return nil, nil
}

func (m *mockContactRepo) GetByRoutingValue(ctx context.Context, tenantID, channel, routingValue string) (*domain.Contact, error) {
	if m.getByRoutingValueFunc != nil {
		return m.getByRoutingValueFunc(ctx, tenantID, channel, routingValue)
	}
	return nil, nil
}

func (m *mockContactRepo) Create(ctx context.Context, contact *domain.Contact) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, contact)
	}
	return nil
}

func (m *mockContactRepo) ListByTenant(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
	if m.listByTenantFunc != nil {
		return m.listByTenantFunc(ctx, tenantID, channelFilter, limit, offset)
	}
	return nil, nil
}

func (m *mockContactRepo) ListWithFilter(ctx context.Context, tenantID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
	if m.listWithFilterFunc != nil {
		return m.listWithFilterFunc(ctx, tenantID, filter)
	}
	return nil, 0, nil
}

func (m *mockContactRepo) StreamContacts(ctx context.Context, tenantID string, filter domain.ContactFilter, yield func(c *domain.Contact, tagNames string) error) error {
	if m.streamContactsFunc != nil {
		return m.streamContactsFunc(ctx, tenantID, filter, yield)
	}
	return nil
}

type mockDestinationRepo struct {
	listByIDsFunc func(ctx context.Context, tenantID string, ids []string) ([]domain.TelegramDestination, error)
}

func (m *mockDestinationRepo) Upsert(ctx context.Context, destination *domain.TelegramDestination) error {
	return nil
}

func (m *mockDestinationRepo) ListByTenant(ctx context.Context, tenantID string) ([]domain.TelegramDestination, error) {
	return nil, nil
}

func (m *mockDestinationRepo) ListByIDs(ctx context.Context, tenantID string, ids []string) ([]domain.TelegramDestination, error) {
	if m.listByIDsFunc != nil {
		return m.listByIDsFunc(ctx, tenantID, ids)
	}
	return nil, nil
}

type mockSubscriptionRepo struct {
	getByTenantIDFunc func(ctx context.Context, tenantID string) (*domain.Subscription, error)
}

func (m *mockSubscriptionRepo) GetByTenantID(ctx context.Context, tenantID string) (*domain.Subscription, error) {
	if m.getByTenantIDFunc != nil {
		return m.getByTenantIDFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockSubscriptionRepo) Upsert(ctx context.Context, sub *domain.Subscription) error {
	return nil
}

func (m *mockSubscriptionRepo) GetBySubscriptionID(ctx context.Context, provider, subscriptionID string) (*domain.Subscription, error) {
	return nil, nil
}

func (m *mockSubscriptionRepo) GetByCustomerID(ctx context.Context, provider, customerID string) (*domain.Subscription, error) {
	return nil, nil
}

// TestComplianceOptOut_ExcludedFromBroadcast verifies that users with status != 'active' (opted_out, suppressed)
// are strictly excluded from broadcast dispatches and never queued to the delivery bus.
func TestComplianceOptOut_ExcludedFromBroadcast(t *testing.T) {
	tenantID := "tenant-test-compliance"
	campaignID := "camp-comp-1"

	campaign := &domain.Campaign{
		ID:               campaignID,
		TenantID:         tenantID,
		Title:             "Autumn Promo",
		MessageBody:      "Special discount code: AUTUMN50",
		Status:           "approved",
		SelectedChannels: `["whatsapp"]`,
	}

	campaignRepo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
			return campaign, nil
		},
		setDispatchingFunc: func(ctx context.Context, tID, id string, totalTargets int) error {
			if totalTargets != 2 {
				t.Errorf("expected 2 active targets, got %d", totalTargets)
			}
			return nil
		},
	}

	contacts := []*domain.Contact{
		{ID: "c1", TenantID: tenantID, FirstName: "Alice", Channel: "whatsapp", RoutingValue: "+1111111111", Status: "active"},
		{ID: "c2", TenantID: tenantID, FirstName: "Bob", Channel: "whatsapp", RoutingValue: "+2222222222", Status: "opted_out"},
		{ID: "c3", TenantID: tenantID, FirstName: "Charlie", Channel: "whatsapp", RoutingValue: "+3333333333", Status: "suppressed"},
		{ID: "c4", TenantID: tenantID, FirstName: "Diana", Channel: "whatsapp", RoutingValue: "+4444444444", Status: "active"},
	}

	contactRepo := &mockContactRepo{
		listByTenantFunc: func(ctx context.Context, tID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
			if offset == 0 {
				return contacts, nil
			}
			return nil, nil
		},
	}

	var mu sync.Mutex
	var dispatchedContactIDs []string

	publisher := &mockPublisher{
		publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
			mu.Lock()
			dispatchedContactIDs = append(dispatchedContactIDs, task.ContactID)
			mu.Unlock()
			return nil
		},
	}

	uc := NewCampaignUseCase(campaignRepo, contactRepo, &mockDestinationRepo{}, publisher, nil)
	err := uc.TriggerDispatch(context.Background(), tenantID, campaignID)
	if err != nil {
		t.Fatalf("unexpected error during dispatch: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Assert ONLY active contacts were queued
	if len(dispatchedContactIDs) != 2 {
		t.Fatalf("expected exactly 2 dispatched tasks, got %d: %v", len(dispatchedContactIDs), dispatchedContactIDs)
	}

	for _, id := range dispatchedContactIDs {
		if id == "c2" {
			t.Errorf("CRITICAL VIOLATION: Opted-out contact c2 was dispatched!")
		}
		if id == "c3" {
			t.Errorf("CRITICAL VIOLATION: Suppressed contact c3 was dispatched!")
		}
	}
}

// TestComplianceOptOut_ExplicitlySelectedOptedOutContact verifies that even if an opted-out contact is explicitly
// passed in campaign.SelectedContactIDs, the compliance filter strictly drops them.
func TestComplianceOptOut_ExplicitlySelectedOptedOutContact(t *testing.T) {
	tenantID := "tenant-test-compliance"
	campaignID := "camp-comp-2"

	campaign := &domain.Campaign{
		ID:                 campaignID,
		TenantID:           tenantID,
		Title:               "Targeted Campaign",
		MessageBody:        "VIP Update",
		Status:             "approved",
		SelectedContactIDs: `["c-opted-out", "c-active"]`,
		SelectedChannels:   `["whatsapp"]`,
	}

	campaignRepo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
			return campaign, nil
		},
		setDispatchingFunc: func(ctx context.Context, tID, id string, totalTargets int) error {
			if totalTargets != 1 {
				t.Errorf("expected 1 active target dispatched, got %d", totalTargets)
			}
			return nil
		},
	}

	contacts := []*domain.Contact{
		{ID: "c-opted-out", TenantID: tenantID, FirstName: "OptedOutUser", Channel: "whatsapp", RoutingValue: "+999999999", Status: "opted_out"},
		{ID: "c-active", TenantID: tenantID, FirstName: "ActiveUser", Channel: "whatsapp", RoutingValue: "+888888888", Status: "active"},
	}

	contactRepo := &mockContactRepo{
		listByTenantFunc: func(ctx context.Context, tID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
			if offset == 0 {
				return contacts, nil
			}
			return nil, nil
		},
	}

	var dispatchedTasks []*contracts.TargetDispatchTask
	publisher := &mockPublisher{
		publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
			dispatchedTasks = append(dispatchedTasks, task)
			return nil
		},
	}

	uc := NewCampaignUseCase(campaignRepo, contactRepo, &mockDestinationRepo{}, publisher, nil)
	err := uc.TriggerDispatch(context.Background(), tenantID, campaignID)
	if err != nil {
		t.Fatalf("unexpected error during dispatch: %v", err)
	}

	if len(dispatchedTasks) != 1 {
		t.Fatalf("expected exactly 1 task dispatched, got %d", len(dispatchedTasks))
	}
	if dispatchedTasks[0].ContactID != "c-active" {
		t.Errorf("expected task for c-active, got %s", dispatchedTasks[0].ContactID)
	}
}

// TestComplianceOptOut_AllTargetsOptedOutRejectsDispatch verifies that if every targeted recipient is opted out,
// the campaign is rejected, zero messages are published, and campaign status is not advanced.
func TestComplianceOptOut_AllTargetsOptedOutRejectsDispatch(t *testing.T) {
	tenantID := "tenant-test-compliance"
	campaignID := "camp-comp-3"

	campaign := &domain.Campaign{
		ID:               campaignID,
		TenantID:         tenantID,
		Title:             "Blacklisted Audience",
		MessageBody:      "You should not see this",
		Status:           "approved",
		SelectedChannels: `["whatsapp"]`,
	}

	campaignRepo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
			return campaign, nil
		},
		setDispatchingFunc: func(ctx context.Context, tID, id string, totalTargets int) error {
			t.Errorf("setDispatching should NOT be called when zero targets matched")
			return nil
		},
	}

	contacts := []*domain.Contact{
		{ID: "c-opted-1", TenantID: tenantID, Channel: "whatsapp", Status: "opted_out"},
		{ID: "c-opted-2", TenantID: tenantID, Channel: "whatsapp", Status: "opted_out"},
	}

	contactRepo := &mockContactRepo{
		listByTenantFunc: func(ctx context.Context, tID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
			if offset == 0 {
				return contacts, nil
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

	uc := NewCampaignUseCase(campaignRepo, contactRepo, &mockDestinationRepo{}, publisher, nil)
	err := uc.TriggerDispatch(context.Background(), tenantID, campaignID)
	if err == nil {
		t.Fatal("expected error rejecting dispatch when all targets are opted out, got nil")
	}
	if !strings.Contains(err.Error(), "no active targets matched") {
		t.Fatalf("expected 'no active targets matched' error, got %v", err)
	}
	if tasksCount != 0 {
		t.Errorf("expected 0 published tasks, got %d", tasksCount)
	}
}
