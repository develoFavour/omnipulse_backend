package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/config"
	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type mockReviewRepo struct {
	domain.ReviewRepository

	token         *domain.ReviewToken
	tokens        []*domain.ReviewToken
	created       []*domain.ReviewToken
	storedHash    string
	recorded      *domain.ReviewEvent
	recordErr     error
	recordedTimes int
	latest        *domain.ReviewEvent
	revoked       bool
	lastViewed    bool
	brand         *domain.ReviewBrand
	names         map[string]string
}

func (m *mockReviewRepo) CreateToken(ctx context.Context, token *domain.ReviewToken, tokenHash string) error {
	m.token = token
	m.created = append(m.created, token)
	m.storedHash = tokenHash
	return nil
}

func (m *mockReviewRepo) ListTokens(ctx context.Context, tenantID, campaignID string) ([]*domain.ReviewToken, error) {
	return m.tokens, nil
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
	m.recordedTimes++
	return nil
}

func (m *mockReviewRepo) LatestDecision(ctx context.Context, tenantID, campaignID string) (*domain.ReviewEvent, error) {
	return m.latest, nil
}

func (m *mockReviewRepo) GetBranding(ctx context.Context, tenantID string) (*domain.ReviewBrand, error) {
	return m.brand, nil
}

func (m *mockReviewRepo) ResolveStaffNames(ctx context.Context, tenantID string, userIDs []string) (map[string]string, error) {
	resolved := make(map[string]string, len(userIDs))
	for _, id := range userIDs {
		if name, ok := m.names[id]; ok {
			resolved[id] = name
		}
	}
	return resolved, nil
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

type recordingBrandNotifier struct {
	calls         int
	tenantID      string
	campaignID    string
	campaignTitle string
	decision      string
	note          string
	reviewerLabel string
	submittedBy   string
}

func (r *recordingBrandNotifier) NotifyBrandDecision(ctx context.Context, tenantID, campaignID, campaignTitle, decision, note, reviewerLabel, submittedBy string) {
	r.calls++
	r.tenantID = tenantID
	r.campaignID = campaignID
	r.campaignTitle = campaignTitle
	r.decision = decision
	r.note = note
	r.reviewerLabel = reviewerLabel
	r.submittedBy = submittedBy
}

func newReviewFixture(status string) (*ReviewUseCase, *mockReviewRepo, *mockReviewCampaigns) {
	uc, reviewRepo, campaignRepo, _ := newReviewFixtureWithNotifier(status)
	return uc, reviewRepo, campaignRepo
}

func newReviewFixtureWithNotifier(status string) (*ReviewUseCase, *mockReviewRepo, *mockReviewCampaigns, *recordingBrandNotifier) {
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
	notifier := &recordingBrandNotifier{}
	uc := NewReviewUseCase(reviewRepo, campaignRepo, stubPreflighter{
		result: &domain.CampaignPreflight{TotalTargets: 10, Sendable: 8, OptedOut: 2},
	}, nil, "", notifier)
	return uc, reviewRepo, campaignRepo, notifier
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

func TestDecideNotifiesAgencyOfBrandDecision(t *testing.T) {
	uc, _, campaignRepo, notifier := newReviewFixtureWithNotifier("pending_approval")
	submittedBy := "user-7"
	campaignRepo.campaign.SubmittedBy = &submittedBy
	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "Acme marketing lead", "user-7", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}

	if _, err := uc.Decide(context.Background(), link.Token, "approve", "Looks great", "203.0.113.9"); err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("expected one brand decision notification, got %d", notifier.calls)
	}
	if notifier.tenantID != "tenant-1" || notifier.campaignID != "campaign-1" {
		t.Fatalf("unexpected notification target %+v", notifier)
	}
	if notifier.campaignTitle != "November launch" {
		t.Fatalf("unexpected campaign title %q", notifier.campaignTitle)
	}
	if notifier.decision != domain.ReviewDecisionApproved {
		t.Fatalf("unexpected decision %q", notifier.decision)
	}
	if notifier.note != "Looks great" {
		t.Fatalf("unexpected note %q", notifier.note)
	}
	if notifier.reviewerLabel != "Acme marketing lead" {
		t.Fatalf("unexpected reviewer label %q", notifier.reviewerLabel)
	}
	if notifier.submittedBy != "user-7" {
		t.Fatalf("unexpected submitter %q", notifier.submittedBy)
	}
}

