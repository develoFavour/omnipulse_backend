package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type mockReviewRepo struct {
	domain.ReviewRepository

	token      *domain.ReviewToken
	storedHash string
	recorded   *domain.ReviewEvent
	recordErr  error
	latest     *domain.ReviewEvent
	revoked    bool
	lastViewed bool
	brand      *domain.ReviewBrand
}

func (m *mockReviewRepo) CreateToken(ctx context.Context, token *domain.ReviewToken, tokenHash string) error {
	m.token = token
	m.storedHash = tokenHash
	return nil
}

func (m *mockReviewRepo) FindTokenByHash(ctx context.Context, tokenHash string) (*domain.ReviewToken, error) {
	if m.storedHash != "" && tokenHash != m.storedHash {
		return nil, nil
	}
	return m.token, nil
}

func (m *mockReviewRepo) TouchToken(ctx context.Context, tokenID string) error {
	m.lastViewed = true
	return nil
}

func (m *mockReviewRepo) RecordDecision(ctx context.Context, event *domain.ReviewEvent) error {
	if m.recordErr != nil {
		return m.recordErr
	}
	m.recorded = event
	return nil
}

func (m *mockReviewRepo) LatestDecision(ctx context.Context, tenantID, campaignID string) (*domain.ReviewEvent, error) {
	return m.latest, nil
}

func (m *mockReviewRepo) GetBranding(ctx context.Context, tenantID string) (*domain.ReviewBrand, error) {
	return m.brand, nil
}

type mockReviewCampaigns struct {
	domain.CampaignRepository

	campaign   *domain.Campaign
	approvedBy string
	approved   bool
	stats      *domain.CampaignStats
}

func (m *mockReviewCampaigns) GetByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	if m.campaign == nil {
		return nil, errors.New("campaign missing")
	}
	return m.campaign, nil
}

func (m *mockReviewCampaigns) ApproveCampaign(ctx context.Context, tenantID, id, approvedBy string) error {
	m.approved = true
	m.approvedBy = approvedBy
	m.campaign.Status = "approved"
	return nil
}

func (m *mockReviewCampaigns) GetCampaignStats(ctx context.Context, tenantID, campaignID string) (*domain.CampaignStats, error) {
	if m.stats == nil {
		return &domain.CampaignStats{}, nil
	}
	return m.stats, nil
}

type stubPreflighter struct {
	result *domain.CampaignPreflight
}

func (s stubPreflighter) Preflight(ctx context.Context, tenantID, campaignID string) (*domain.CampaignPreflight, error) {
	return s.result, nil
}

func newReviewFixture(status string) (*ReviewUseCase, *mockReviewRepo, *mockReviewCampaigns) {
	reviewRepo := &mockReviewRepo{brand: &domain.ReviewBrand{Name: "Acme Agency"}}
	campaignRepo := &mockReviewCampaigns{
		campaign: &domain.Campaign{
			ID:               "campaign-1",
			TenantID:         "tenant-1",
			Title:            "November launch",
			MessageBody:      "Hello from Acme",
			Status:           status,
			SelectedChannels: `["whatsapp"]`,
		},
	}
	uc := NewReviewUseCase(reviewRepo, campaignRepo, stubPreflighter{
		result: &domain.CampaignPreflight{TotalTargets: 10, Sendable: 8, OptedOut: 2},
	})
	return uc, reviewRepo, campaignRepo
}

func TestCreateReviewLinkStoresOnlyHash(t *testing.T) {
	uc, reviewRepo, _ := newReviewFixture("pending_approval")

	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", " Acme marketing lead ", "user-1", 3)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}
	if link.Token == "" {
		t.Fatal("expected a raw token for the caller")
	}
	if reviewRepo.storedHash == link.Token {
		t.Fatal("raw token must not be persisted")
	}
	if len(reviewRepo.storedHash) != 64 {
		t.Fatalf("expected a sha256 hex hash, got %q", reviewRepo.storedHash)
	}
	if reviewRepo.storedHash != hashReviewToken(link.Token) {
		t.Fatal("stored hash does not match the returned token")
	}
	if link.Label != "Acme marketing lead" {
		t.Fatalf("label was not trimmed, got %q", link.Label)
	}
}

