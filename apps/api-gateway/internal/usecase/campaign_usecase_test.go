package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/ctxkeys"
	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

type mockCampaignRepo struct {
	createFunc                    func(ctx context.Context, c *domain.Campaign) error
	updateContentFunc             func(ctx context.Context, tenantID, id string, c *domain.Campaign, resetApproval bool) error
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
	claimDeliveriesForRetryFunc   func(ctx context.Context, tenantID, campaignID string, ids []string) ([]*domain.CampaignDelivery, error)
	releaseDeliveryClaimsFunc     func(ctx context.Context, campaignID string, ids []string) error
	countAudienceConsentFunc      func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceConsentCounts, error)
	countAudienceReachabilityFunc func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceReachability, error)
	listUnstartedTelegramFunc     func(ctx context.Context, tenantID string, contactIDs []string) ([]string, error)
	streamAudienceConsentFunc     func(ctx context.Context, tenantID string, contactIDs, channels []string, yield func(*domain.AudienceConsentRow) error) error
}

func (m *mockCampaignRepo) Create(ctx context.Context, c *domain.Campaign) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, c)
	}
	return nil
}
func (m *mockCampaignRepo) UpdateContent(ctx context.Context, tenantID, id string, c *domain.Campaign, resetApproval bool) error {
	if m.updateContentFunc != nil {
		return m.updateContentFunc(ctx, tenantID, id, c, resetApproval)
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
func (m *mockCampaignRepo) ListDeliveriesByCampaign(ctx context.Context, tenantID, campaignID, status, failureCode string, retryable *bool, limit, offset int) ([]*domain.CampaignDelivery, error) {
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

func (m *mockCampaignRepo) GetCampaignHealth(ctx context.Context, tenantID, campaignID string) (*domain.CampaignHealth, error) {
	return nil, nil
}

func (m *mockCampaignRepo) ClaimDeliveriesForRetry(ctx context.Context, tenantID, campaignID string, ids []string) ([]*domain.CampaignDelivery, error) {
	if m.claimDeliveriesForRetryFunc != nil {
		return m.claimDeliveriesForRetryFunc(ctx, tenantID, campaignID, ids)
	}
	return nil, nil
}

func (m *mockCampaignRepo) ReleaseDeliveryClaims(ctx context.Context, campaignID string, ids []string) error {
	if m.releaseDeliveryClaimsFunc != nil {
		return m.releaseDeliveryClaimsFunc(ctx, campaignID, ids)
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

func TestRetryDeliveries(t *testing.T) {
	const tenantID = "tenant-123"
	const campaignID = "camp-456"

	completedCampaign := &domain.Campaign{
		ID: campaignID, TenantID: tenantID, Status: "completed", MessageBody: "Hi {first_name}",
	}

	t.Run("publishes claimed rows with the original first name", func(t *testing.T) {
		var published []*contracts.TargetDispatchTask
		var reopenedTo string

		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return completedCampaign, nil
			},
			claimDeliveriesForRetryFunc: func(ctx context.Context, tID, cID string, ids []string) ([]*domain.CampaignDelivery, error) {
				return []*domain.CampaignDelivery{{
					ID: "d1", TargetType: "contact", Platform: "whatsapp",
					RoutingValue: "+2348000000000", FirstName: "Ada",
				}}, nil
			},
			updateStatusFunc: func(ctx context.Context, tID, id, status string) error {
				reopenedTo = status
				return nil
			},
		}
		pub := &mockPublisher{publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
			published = append(published, task)
			return nil
		}}
		uc := NewCampaignUseCase(repo, nil, nil, pub, nil)

		resp, err := uc.RetryDeliveries(context.Background(), tenantID, campaignID, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Retried != 1 || resp.TotalTasks != 1 {
			t.Fatalf("expected 1/1 retried, got %d/%d", resp.Retried, resp.TotalTasks)
		}
		if len(published) != 1 {
			t.Fatalf("expected 1 published task, got %d", len(published))
		}
		if published[0].FirstName != "Ada" {
			t.Errorf("expected first name to survive retry, got %q", published[0].FirstName)
		}
		if reopenedTo != "processing" {
			t.Errorf("expected completed campaign to reopen as processing, got %q", reopenedTo)
		}
	})

	t.Run("publishes nothing when no row can be claimed", func(t *testing.T) {
		published := 0
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return completedCampaign, nil
			},
			claimDeliveriesForRetryFunc: func(ctx context.Context, tID, cID string, ids []string) ([]*domain.CampaignDelivery, error) {
				return nil, nil
			},
		}
		pub := &mockPublisher{publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
			published++
			return nil
		}}
		uc := NewCampaignUseCase(repo, nil, nil, pub, nil)

		resp, err := uc.RetryDeliveries(context.Background(), tenantID, campaignID, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Retried != 0 || published != 0 {
			t.Fatalf("expected no re-publish, got %d retried and %d published", resp.Retried, published)
		}
	})

	t.Run("releases the claim when publishing fails", func(t *testing.T) {
		var released []string
		var statusCalls []string

		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return completedCampaign, nil
			},
			claimDeliveriesForRetryFunc: func(ctx context.Context, tID, cID string, ids []string) ([]*domain.CampaignDelivery, error) {
				return []*domain.CampaignDelivery{{ID: "d1", TargetType: "contact", Platform: "whatsapp", RoutingValue: "+234"}}, nil
			},
			releaseDeliveryClaimsFunc: func(ctx context.Context, cID string, ids []string) error {
				released = append(released, ids...)
				return nil
			},
			updateStatusFunc: func(ctx context.Context, tID, id, status string) error {
				statusCalls = append(statusCalls, status)
				return nil
			},
		}
		pub := &mockPublisher{publishFunc: func(ctx context.Context, task *contracts.TargetDispatchTask) error {
			return context.DeadlineExceeded
		}}
		uc := NewCampaignUseCase(repo, nil, nil, pub, nil)

		resp, err := uc.RetryDeliveries(context.Background(), tenantID, campaignID, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Retried != 0 {
			t.Errorf("expected 0 retried, got %d", resp.Retried)
		}
		if len(released) != 1 || released[0] != "d1" {
			t.Errorf("expected claim d1 to be released, got %v", released)
		}
		if len(statusCalls) != 2 || statusCalls[0] != "processing" || statusCalls[1] != "completed" {
			t.Errorf("expected campaign to reopen then restore to completed, got %v", statusCalls)
		}
	})

	t.Run("refuses paused campaigns", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: campaignID, TenantID: tenantID, Status: "paused"}, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)

		_, err := uc.RetryDeliveries(context.Background(), tenantID, campaignID, nil)
		if err == nil || !strings.Contains(err.Error(), "processing or completed") {
			t.Fatalf("expected paused campaigns to be rejected, got %v", err)
		}
	})
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

	t.Run("ApproveCampaign blocks approval when the brand requested changes", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                   id,
					TenantID:             tID,
					Status:               "pending_approval",
					LatestReviewDecision: domain.ReviewDecisionChangesRequested,
				}, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "owner")
		err := uc.ApproveCampaign(ctx, tenantID, campaignID, "owner-id")
		if err == nil || !strings.Contains(err.Error(), "requested changes") {
			t.Fatalf("expected changes-requested error, got %v", err)
		}
	})

	t.Run("ApproveCampaign allows a resubmission the brand asked changes on earlier", func(t *testing.T) {
		reviewedAt := time.Now().Add(-2 * time.Hour)
		revisedAt := time.Now().Add(-time.Hour)
		approved := false
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                   id,
					TenantID:             tID,
					Status:               "pending_approval",
					LatestReviewDecision: domain.ReviewDecisionChangesRequested,
					LatestReviewAt:       &reviewedAt,
					ContentRevisedAt:     &revisedAt,
				}, nil
			},
			approveCampaignFunc: func(ctx context.Context, tID, id, approvedBy string) error {
				approved = true
				return nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "owner")
		if err := uc.ApproveCampaign(ctx, tenantID, campaignID, "owner-id"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !approved {
			t.Fatal("expected the revised campaign to be approved")
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

	t.Run("TriggerDispatch blocks owner dispatching an unapproved draft", func(t *testing.T) {
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
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "owner")
		err := uc.TriggerDispatch(ctx, tenantID, campaignID)
		if err == nil || !strings.Contains(err.Error(), "campaign approval is required") {
			t.Fatalf("expected approval error for owner dispatch of draft, got %v", err)
		}
	})

	t.Run("TriggerDispatch blocks pending_approval regardless of role", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                             id,
					TenantID:                       tID,
					Status:                         "pending_approval",
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
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "admin")
		err := uc.TriggerDispatch(ctx, tenantID, campaignID)
		if err == nil || !strings.Contains(err.Error(), "pending approval") {
			t.Fatalf("expected pending approval error, got %v", err)
		}
	})

	t.Run("TriggerDispatch blocks a campaign the brand asked to change", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                             id,
					TenantID:                       tID,
					Status:                         "approved",
					LatestReviewDecision:           domain.ReviewDecisionChangesRequested,
					SelectedChannels:               "[]",
					SelectedTelegramDestinationIDs: "[]",
					SelectedContactIDs:             "[]",
				}, nil
			},
			getApprovalSettingFunc: func(ctx context.Context, tID string) (bool, error) {
				return false, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		err := uc.TriggerDispatch(context.Background(), tenantID, campaignID)
		if err == nil || !strings.Contains(err.Error(), "requested changes") {
			t.Fatalf("expected changes-requested error, got %v", err)
		}
	})

	t.Run("TriggerDispatch blocks a campaign revised since the brand approved it", func(t *testing.T) {
		reviewedAt := time.Now().Add(-2 * time.Hour)
		revisedAt := time.Now().Add(-time.Hour)
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                             id,
					TenantID:                       tID,
					Status:                         "approved",
					LatestReviewDecision:           domain.ReviewDecisionApproved,
					LatestReviewAt:                 &reviewedAt,
					ContentRevisedAt:               &revisedAt,
					SelectedChannels:               "[]",
					SelectedTelegramDestinationIDs: "[]",
					SelectedContactIDs:             "[]",
				}, nil
			},
			getApprovalSettingFunc: func(ctx context.Context, tID string) (bool, error) {
				return false, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		err := uc.TriggerDispatch(context.Background(), tenantID, campaignID)
		if err == nil || !strings.Contains(err.Error(), "latest version") {
			t.Fatalf("expected stale-review error, got %v", err)
		}
	})

	t.Run("ScheduleCampaign allows an approved campaign the brand re-approved after revision", func(t *testing.T) {
		reviewedAt := time.Now().Add(-time.Hour)
		revisedAt := time.Now().Add(-2 * time.Hour)
		scheduled := false
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                   id,
					TenantID:             tID,
					Status:               "approved",
					LatestReviewDecision: domain.ReviewDecisionApproved,
					LatestReviewAt:       &reviewedAt,
					ContentRevisedAt:     &revisedAt,
				}, nil
			},
			getApprovalSettingFunc: func(ctx context.Context, tID string) (bool, error) {
				return true, nil
			},
			setScheduledFunc: func(ctx context.Context, tID, id string, at time.Time) error {
				scheduled = true
				return nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		err := uc.ScheduleCampaign(context.Background(), tenantID, campaignID, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !scheduled {
			t.Fatal("expected the campaign to be scheduled")
		}
	})

	t.Run("ScheduleCampaign requires approval for owners when enabled", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:       id,
					TenantID: tID,
					Status:   "draft",
				}, nil
			},
			getApprovalSettingFunc: func(ctx context.Context, tID string) (bool, error) {
				return true, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "owner")
		err := uc.ScheduleCampaign(ctx, tenantID, campaignID, time.Now().Add(time.Hour))
		if err == nil || !strings.Contains(err.Error(), "campaign approval is required") {
			t.Fatalf("expected approval error for owner scheduling draft, got %v", err)
		}
	})

	t.Run("ScheduleCampaign allows an approved campaign when approval is required", func(t *testing.T) {
		scheduled := false
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:       id,
					TenantID: tID,
					Status:   "approved",
				}, nil
			},
			getApprovalSettingFunc: func(ctx context.Context, tID string) (bool, error) {
				return true, nil
			},
			setScheduledFunc: func(ctx context.Context, tID, id string, at time.Time) error {
				scheduled = true
				return nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		ctx := context.WithValue(context.Background(), ctxkeys.UserRoleKey, "member")
		if err := uc.ScheduleCampaign(ctx, tenantID, campaignID, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !scheduled {
			t.Fatal("expected setScheduledFunc to be invoked")
		}
	})

	t.Run("ScheduleCampaign blocks a campaign the brand asked to change", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                   id,
					TenantID:             tID,
					Status:               "approved",
					LatestReviewDecision: domain.ReviewDecisionChangesRequested,
				}, nil
			},
			getApprovalSettingFunc: func(ctx context.Context, tID string) (bool, error) {
				return false, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		err := uc.ScheduleCampaign(context.Background(), tenantID, campaignID, time.Now().Add(time.Hour))
		if err == nil || !strings.Contains(err.Error(), "requested changes") {
			t.Fatalf("expected changes-requested error, got %v", err)
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

func (m *mockCampaignRepo) CountAudienceConsent(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceConsentCounts, error) {
	if m.countAudienceConsentFunc != nil {
		return m.countAudienceConsentFunc(ctx, tenantID, contactIDs, channels)
	}
	return &domain.AudienceConsentCounts{}, nil
}

func (m *mockCampaignRepo) CountAudienceReachability(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceReachability, error) {
	if m.countAudienceReachabilityFunc != nil {
		return m.countAudienceReachabilityFunc(ctx, tenantID, contactIDs, channels)
	}
	return &domain.AudienceReachability{}, nil
}

func (m *mockCampaignRepo) ListUnstartedTelegramContactIDs(ctx context.Context, tenantID string, contactIDs []string) ([]string, error) {
	if m.listUnstartedTelegramFunc != nil {
		return m.listUnstartedTelegramFunc(ctx, tenantID, contactIDs)
	}
	return nil, nil
}

func (m *mockCampaignRepo) StreamAudienceConsent(ctx context.Context, tenantID string, contactIDs, channels []string, yield func(*domain.AudienceConsentRow) error) error {
	if m.streamAudienceConsentFunc != nil {
		return m.streamAudienceConsentFunc(ctx, tenantID, contactIDs, channels, yield)
	}
	return nil
}

func TestPreflight(t *testing.T) {
	newRepo := func(counts *domain.AudienceConsentCounts, destinations []domain.TelegramDestination) *mockCampaignRepo {
		return &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                             id,
					TenantID:                       tenantID,
					Status:                         "draft",
					SelectedChannels:               `["whatsapp"]`,
					SelectedTelegramDestinationIDs: `["d1","d2"]`,
				}, nil
			},
			countAudienceConsentFunc: func(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceConsentCounts, error) {
				return counts, nil
			},
		}
	}

	t.Run("reports skipped contacts and destinations", func(t *testing.T) {
		repo := newRepo(&domain.AudienceConsentCounts{Total: 100, Active: 95, OptedOut: 5, NoConsent: 2}, nil)
		destRepo := &mockDestinationRepo{
			listByIDsFunc: func(ctx context.Context, tenantID string, ids []string) ([]domain.TelegramDestination, error) {
				return []domain.TelegramDestination{{ID: "d1"}, {ID: "d2"}}, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, destRepo, &mockPublisher{}, nil)

		got, err := uc.Preflight(context.Background(), "t1", "c1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.TotalTargets != 102 || got.Sendable != 97 {
			t.Fatalf("total/sendable = %d/%d, want 102/97", got.TotalTargets, got.Sendable)
		}
		if got.OptedOut != 5 || got.NoConsent != 2 {
			t.Fatalf("opted_out/no_consent = %d/%d, want 5/2", got.OptedOut, got.NoConsent)
		}
		codes := map[string]bool{}
		for _, reason := range got.Reasons {
			codes[reason.Code] = true
		}
		if !codes["opted_out"] || !codes["no_consent"] {
			t.Fatalf("missing expected reasons, got %+v", got.Reasons)
		}
	})

	t.Run("warns when nothing is sendable", func(t *testing.T) {
		repo := newRepo(&domain.AudienceConsentCounts{Total: 10, Active: 0, OptedOut: 10}, nil)
		uc := NewCampaignUseCase(repo, nil, &mockDestinationRepo{}, &mockPublisher{}, nil)

		got, err := uc.Preflight(context.Background(), "t1", "c1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Sendable != 0 {
			t.Fatalf("sendable = %d, want 0", got.Sendable)
		}
		if len(got.Reasons) == 0 || got.Reasons[len(got.Reasons)-1].Code != "no_sendable_targets" {
			t.Fatalf("expected no_sendable_targets reason, got %+v", got.Reasons)
		}
	})

	t.Run("campaign without an audience reports zero targets", func(t *testing.T) {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: id, TenantID: tenantID, Status: "draft"}, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, &mockDestinationRepo{}, &mockPublisher{}, nil)

		got, err := uc.Preflight(context.Background(), "t1", "c1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.TotalTargets != 0 || got.Sendable != 0 {
			t.Fatalf("expected empty audience, got %+v", got)
		}
	})
}

