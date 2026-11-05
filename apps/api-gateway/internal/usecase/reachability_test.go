package usecase

import (
	"context"
	"strings"
	"testing"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

type mockChannelStatus struct {
	status string
	err    error
}

func (m *mockChannelStatus) GetStatus(ctx context.Context, tenantID string) (string, string, string, error) {
	return m.status, "", "", m.err
}

func TestPreflightReportsChannelReachability(t *testing.T) {
	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
			return &domain.Campaign{
				ID:               id,
				TenantID:         tenantID,
				Status:           "draft",
				SelectedChannels: `["telegram","whatsapp"]`,
			}, nil
		},
		countAudienceConsentFunc: func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceConsentCounts, error) {
			return &domain.AudienceConsentCounts{Total: 10, Active: 10}, nil
		},
		countAudienceReachabilityFunc: func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceReachability, error) {
			return &domain.AudienceReachability{WhatsAppActive: 4, TelegramActive: 6, TelegramUnstarted: 2}, nil
		},
	}
	uc := NewCampaignUseCase(repo, nil, &mockDestinationRepo{}, &mockPublisher{}, nil)
	uc.SetChannelStatusReader(&mockChannelStatus{status: "disconnected"})

	got, err := uc.Preflight(context.Background(), "t1", "c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Unreachable != 2 || got.Sendable != 8 {
		t.Fatalf("unreachable/sendable = %d/%d, want 2/8", got.Unreachable, got.Sendable)
	}
	if got.WhatsAppConnected {
		t.Fatal("expected whatsapp_connected to be false")
	}
	codes := map[string]bool{}
	for _, reason := range got.Reasons {
		codes[reason.Code] = true
	}
	if !codes["telegram_not_started"] || !codes["whatsapp_disconnected"] {
		t.Fatalf("missing reachability reasons, got %+v", got.Reasons)
	}
}

func TestPreflightReportsConnectedWhatsApp(t *testing.T) {
	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
			return &domain.Campaign{ID: id, TenantID: tenantID, Status: "draft", SelectedChannels: `["whatsapp"]`}, nil
		},
		countAudienceConsentFunc: func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceConsentCounts, error) {
			return &domain.AudienceConsentCounts{Total: 5, Active: 5}, nil
		},
		countAudienceReachabilityFunc: func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceReachability, error) {
			return &domain.AudienceReachability{WhatsAppActive: 5}, nil
		},
	}
	uc := NewCampaignUseCase(repo, nil, &mockDestinationRepo{}, &mockPublisher{}, nil)
	uc.SetChannelStatusReader(&mockChannelStatus{status: "connected"})

	got, err := uc.Preflight(context.Background(), "t1", "c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.WhatsAppConnected || got.Sendable != 5 {
		t.Fatalf("connected/sendable = %v/%d, want true/5", got.WhatsAppConnected, got.Sendable)
	}
	for _, reason := range got.Reasons {
		if reason.Code == "whatsapp_disconnected" {
			t.Fatalf("unexpected disconnected reason: %+v", got.Reasons)
		}
	}
}

func TestTriggerDispatchBlocksDisconnectedWhatsApp(t *testing.T) {
	published := 0
	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
			return &domain.Campaign{ID: id, TenantID: tenantID, Status: "draft", SelectedChannels: `["whatsapp"]`}, nil
		},
		countAudienceReachabilityFunc: func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceReachability, error) {
			return &domain.AudienceReachability{WhatsAppActive: 5}, nil
		},
	}
	uc := NewCampaignUseCase(repo, &mockContactRepo{}, &mockDestinationRepo{}, &mockPublisher{
		publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
			published++
			return nil
		},
	}, nil)
	uc.SetChannelStatusReader(&mockChannelStatus{status: "disconnected"})

	err := uc.TriggerDispatch(context.Background(), "t1", "c1")
	if err == nil || !strings.Contains(err.Error(), "WhatsApp is not connected") {
		t.Fatalf("expected WhatsApp connection error, got %v", err)
	}
	if published != 0 {
		t.Fatalf("expected no tasks to be published, got %d", published)
	}
}

func TestTriggerDispatchSkipsUnstartedTelegramContacts(t *testing.T) {
	var publishedTo []string
	var dispatchedTotal int

	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
			return &domain.Campaign{ID: id, TenantID: tenantID, Status: "draft", SelectedChannels: `["telegram"]`}, nil
		},
		countAudienceReachabilityFunc: func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceReachability, error) {
			return &domain.AudienceReachability{TelegramActive: 3, TelegramUnstarted: 1}, nil
		},
		listUnstartedTelegramFunc: func(ctx context.Context, tenantID string, contactIDs []string) ([]string, error) {
			return []string{"c2"}, nil
		},
		setDispatchingFunc: func(ctx context.Context, tenantID, id string, totalTargets int) error {
			dispatchedTotal = totalTargets
			return nil
		},
	}
	contacts := &mockContactRepo{
		listByTenantFunc: func(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
			if offset > 0 {
				return nil, nil
			}
			return []*domain.Contact{
				{ID: "c1", TenantID: tenantID, Channel: "telegram", RoutingValue: "111", Status: "active"},
				{ID: "c2", TenantID: tenantID, Channel: "telegram", RoutingValue: "222", Status: "active"},
				{ID: "c3", TenantID: tenantID, Channel: "telegram", RoutingValue: "333", Status: "active"},
			}, nil
		},
	}
	uc := NewCampaignUseCase(repo, contacts, &mockDestinationRepo{}, &mockPublisher{
		publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
			publishedTo = append(publishedTo, task.ContactID)
			return nil
		},
	}, nil)

	if err := uc.TriggerDispatch(context.Background(), "t1", "c1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(publishedTo) != 2 || publishedTo[0] != "c1" || publishedTo[1] != "c3" {
		t.Fatalf("published to %v, want [c1 c3]", publishedTo)
	}
	if dispatchedTotal != 2 {
		t.Fatalf("total targets = %d, want 2", dispatchedTotal)
	}
}
