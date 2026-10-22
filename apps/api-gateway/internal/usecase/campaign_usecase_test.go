package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/ctxkeys"
	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

type mockCampaignRepo struct {
	createFunc                    func(ctx context.Context, c *domain.Campaign) error
	getByIDFunc                   func(ctx context.Context, tenantID, id string) (*domain.Campaign, error)
	listByTenantFunc              func(ctx context.Context, tenantID, status string, limit, offset int) ([]*domain.Campaign, error)
	updateStatusFunc              func(ctx context.Context, tenantID, id, status string) error
	setDispatchingFunc            func(ctx context.Context, tenantID, id string, totalTargets int) error
	setScheduledFunc              func(ctx context.Context, tenantID, id string, scheduledAt time.Time) error
	pauseCampaignFunc             func(ctx context.Context, tenantID, id string) error
	resumeCampaignFunc            func(ctx context.Context, tenantID, id string) error
	cancelCampaignFunc            func(ctx context.Context, tenantID, id string) error
	deleteDraftFunc               func(ctx context.Context, tenantID, id string) error
	recordDeliveryResultFunc      func(ctx context.Context, res *contracts.TargetDeliveryResult) error
	getCampaignStatsFunc          func(ctx context.Context, tenantID, campaignID string) (*domain.CampaignStats, error)
	listDeliveriesByCampaignFunc  func(ctx context.Context, tenantID, campaignID string, limit, offset int) ([]*domain.CampaignDelivery, error)
	findDueScheduledCampaignsFunc func(ctx context.Context) ([]*domain.Campaign, error)
	cancelScheduledCampaignFunc   func(ctx context.Context, tenantID, id string) error
	countDeliveriesSinceFunc      func(ctx context.Context, tenantID string, since time.Time) (int, error)
	submitForApprovalFunc         func(ctx context.Context, tenantID, id, submittedBy string) error
	approveCampaignFunc           func(ctx context.Context, tenantID, id, approvedBy string) error
	rejectCampaignFunc            func(ctx context.Context, tenantID, id, reason string) error
	getApprovalSettingFunc        func(ctx context.Context, tenantID string) (bool, error)
}