func TestUpdateCampaignResetsApprovalAfterBrandChanges(t *testing.T) {
	const tenantID, campaignID = "tenant-1", "campaign-1"

	stored := &domain.Campaign{
		ID:                   campaignID,
		TenantID:             tenantID,
		Status:               "approved",
		LatestReviewDecision: domain.ReviewDecisionChangesRequested,
	}
	resetApproval := false
	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tid, id string) (*domain.Campaign, error) {
			return stored, nil
		},
		updateContentFunc: func(ctx context.Context, tid, id string, c *domain.Campaign, reset bool) error {
			resetApproval = reset
			stored = &domain.Campaign{
				ID:                   id,
				TenantID:             tid,
				Title:                c.Title,
				MessageBody:          c.MessageBody,
				Status:               "draft",
				SelectedChannels:     c.SelectedChannels,
				LatestReviewDecision: domain.ReviewDecisionChangesRequested,
			}
			return nil
		},
	}
	uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)

	updated, err := uc.UpdateCampaign(context.Background(), tenantID, campaignID, &domain.Campaign{
		Title:            "  Fixed discount  ",
		MessageBody:      "  New copy  ",
		SelectedChannels: `["whatsapp"]`,
	})
	if err != nil {
		t.Fatalf("UpdateCampaign returned error: %v", err)
	}
	if !resetApproval {
		t.Fatal("editing an approved campaign must reset approval")
	}
	if updated.Status != "draft" {
		t.Fatalf("expected the campaign to return to draft, got %q", updated.Status)
	}
	if updated.Title != "Fixed discount" || updated.MessageBody != "New copy" {
		t.Fatalf("content was not normalized: %+v", updated)
	}
}

