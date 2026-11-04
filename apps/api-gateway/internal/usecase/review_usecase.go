package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

const (
	reviewTokenTTLDefault = 7 * 24 * time.Hour
	reviewTokenTTLMax     = 30 * 24 * time.Hour
	reviewNoteMaxLen      = 2000
)

// campaignPreflighter lets the review portal reuse the same audience counts the
// sender sees, without exposing contact rows to the client.
type campaignPreflighter interface {
	Preflight(ctx context.Context, tenantID, campaignID string) (*domain.CampaignPreflight, error)
}

type ReviewUseCase struct {
	repo      domain.ReviewRepository
	campaigns domain.CampaignRepository
	preflight campaignPreflighter
}

func NewReviewUseCase(repo domain.ReviewRepository, campaigns domain.CampaignRepository, preflight campaignPreflighter) *ReviewUseCase {
	return &ReviewUseCase{repo: repo, campaigns: campaigns, preflight: preflight}
}

type ReviewLink struct {
	Token     string    `json:"token"`
	Label     string    `json:"label"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateLink mints a bearer token for a campaign that is still open for review.
// The raw token is returned once; only its hash is stored.
func (u *ReviewUseCase) CreateLink(ctx context.Context, tenantID, campaignID, label, createdBy string, ttlDays int) (*ReviewLink, error) {
	campaign, err := u.campaigns.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	switch campaign.Status {
	case "draft", "pending_approval", "approved":
	default:
		return nil, fmt.Errorf("a review link cannot be created while the campaign is %s", campaign.Status)
	}

	label = strings.TrimSpace(label)
	if len(label) > 120 {
		label = label[:120]
	}

	ttl := reviewTokenTTLDefault
	if ttlDays > 0 {
		ttl = time.Duration(ttlDays) * 24 * time.Hour
	}
	if ttl > reviewTokenTTLMax {
		ttl = reviewTokenTTLMax
	}

	raw, err := generateReviewToken()
	if err != nil {
		return nil, err
	}

	token := &domain.ReviewToken{
		TenantID:   tenantID,
		CampaignID: campaignID,
		Label:      label,
		CreatedBy:  createdBy,
		ExpiresAt:  time.Now().Add(ttl).UTC(),
	}
	if err := u.repo.CreateToken(ctx, token, hashReviewToken(raw)); err != nil {
		return nil, err
	}

	return &ReviewLink{Token: raw, Label: label, ExpiresAt: token.ExpiresAt}, nil
}

func (u *ReviewUseCase) ListLinks(ctx context.Context, tenantID, campaignID string) ([]*domain.ReviewToken, error) {
	return u.repo.ListTokens(ctx, tenantID, campaignID)
}

func (u *ReviewUseCase) RevokeLink(ctx context.Context, tenantID, campaignID, tokenID string) error {
	return u.repo.RevokeToken(ctx, tenantID, campaignID, tokenID)
}

func (u *ReviewUseCase) GetPublicReview(ctx context.Context, rawToken string) (*domain.PublicReview, error) {
	token, err := u.resolveToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	return u.buildPublicReview(ctx, token)
}

// Decide records the client's one-time answer. Approval advances a campaign
// that is awaiting approval; a change request is recorded without moving the
// campaign into a state the agency cannot edit out of.
func (u *ReviewUseCase) Decide(ctx context.Context, rawToken, decision, note, reviewerIP string) (*domain.PublicReview, error) {
	normalized, err := ParseReviewDecision(decision)
	if err != nil {
		return nil, err
	}

	note = strings.TrimSpace(note)
	if len(note) > reviewNoteMaxLen {
		note = note[:reviewNoteMaxLen]
	}
	if normalized == domain.ReviewDecisionChangesRequested && note == "" {
		return nil, domain.ErrReviewNoteRequired
	}

	token, err := u.resolveToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}

	campaign, err := u.campaigns.GetByID(ctx, token.TenantID, token.CampaignID)
	if err != nil {
		return nil, err
	}

	switch campaign.Status {
	case "scheduled", "processing", "completed", "paused", "cancelled", "rejected":
		return nil, domain.ErrReviewNotReviewable
	}

	// Record first: the unique constraint on token_id is what makes a decision
	// single-use. Advancing the campaign before this check would let a repeated
	// decision change status while still returning an error.
	event := &domain.ReviewEvent{
		TenantID:   token.TenantID,
		CampaignID: token.CampaignID,
		TokenID:    token.ID,
		Decision:   normalized,
		Note:       note,
		ReviewerIP: reviewerIP,
	}
	if err := u.repo.RecordDecision(ctx, event); err != nil {
		return nil, err
	}

	if normalized == domain.ReviewDecisionApproved && campaign.Status == "pending_approval" {
		approvedBy := "client_review"
		if token.Label != "" {
			approvedBy = "client:" + token.Label
		}
		if err := u.campaigns.ApproveCampaign(ctx, token.TenantID, token.CampaignID, approvedBy); err != nil {
			// A concurrent reviewer may have approved it first; that is the same
			// outcome the client asked for, so treat it as success.
			if latest, getErr := u.campaigns.GetByID(ctx, token.TenantID, token.CampaignID); getErr != nil || latest.Status != "approved" {
				log.Printf("[REVIEW] decision stored but campaign %s could not be approved: %v\n", token.CampaignID, err)
				return nil, err
			}
		}
	}

	return u.buildPublicReview(ctx, token)
}

func (u *ReviewUseCase) GetOutcomeReport(ctx context.Context, rawToken string) (*domain.PublicOutcomeReport, *domain.ReviewBrand, error) {
	token, err := u.resolveToken(ctx, rawToken)
	if err != nil {
		return nil, nil, err
	}
	campaign, err := u.campaigns.GetByID(ctx, token.TenantID, token.CampaignID)
	if err != nil {
		return nil, nil, err
	}
	if !hasOutcome(campaign.Status) {
		return nil, nil, domain.ErrReviewOutcomeUnavailable
	}
	brand, _ := u.repo.GetBranding(ctx, token.TenantID)
	report, err := u.buildOutcomeReport(ctx, campaign)
	if err != nil {
		return nil, nil, err
	}
	return report, brand, nil
}

func ParseReviewDecision(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "approve", "approved":
		return domain.ReviewDecisionApproved, nil
	case "request_changes", "changes_requested", "request-changes":
		return domain.ReviewDecisionChangesRequested, nil
	default:
		return "", domain.ErrReviewInvalidDecision
	}
}

func (u *ReviewUseCase) resolveToken(ctx context.Context, rawToken string) (*domain.ReviewToken, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, domain.ErrReviewLinkNotFound
	}

	token, err := u.repo.FindTokenByHash(ctx, hashReviewToken(rawToken))
	if err != nil {
		return nil, err
	}
	if token == nil || token.RevokedAt != nil {
		return nil, domain.ErrReviewLinkNotFound
	}
	if time.Now().After(token.ExpiresAt) {
		return nil, domain.ErrReviewLinkExpired
	}

	if err := u.repo.TouchToken(ctx, token.ID); err != nil {
		log.Printf("[REVIEW] failed to record view for token %s: %v\n", token.ID, err)
	}
	return token, nil
}

func (u *ReviewUseCase) buildPublicReview(ctx context.Context, token *domain.ReviewToken) (*domain.PublicReview, error) {
	campaign, err := u.campaigns.GetByID(ctx, token.TenantID, token.CampaignID)
	if err != nil {
		return nil, err
	}

	view := &domain.PublicReview{
		CampaignID:   campaign.ID,
		Title:        campaign.Title,
		MessageBody:  campaign.MessageBody,
		MediaURL:     campaign.MediaURL,
		DeliveryType: campaign.DeliveryType,
		Channels:     parseStringList(campaign.SelectedChannels),
		Status:       campaign.Status,
		ScheduledAt:  campaign.ScheduledAt,
		ExpiresAt:    token.ExpiresAt,
	}
	if view.Channels == nil {
		view.Channels = []string{}
	}

	if brand, err := u.repo.GetBranding(ctx, token.TenantID); err == nil && brand != nil {
		view.Brand = *brand
	}

	if preflight, err := u.preflight.Preflight(ctx, token.TenantID, token.CampaignID); err == nil && preflight != nil {
		view.Audience = domain.PublicReviewAudience{
			Total:     preflight.TotalTargets,
			Sendable:  preflight.Sendable,
			OptedOut:  preflight.OptedOut,
			NoConsent: preflight.NoConsent,
		}
	} else if err != nil {
		log.Printf("[REVIEW] preflight failed for campaign %s: %v\n", campaign.ID, err)
	}

	if event, err := u.repo.LatestDecision(ctx, token.TenantID, token.CampaignID); err == nil && event != nil {
		view.Decision = &domain.PublicReviewDecision{
			Decision:  event.Decision,
			Note:      event.Note,
			DecidedAt: event.CreatedAt,
		}
	} else if err != nil {
		log.Printf("[REVIEW] decision lookup failed for campaign %s: %v\n", campaign.ID, err)
	}

	if hasOutcome(campaign.Status) {
		if report, err := u.buildOutcomeReport(ctx, campaign); err == nil {
			view.Report = report
		} else {
			log.Printf("[REVIEW] outcome report failed for campaign %s: %v\n", campaign.ID, err)
		}
	}

	return view, nil
}

func (u *ReviewUseCase) buildOutcomeReport(ctx context.Context, campaign *domain.Campaign) (*domain.PublicOutcomeReport, error) {
	stats, err := u.campaigns.GetCampaignStats(ctx, campaign.TenantID, campaign.ID)
	if err != nil {
		return nil, err
	}

	deliveryRate := 0.0
	if stats.TotalTargets > 0 {
		deliveryRate = math.Round((float64(stats.Delivered)/float64(stats.TotalTargets))*1000) / 10
	}

	return &domain.PublicOutcomeReport{
		CampaignID:   campaign.ID,
		Title:        campaign.Title,
		Status:       campaign.Status,
		TotalTargets: stats.TotalTargets,
		Sent:         stats.Sent,
		Delivered:    stats.Delivered,
		Failed:       stats.Failed,
		DeliveryRate: deliveryRate,
		GeneratedAt:  time.Now().UTC(),
	}, nil
}

func hasOutcome(status string) bool {
	switch status {
	case "processing", "completed", "paused", "cancelled":
		return true
	default:
		return false
	}
}

func generateReviewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate review token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashReviewToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