func (m *mockCampaignRepo) Create(ctx context.Context, c *domain.Campaign) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, c)
	}
	return nil
}
func (m *mockCampaignRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, tenantID, id)
	}
	return nil, nil
}
func (m *mockCampaignRepo) ListByTenant(ctx context.Context, tenantID, status string, limit, offset int) ([]*domain.Campaign, error) {
	if m.listByTenantFunc != nil {
		return m.listByTenantFunc(ctx, tenantID, status, limit, offset)
	}
	return nil, nil
}
func (m *mockCampaignRepo) UpdateStatus(ctx context.Context, tenantID, id, status string) error {
	if m.updateStatusFunc != nil {
		return m.updateStatusFunc(ctx, tenantID, id, status)
	}
	return nil
}
func (m *mockCampaignRepo) SetDispatching(ctx context.Context, tenantID, id string, totalTargets int) error {
	if m.setDispatchingFunc != nil {
		return m.setDispatchingFunc(ctx, tenantID, id, totalTargets)
	}
	return nil
}
func (m *mockCampaignRepo) SetScheduled(ctx context.Context, tenantID, id string, scheduledAt time.Time) error {
	if m.setScheduledFunc != nil {
		return m.setScheduledFunc(ctx, tenantID, id, scheduledAt)
	}
	return nil
}
func (m *mockCampaignRepo) PauseCampaign(ctx context.Context, tenantID, id string) error {
	if m.pauseCampaignFunc != nil {
		return m.pauseCampaignFunc(ctx, tenantID, id)
	}
	return nil
}
func (m *mockCampaignRepo) ResumeCampaign(ctx context.Context, tenantID, id string) error {
	if m.resumeCampaignFunc != nil {
		return m.resumeCampaignFunc(ctx, tenantID, id)
	}
	return nil
}
func (m *mockCampaignRepo) CancelCampaign(ctx context.Context, tenantID, id string) error {
	if m.cancelCampaignFunc != nil {
		return m.cancelCampaignFunc(ctx, tenantID, id)
	}
	return nil
}
func (m *mockCampaignRepo) DeleteDraft(ctx context.Context, tenantID, id string) error {
	if m.deleteDraftFunc != nil {
		return m.deleteDraftFunc(ctx, tenantID, id)
	}
	return nil
}
func (m *mockCampaignRepo) RecordDeliveryResult(ctx context.Context, res *contracts.TargetDeliveryResult) error {
	if m.recordDeliveryResultFunc != nil {
		return m.recordDeliveryResultFunc(ctx, res)
	}
	return nil
}
func (m *mockCampaignRepo) GetCampaignStats(ctx context.Context, tenantID, campaignID string) (*domain.CampaignStats, error) {
	if m.getCampaignStatsFunc != nil {
		return m.getCampaignStatsFunc(ctx, tenantID, campaignID)
	}
	return &domain.CampaignStats{}, nil
}
func (m *mockCampaignRepo) ListDeliveriesByCampaign(ctx context.Context, tenantID, campaignID string, limit, offset int) ([]*domain.CampaignDelivery, error) {
	if m.listDeliveriesByCampaignFunc != nil {
		return m.listDeliveriesByCampaignFunc(ctx, tenantID, campaignID, limit, offset)
	}
	return nil, nil
}
func (m *mockCampaignRepo) FindDueScheduledCampaigns(ctx context.Context) ([]*domain.Campaign, error) {
	if m.findDueScheduledCampaignsFunc != nil {
		return m.findDueScheduledCampaignsFunc(ctx)
	}
	return nil, nil
}
func (m *mockCampaignRepo) CancelScheduledCampaign(ctx context.Context, tenantID, id string) error {
	if m.cancelScheduledCampaignFunc != nil {
		return m.cancelScheduledCampaignFunc(ctx, tenantID, id)
	}
	return nil
}
func (m *mockCampaignRepo) CountDeliveriesSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	if m.countDeliveriesSinceFunc != nil {
		return m.countDeliveriesSinceFunc(ctx, tenantID, since)
	}
	return 0, nil
}
func (m *mockCampaignRepo) SubmitForApproval(ctx context.Context, tenantID, id, submittedBy string) error {
	if m.submitForApprovalFunc != nil {
		return m.submitForApprovalFunc(ctx, tenantID, id, submittedBy)
	}
	return nil
}
func (m *mockCampaignRepo) ApproveCampaign(ctx context.Context, tenantID, id, approvedBy string) error {
	if m.approveCampaignFunc != nil {
		return m.approveCampaignFunc(ctx, tenantID, id, approvedBy)
	}
	return nil
}
func (m *mockCampaignRepo) RejectCampaign(ctx context.Context, tenantID, id, reason string) error {
	if m.rejectCampaignFunc != nil {
		return m.rejectCampaignFunc(ctx, tenantID, id, reason)
	}
	return nil
}
func (m *mockCampaignRepo) GetApprovalSetting(ctx context.Context, tenantID string) (bool, error) {
	if m.getApprovalSettingFunc != nil {
		return m.getApprovalSettingFunc(ctx, tenantID)
	}
	return false, nil
}

type mockPublisher struct {
	publishFunc func(ctx context.Context, task *contracts.TargetDispatchTask) error
}

func (p *mockPublisher) PublishDispatchTask(ctx context.Context, task *contracts.TargetDispatchTask) error {
	if p.publishFunc != nil {
		return p.publishFunc(ctx, task)
	}
	return nil
}