func TestDecideDoesNotNotifyWhenDecisionIsRejected(t *testing.T) {
	uc, reviewRepo, _, notifier := newReviewFixtureWithNotifier("pending_approval")
	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "Acme", "user-1", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}

	recordErr := errors.New("database unavailable")
	reviewRepo.recordErr = recordErr
	if _, err := uc.Decide(context.Background(), link.Token, "approve", "", ""); !errors.Is(err, recordErr) {
		t.Fatalf("expected the record error, got %v", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("a rejected decision must not notify the agency, got %d calls", notifier.calls)
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

func TestDecideRejectsExpiredAndRevokedLinks(t *testing.T) {
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
}

func TestDecideAcceptsAnotherDecisionOnTheSameLink(t *testing.T) {
	uc, reviewRepo, campaignRepo := newReviewFixture("pending_approval")
	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "", "user-1", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}

	if _, err := uc.Decide(context.Background(), link.Token, "request_changes", "Fix the pricing", ""); err != nil {
		t.Fatalf("first Decide returned error: %v", err)
	}
	if campaignRepo.approved {
		t.Fatal("a change request must not approve the campaign")
	}

	if _, err := uc.Decide(context.Background(), link.Token, "approve", "Pricing fixed", ""); err != nil {
		t.Fatalf("second Decide returned error: %v", err)
	}
	if reviewRepo.recordedTimes != 2 {
		t.Fatalf("expected two recorded decisions, got %d", reviewRepo.recordedTimes)
	}
	if !campaignRepo.approved {
		t.Fatal("a repeat approval on a pending campaign must advance it")
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

func TestCreateReviewLinkAllowedForSentCampaigns(t *testing.T) {
	for _, status := range []string{"scheduled", "processing", "completed", "paused", "cancelled"} {
		uc, _, _ := newReviewFixture(status)
		if _, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "", "user-1", 7); err != nil {
			t.Fatalf("status %s should be shareable: %v", status, err)
		}
	}
}

func TestCreateReviewLinkRejectsRejectedCampaign(t *testing.T) {
	uc, _, _ := newReviewFixture("rejected")
	if _, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "", "user-1", 7); !errors.Is(err, domain.ErrReviewNotShareable) {
		t.Fatalf("expected ErrReviewNotShareable, got %v", err)
	}
}

func TestGetPublicReviewBuildsApprovalTrail(t *testing.T) {
	uc, reviewRepo, campaignRepo := newReviewFixture("completed")
	submittedAt := time.Now().Add(-2 * time.Hour)
	approvedAt := time.Now().Add(-time.Hour)
	submittedBy, approvedBy := "user-1", "user-2"
	campaignRepo.campaign.SubmittedBy = &submittedBy
	campaignRepo.campaign.SubmittedAt = &submittedAt
	campaignRepo.campaign.ApprovedBy = &approvedBy
	campaignRepo.campaign.ApprovedAt = &approvedAt
	reviewRepo.names = map[string]string{
		"user-1": "favour.opia@agency.example",
		"user-2": "Ada Lovelace",
	}
	decidedAt := time.Now().Add(-30 * time.Minute)
	reviewRepo.latest = &domain.ReviewEvent{
		Decision:  domain.ReviewDecisionApproved,
		Note:      "Looks great",
		CreatedAt: decidedAt,
	}

	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "Acme marketing lead", "user-1", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}
	view, err := uc.GetPublicReview(context.Background(), link.Token)
	if err != nil {
		t.Fatalf("GetPublicReview returned error: %v", err)
	}

	if len(view.ApprovalTrail) != 3 {
		t.Fatalf("expected 3 trail entries, got %+v", view.ApprovalTrail)
	}
	if got := view.ApprovalTrail[0]; got.Stage != "submitted" || got.Actor != "Favour Opia" || got.At == nil {
		t.Fatalf("unexpected submitted entry %+v", got)
	}
	if got := view.ApprovalTrail[1]; got.Stage != "approved" || got.Actor != "Ada Lovelace" {
		t.Fatalf("unexpected internal approval entry %+v", got)
	}
	if got := view.ApprovalTrail[2]; got.Stage != "brand_approved" || got.Actor != "Acme marketing lead" || got.Note != "Looks great" {
		t.Fatalf("unexpected brand entry %+v", got)
	}
}