func TestUpdateCampaignKeepsDraftStatus(t *testing.T) {
	const tenantID, campaignID = "tenant-1", "campaign-1"

	stored := &domain.Campaign{ID: campaignID, TenantID: tenantID, Status: "draft"}
	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tid, id string) (*domain.Campaign, error) {
			return stored, nil
		},
		updateContentFunc: func(ctx context.Context, tid, id string, c *domain.Campaign, reset bool) error {
			if reset {
				t.Fatal("a draft edit should not reset an approval that does not exist")
			}
			return nil
		},
	}
	uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)

	if _, err := uc.UpdateCampaign(context.Background(), tenantID, campaignID, &domain.Campaign{
		Title:       "Renamed",
		MessageBody: "Body",
	}); err != nil {
		t.Fatalf("UpdateCampaign returned error: %v", err)
	}
}

func TestUpdateCampaignRejectsCampaignsUnderReviewOrSent(t *testing.T) {
	cases := []string{"scheduled", "processing", "completed", "cancelled", "pending_approval", "approved"}
	for _, status := range cases {
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{ID: id, TenantID: tenantID, Status: status}, nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		if _, err := uc.UpdateCampaign(context.Background(), "tenant-1", "campaign-1", &domain.Campaign{
			Title:       "Title",
			MessageBody: "Body",
		}); err == nil || !strings.Contains(err.Error(), "can no longer be edited") {
			t.Fatalf("status %q should not be editable, got %v", status, err)
		}
	}
}