func TestCampaignApprovalWorkflow(t *testing.T) {
	tenantID := "tenant-123"
	campaignID := "camp-456"

	t.Run("SubmitForApproval succeeds for draft campaign", func(t *testing.T) {
		submitted := false
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: id, TenantID: tID, Status: "draft"}, nil
			},
			submitForApprovalFunc: func(ctx context.Context, tID, id, subBy string) error {
				submitted = true
				if subBy != "user-1" {
					t.Errorf("expected submitted_by user-1, got %s", subBy)
				}
				return nil
			},
		}

		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		err := uc.SubmitForApproval(context.Background(), tenantID, campaignID, "user-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !submitted {
			t.Fatal("expected submitForApprovalFunc to be invoked")
		}
	})

	t.Run("SubmitForApproval fails for already running campaign", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: id, TenantID: tID, Status: "processing"}, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		err := uc.SubmitForApproval(context.Background(), tenantID, campaignID, "user-1")
		if err == nil || !strings.Contains(err.Error(), "only draft or rejected") {
			t.Fatalf("expected error for non-draft submission, got %v", err)
		}
	})

	t.Run("ApproveCampaign fails when caller is regular member", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: id, TenantID: tID, Status: "pending_approval"}, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "member")
		err := uc.ApproveCampaign(ctx, tenantID, campaignID, "user-member")
		if err == nil || !strings.Contains(err.Error(), "only workspace owners or admins") {
			t.Fatalf("expected member approval to be forbidden, got %v", err)
		}
	})

	t.Run("ApproveCampaign succeeds when caller is owner", func(t *testing.T) {
		approved := false
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: id, TenantID: tID, Status: "pending_approval"}, nil
			},
			approveCampaignFunc: func(ctx context.Context, tID, id, appBy string) error {
				approved = true
				return nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "owner")
		err := uc.ApproveCampaign(ctx, tenantID, campaignID, "owner-id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !approved {
			t.Fatal("expected approveCampaignFunc to be called")
		}
	})

	t.Run("RejectCampaign requires a non-empty reason", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: id, TenantID: tID, Status: "pending_approval"}, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "admin")
		err := uc.RejectCampaign(ctx, tenantID, campaignID, "   ")
		if err == nil || !strings.Contains(err.Error(), "rejection reason must be provided") {
			t.Fatalf("expected error for empty rejection reason, got %v", err)
		}
	})

	t.Run("TriggerDispatch blocks member when approval is required", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                             id,
					TenantID:                       tID,
					Status:                         "draft",
					SelectedChannels:               "[]",
					SelectedTelegramDestinationIDs: "[]",
					SelectedContactIDs:             "[]",
				}, nil
			},
			getApprovalSettingFunc: func(ctx context.Context, tID string) (bool, error) {
				return true, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "member")
		err := uc.TriggerDispatch(ctx, tenantID, campaignID)
		if err == nil || !strings.Contains(err.Error(), "campaign approval is required") {
			t.Fatalf("expected approval error for member dispatch, got %v", err)
		}
	})

	t.Run("GetCampaignReport computes correct rates", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: id, TenantID: tID, Title: "Winter Promo", Status: "completed"}, nil
			},
			getCampaignStatsFunc: func(ctx context.Context, tID, cID string) (*domain.CampaignStats, error) {
				return &domain.CampaignStats{
					TotalTargets: 100,
					Delivered:    80,
					Failed:       20,
				}, nil
			},
			listDeliveriesByCampaignFunc: func(ctx context.Context, tID, cID string, limit, offset int) ([]*domain.CampaignDelivery, error) {
				return []*domain.CampaignDelivery{
					{ID: "d1", Status: "read"},
					{ID: "d2", Status: "read"},
					{ID: "d3", Status: "delivered"},
					{ID: "d4", Status: "failed"},
				}, nil
			},
		}

		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		report, err := uc.GetCampaignReport(context.Background(), tenantID, campaignID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if report.DeliveryRate != 80.0 {
			t.Errorf("expected 80.0%% delivery rate, got %.1f", report.DeliveryRate)
		}
		// 2 reads out of 80 delivered = 2.5%
		if report.ReadRate != 2.5 {
			t.Errorf("expected 2.5%% read rate, got %.1f", report.ReadRate)
		}
	})
}
