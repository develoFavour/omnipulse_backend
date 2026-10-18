package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnipulse/apps/api-gateway/internal/domain"
)

type mockChannelRepo struct {
	createChannelFunc        func(ctx context.Context, channel *domain.TenantChannel) error
	listByTenantFunc         func(ctx context.Context, tenantID string) ([]domain.TenantChannel, error)
	countActiveByTenantFunc  func(ctx context.Context, tenantID string) (int, error)
	findActiveByPlatformFunc func(ctx context.Context, tenantID, platform string) (*domain.TenantChannel, error)
	deleteByPlatformFunc     func(ctx context.Context, tenantID, platform string) error
}

func (m *mockChannelRepo) CreateChannel(ctx context.Context, channel *domain.TenantChannel) error {
	if m.createChannelFunc != nil {
		return m.createChannelFunc(ctx, channel)
	}
	return nil
}

func (m *mockChannelRepo) ListByTenant(ctx context.Context, tenantID string) ([]domain.TenantChannel, error) {
	if m.listByTenantFunc != nil {
		return m.listByTenantFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockChannelRepo) CountActiveByTenant(ctx context.Context, tenantID string) (int, error) {
	if m.countActiveByTenantFunc != nil {
		return m.countActiveByTenantFunc(ctx, tenantID)
	}
	return 0, nil
}

func (m *mockChannelRepo) FindActiveByPlatform(ctx context.Context, tenantID, platform string) (*domain.TenantChannel, error) {
	if m.findActiveByPlatformFunc != nil {
		return m.findActiveByPlatformFunc(ctx, tenantID, platform)
	}
	return nil, nil
}

func (m *mockChannelRepo) DeleteByPlatform(ctx context.Context, tenantID, platform string) error {
	if m.deleteByPlatformFunc != nil {
		return m.deleteByPlatformFunc(ctx, tenantID, platform)
	}
	return nil
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

func TestChannelQuota_Enforcement(t *testing.T) {
	tenantID := "tenant-channel-quota-test"

	t.Run("Rejects channel creation with HTTP 402 when starter channel limit (2) reached", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:      tID,
					PlanTier:      domain.PlanStarter,
					ChannelsLimit: 2,
				}, nil
			},
		}

		channelRepo := &mockChannelRepo{
			listByTenantFunc: func(ctx context.Context, tID string) ([]domain.TenantChannel, error) {
				// Already 2 connected channels (e.g. WhatsApp and Telegram)
				return []domain.TenantChannel{
					{ID: "ch-1", TenantID: tID, PlatformName: "whatsapp", Status: "active"},
					{ID: "ch-2", TenantID: tID, PlatformName: "telegram", Status: "active"},
				}, nil
			},
		}

		h := NewChannelHandler(channelRepo, nil, subRepo, "http://localhost", "http://localhost", MetaAppConfig{}, nil)

		// 1. Direct method check
		err := h.checkChannelQuota(context.Background(), tenantID)
		if err == nil {
			t.Fatal("expected ErrQuotaExceeded for 2 active channels on starter plan, got nil")
		}
		if !errors.Is(err, domain.ErrQuotaExceeded) {
			t.Fatalf("expected domain.ErrQuotaExceeded, got: %v", err)
		}
		if !strings.Contains(err.Error(), "channel limit of 2 reached for starter tier") {
			t.Errorf("unexpected error message: %v", err)
		}

		// 2. HTTP handler response check (402 Payment Required)
		body, _ := json.Marshal(map[string]string{
			"platform_name":   "instagram",
			"sender_identity": "MyBrand",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), TenantIDKey, tenantID))
		rr := httptest.NewRecorder()

		h.CreateChannel(rr, req)

		if rr.Code != http.StatusPaymentRequired {
			t.Fatalf("expected HTTP 402 Payment Required when channel quota exceeded, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "channel limit of 2 reached") {
			t.Errorf("expected quota message in response body, got %s", rr.Body.String())
		}
	})

	t.Run("Allows channel creation when under channel limit", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:      tID,
					PlanTier:      domain.PlanStarter,
					ChannelsLimit: 2,
				}, nil
			},
		}

		channelRepo := &mockChannelRepo{
			listByTenantFunc: func(ctx context.Context, tID string) ([]domain.TenantChannel, error) {
				// 1 active channel, 1 disconnected channel
				return []domain.TenantChannel{
					{ID: "ch-1", TenantID: tID, PlatformName: "whatsapp", Status: "active"},
					{ID: "ch-2", TenantID: tID, PlatformName: "telegram", Status: "disconnected"},
				}, nil
			},
		}

		h := NewChannelHandler(channelRepo, nil, subRepo, "http://localhost", "http://localhost", MetaAppConfig{}, nil)

		err := h.checkChannelQuota(context.Background(), tenantID)
		if err != nil {
			t.Fatalf("unexpected error when under channel limit: %v", err)
		}
	})

	t.Run("Allows unlimited channels on Growth or Agency plans (-1)", func(t *testing.T) {
		subRepo := &mockSubscriptionRepo{
			getByTenantIDFunc: func(ctx context.Context, tID string) (*domain.Subscription, error) {
				return &domain.Subscription{
					TenantID:      tID,
					PlanTier:      domain.PlanGrowth,
					ChannelsLimit: -1, // unlimited
				}, nil
			},
		}

		channelRepo := &mockChannelRepo{
			listByTenantFunc: func(ctx context.Context, tID string) ([]domain.TenantChannel, error) {
				// Already 5 active channels
				return []domain.TenantChannel{
					{ID: "ch-1", TenantID: tID, Status: "active"},
					{ID: "ch-2", TenantID: tID, Status: "active"},
					{ID: "ch-3", TenantID: tID, Status: "active"},
					{ID: "ch-4", TenantID: tID, Status: "active"},
					{ID: "ch-5", TenantID: tID, Status: "active"},
				}, nil
			},
		}

		h := NewChannelHandler(channelRepo, nil, subRepo, "http://localhost", "http://localhost", MetaAppConfig{}, nil)

		err := h.checkChannelQuota(context.Background(), tenantID)
		if err != nil {
			t.Fatalf("unexpected error for unlimited channels: %v", err)
		}
	})
}