func TestUpdateCampaignAllowsPendingApprovalWithBrandChanges(t *testing.T) {
	for _, status := range []string{"pending_approval", "approved"} {
		reset := false
		repo := &mockCampaignRepo{
			getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
				return &domain.Campaign{
					ID:                   id,
					TenantID:             tenantID,
					Status:               status,
					LatestReviewDecision: domain.ReviewDecisionChangesRequested,
				}, nil
			},
			updateContentFunc: func(ctx context.Context, tenantID, id string, c *domain.Campaign, resetApproval bool) error {
				reset = resetApproval
				return nil
			},
		}
		uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
		if _, err := uc.UpdateCampaign(context.Background(), "tenant-1", "campaign-1", &domain.Campaign{
			Title:       "Title",
			MessageBody: "Body",
		}); err != nil {
			t.Fatalf("status %q with brand changes should be editable: %v", status, err)
		}
		if !reset {
			t.Fatalf("status %q should reset approval on edit", status)
		}
	}
}

func TestUpdateCampaignValidatesContent(t *testing.T) {
	updateCalled := false
	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
			return &domain.Campaign{ID: id, TenantID: tenantID, Status: "draft"}, nil
		},
		updateContentFunc: func(ctx context.Context, tenantID, id string, c *domain.Campaign, resetApproval bool) error {
			updateCalled = true
			return nil
		},
	}
	uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)

	if _, err := uc.UpdateCampaign(context.Background(), "tenant-1", "campaign-1", &domain.Campaign{
		Title:       "   ",
		MessageBody: "Body",
	}); err == nil || !strings.Contains(err.Error(), "title cannot be empty") {
		t.Fatalf("expected a title validation error, got %v", err)
	}
	if updateCalled {
		t.Fatal("invalid content must not reach the repository")
	}
}