func TestPublicReviewMarksDecisionAsRevised(t *testing.T) {
	uc, reviewRepo, campaignRepo := newReviewFixture("approved")
	decidedAt := time.Now().Add(-time.Hour)
	reviewRepo.latest = &domain.ReviewEvent{
		Decision:  domain.ReviewDecisionChangesRequested,
		Note:      "Fix the pricing",
		CreatedAt: decidedAt,
	}
	revisedAt := time.Now().Add(-30 * time.Minute)
	campaignRepo.campaign.ContentRevisedAt = &revisedAt

	link, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "", "user-1", 7)
	if err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}
	view, err := uc.GetPublicReview(context.Background(), link.Token)
	if err != nil {
		t.Fatalf("GetPublicReview returned error: %v", err)
	}
	if !view.RevisedSinceDecision {
		t.Fatal("expected the previous decision to be marked as revised")
	}

	// A decision made after the revision still describes the current content.
	reviewRepo.latest.CreatedAt = time.Now()
	view, err = uc.GetPublicReview(context.Background(), link.Token)
	if err != nil {
		t.Fatalf("GetPublicReview returned error: %v", err)
	}
	if view.RevisedSinceDecision {
		t.Fatal("a decision made after the revision must not be marked as revised")
	}
}

func TestStaffLabelHidesEmailDomain(t *testing.T) {
	if got := staffLabel(map[string]string{"u": "favour.opia@agency.example"}, "u"); got != "Favour Opia" {
		t.Fatalf("unexpected label %q", got)
	}
	if got := staffLabel(nil, "missing"); got != "An operator" {
		t.Fatalf("unexpected fallback %q", got)
	}
}