func TestDecideApproveAdvancesPendingCampaign(t *testing.T) {
	uc, reviewRepo, campaignRepo := newReviewFixture("pending_approval")
	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "Acme", "user-1", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}

	view, err := uc.Decide(context.Background(), link.Token, "approve", "", "203.0.113.9")
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if !campaignRepo.approved {
		t.Fatal("expected the campaign to be approved")
	}
	if campaignRepo.approvedBy != "client:Acme" {
		t.Fatalf("unexpected approver label %q", campaignRepo.approvedBy)
	}
	if reviewRepo.recorded == nil || reviewRepo.recorded.Decision != domain.ReviewDecisionApproved {
		t.Fatalf("expected an approved decision event, got %+v", reviewRepo.recorded)
	}
	if reviewRepo.recorded.ReviewerIP != "203.0.113.9" {
		t.Fatalf("unexpected reviewer ip %q", reviewRepo.recorded.ReviewerIP)
	}
	if view.Audience.Sendable != 8 || view.Audience.OptedOut != 2 {
		t.Fatalf("unexpected audience projection %+v", view.Audience)
	}
}

func TestDecideRequestChangesRequiresNoteAndDoesNotApprove(t *testing.T) {
	uc, reviewRepo, campaignRepo := newReviewFixture("pending_approval")
	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "", "user-1", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}

	if _, err := uc.Decide(context.Background(), link.Token, "request_changes", "   ", ""); !errors.Is(err, domain.ErrReviewNoteRequired) {
		t.Fatalf("expected ErrReviewNoteRequired, got %v", err)
	}

	_, err = uc.Decide(context.Background(), link.Token, "request_changes", "Please fix the discount code", "")
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if campaignRepo.approved {
		t.Fatal("a change request must not approve the campaign")
	}
	if reviewRepo.recorded.Decision != domain.ReviewDecisionChangesRequested {
		t.Fatalf("unexpected decision %q", reviewRepo.recorded.Decision)
	}
}

func TestDecideRejectsExpiredRevokedAndRepeatedLinks(t *testing.T) {
	uc, reviewRepo, _ := newReviewFixture("pending_approval")
	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "", "user-1", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}

	past := time.Now().Add(-time.Minute)
	reviewRepo.token.ExpiresAt = past
	if _, err := uc.Decide(context.Background(), link.Token, "approve", "", ""); !errors.Is(err, domain.ErrReviewLinkExpired) {
		t.Fatalf("expected ErrReviewLinkExpired, got %v", err)
	}

	reviewRepo.token.ExpiresAt = time.Now().Add(time.Hour)
	reviewRepo.token.RevokedAt = &past
	if _, err := uc.Decide(context.Background(), link.Token, "approve", "", ""); !errors.Is(err, domain.ErrReviewLinkNotFound) {
		t.Fatalf("expected ErrReviewLinkNotFound, got %v", err)
	}

	reviewRepo.token.RevokedAt = nil
	reviewRepo.recordErr = domain.ErrReviewAlreadyDecided
	if _, err := uc.Decide(context.Background(), link.Token, "approve", "", ""); !errors.Is(err, domain.ErrReviewAlreadyDecided) {
		t.Fatalf("expected ErrReviewAlreadyDecided, got %v", err)
	}
}

func TestDecideDoesNotApproveWhenDecisionAlreadyRecorded(t *testing.T) {
	uc, reviewRepo, campaignRepo := newReviewFixture("pending_approval")
	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "", "user-1", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}

	reviewRepo.recordErr = domain.ErrReviewAlreadyDecided
	if _, err := uc.Decide(context.Background(), link.Token, "approve", "", ""); !errors.Is(err, domain.ErrReviewAlreadyDecided) {
		t.Fatalf("expected ErrReviewAlreadyDecided, got %v", err)
	}
	if campaignRepo.approved {
		t.Fatal("a rejected duplicate must not advance the campaign")
	}
}

func TestParseReviewDecision(t *testing.T) {
	cases := map[string]string{
		"approve":           domain.ReviewDecisionApproved,
		" APPROVED ":        domain.ReviewDecisionApproved,
		"request_changes":   domain.ReviewDecisionChangesRequested,
		"request-changes":   domain.ReviewDecisionChangesRequested,
		"changes_requested": domain.ReviewDecisionChangesRequested,
	}
	for input, want := range cases {
		got, err := ParseReviewDecision(input)
		if err != nil || got != want {
			t.Fatalf("ParseReviewDecision(%q) = (%q, %v), want %q", input, got, err, want)
		}
	}
	if _, err := ParseReviewDecision("maybe"); !errors.Is(err, domain.ErrReviewInvalidDecision) {
		t.Fatalf("expected ErrReviewInvalidDecision, got %v", err)
	}
}