type recordingRevisionNotifier struct {
	calls int
	sent  int
	err   error
}

func (r *recordingRevisionNotifier) NotifyBrandRevision(ctx context.Context, campaign *domain.Campaign, createdBy string) (int, error) {
	r.calls++
	return r.sent, r.err
}

func TestUpdateCampaignEmailsBrandAboutRevision(t *testing.T) {
	stored := &domain.Campaign{ID: "campaign-1", TenantID: "tenant-1", Status: "draft"}
	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
			return stored, nil
		},
		updateContentFunc: func(ctx context.Context, tenantID, id string, c *domain.Campaign, resetApproval bool) error {
			return nil
		},
	}
	uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
	notifier := &recordingRevisionNotifier{sent: 1}
	uc.SetBrandRevisionNotifier(notifier)

	updated, err := uc.UpdateCampaign(context.Background(), "tenant-1", "campaign-1", &domain.Campaign{
		Title:       "Title",
		MessageBody: "Body",
	})
	if err != nil {
		t.Fatalf("UpdateCampaign returned error: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("expected the brand to be notified once, got %d", notifier.calls)
	}
	if !updated.BrandRevisionNotified {
		t.Fatal("the response should report that the brand was emailed")
	}
}

func TestUpdateCampaignSucceedsWhenBrandEmailFails(t *testing.T) {
	stored := &domain.Campaign{ID: "campaign-1", TenantID: "tenant-1", Status: "draft"}
	repo := &mockCampaignRepo{
		getByIDFunc: func(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
			return stored, nil
		},
	}
	uc := NewCampaignUseCase(repo, nil, nil, &mockPublisher{}, nil)
	uc.SetBrandRevisionNotifier(&recordingRevisionNotifier{err: errors.New("mail unavailable")})

	updated, err := uc.UpdateCampaign(context.Background(), "tenant-1", "campaign-1", &domain.Campaign{
		Title:       "Title",
		MessageBody: "Body",
	})
	if err != nil {
		t.Fatalf("a mail failure must not undo the save: %v", err)
	}
	if updated.BrandRevisionNotified {
		t.Fatal("the response must not claim the brand was emailed")
	}
}