func TestStaffLabelHidesAccountIdentifiers(t *testing.T) {
	cases := map[string]string{
		"Opiafavour10":           "An operator",
		"D8338118":               "An operator",
		"user_2abc":              "An operator",
		"d8338118@clerk.example": "An operator",
		"Ada Lovelace":           "Ada Lovelace",
		"Favour":                 "Favour",
		"O'Brien":                "O'Brien",
	}
	for raw, want := range cases {
		if got := staffLabel(map[string]string{"u": raw}, "u"); got != want {
			t.Fatalf("staffLabel(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestEmailBrandLinkAllowedBeforeAndAfterDispatch(t *testing.T) {
	for _, status := range []string{"draft", "pending_approval", "approved", "scheduled", "processing", "completed", "paused", "cancelled"} {
		uc, reviewRepo, _ := newReviewFixture(status)
		uc.mailer = utils.NewMailer(&config.Config{})
		if _, err := uc.EmailBrandLink(context.Background(), "tenant-1", "campaign-1", "brand@acme.example", "", "user-1"); err != nil {
			t.Fatalf("status %s should accept an emailed brand link: %v", status, err)
		}
		if reviewRepo.token == nil {
			t.Fatalf("status %s should mint a link", status)
		}
	}
}

func TestEmailBrandLinkRejectsRejectedCampaign(t *testing.T) {
	uc, _, _ := newReviewFixture("rejected")
	uc.mailer = utils.NewMailer(&config.Config{})
	if _, err := uc.EmailBrandLink(context.Background(), "tenant-1", "campaign-1", "brand@acme.example", "", "user-1"); !errors.Is(err, domain.ErrReviewNotShareable) {
		t.Fatalf("expected ErrReviewNotShareable, got %v", err)
	}
}

func TestEmailBrandLinkValidatesRecipient(t *testing.T) {
	uc, _, _ := newReviewFixture("completed")
	for _, email := range []string{"", "not-an-email", "Brand <brand@acme.example>"} {
		if _, err := uc.EmailBrandLink(context.Background(), "tenant-1", "campaign-1", email, "", "user-1"); !errors.Is(err, domain.ErrReviewInvalidRecipient) {
			t.Fatalf("email %q: expected ErrReviewInvalidRecipient, got %v", email, err)
		}
	}
}

func TestEmailBrandLinkRequiresMailer(t *testing.T) {
	uc, _, _ := newReviewFixture("completed")
	if _, err := uc.EmailBrandLink(context.Background(), "tenant-1", "campaign-1", "brand@acme.example", "", "user-1"); err == nil {
		t.Fatal("expected an error when no mailer is configured")
	}
}

func TestEmailBrandLinkMintsToken(t *testing.T) {
	uc, reviewRepo, _ := newReviewFixture("pending_approval")
	uc.mailer = utils.NewMailer(&config.Config{})

	link, err := uc.EmailBrandLink(context.Background(), "tenant-1", "campaign-1", "brand@acme.example", "Acme marketing", "user-1")
	if err != nil {
		t.Fatalf("EmailBrandLink returned error: %v", err)
	}
	if link.Label != "Acme marketing" {
		t.Fatalf("unexpected label %q", link.Label)
	}
	if reviewRepo.token == nil || reviewRepo.token.Label != "Acme marketing" {
		t.Fatalf("expected the token to be stored with the recipient label, got %+v", reviewRepo.token)
	}
	if ttl := time.Until(reviewRepo.token.ExpiresAt); ttl < 13*24*time.Hour {
		t.Fatalf("report links should last about two weeks, got %v", ttl)
	}
}

func TestEmailBrandLinkStoresRecipientEmail(t *testing.T) {
	uc, reviewRepo, _ := newReviewFixture("pending_approval")
	uc.mailer = utils.NewMailer(&config.Config{})

	if _, err := uc.EmailBrandLink(context.Background(), "tenant-1", "campaign-1", "Brand@Acme.example", "", "user-1"); err != nil {
		t.Fatalf("EmailBrandLink returned error: %v", err)
	}
	if reviewRepo.token == nil || reviewRepo.token.RecipientEmail != "Brand@Acme.example" {
		t.Fatalf("expected the recipient to be stored, got %+v", reviewRepo.token)
	}
}

func TestCreateLinkDoesNotStoreRecipientEmail(t *testing.T) {
	uc, reviewRepo, _ := newReviewFixture("pending_approval")

	if _, err := uc.CreateLink(context.Background(), "tenant-1", "campaign-1", "Copied link", "user-1", 7); err != nil {
		t.Fatalf("CreateLink returned error: %v", err)
	}
	if reviewRepo.token == nil || reviewRepo.token.RecipientEmail != "" {
		t.Fatalf("a manually shared link has no recipient, got %+v", reviewRepo.token)
	}
}

func TestNotifyBrandRevisionEmailsEachRecipientOnce(t *testing.T) {
	uc, reviewRepo, campaignRepo := newReviewFixture("pending_approval")
	uc.mailer = utils.NewMailer(&config.Config{})
	revokedAt := time.Now()
	reviewRepo.tokens = []*domain.ReviewToken{
		{ID: "t1", Label: "Ada", RecipientEmail: "ada@brand.example"},
		{ID: "t2", Label: "Ada stale", RecipientEmail: "ADA@brand.example"},
		{ID: "t3", Label: "Manual link"},
		{ID: "t4", Label: "Gone", RecipientEmail: "gone@brand.example", RevokedAt: &revokedAt},
	}
	campaign, err := campaignRepo.GetByID(context.Background(), "tenant-1", "campaign-1")
	if err != nil {
		t.Fatalf("GetByID returned error: %v", err)
	}

	sent, err := uc.NotifyBrandRevision(context.Background(), campaign, "user-1")
	if err != nil {
		t.Fatalf("NotifyBrandRevision returned error: %v", err)
	}
	if sent != 1 {
		t.Fatalf("expected one emailed recipient, got %d", sent)
	}
	if len(reviewRepo.created) != 1 {
		t.Fatalf("expected one fresh link, got %d", len(reviewRepo.created))
	}
	if reviewRepo.created[0].RecipientEmail != "ada@brand.example" {
		t.Fatalf("unexpected recipient %q", reviewRepo.created[0].RecipientEmail)
	}
	if reviewRepo.created[0].Label != "Ada" {
		t.Fatalf("expected the newest label for the recipient, got %q", reviewRepo.created[0].Label)
	}

	reviewRepo.tokens = []*domain.ReviewToken{{ID: "manual", Label: "Manual link"}}
	reviewRepo.created = nil
	if sent, err = uc.NotifyBrandRevision(context.Background(), campaign, "user-1"); err != nil || sent != 0 {
		t.Fatalf("expected no email without a brand recipient, got sent=%d err=%v", sent, err)
	}
	if len(reviewRepo.created) != 0 {
		t.Fatalf("expected no links without a brand recipient, got %d", len(reviewRepo.created))
	}
}
