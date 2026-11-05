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
	"net/mail"
	"strings"
	"time"
	"unicode"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
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

// brandDecisionNotifier alerts the agency that the brand has decided. It is an
// interface so the review flow does not depend on the notifier implementation.
type brandDecisionNotifier interface {
	NotifyBrandDecision(ctx context.Context, tenantID, campaignID, campaignTitle, decision, note, reviewerLabel, submittedBy string)
}

type ReviewUseCase struct {
	repo          domain.ReviewRepository
	campaigns     domain.CampaignRepository
	preflight     campaignPreflighter
	mailer        *utils.Mailer
	appBaseURL    string
	brandNotifier brandDecisionNotifier
}

func NewReviewUseCase(repo domain.ReviewRepository, campaigns domain.CampaignRepository, preflight campaignPreflighter, mailer *utils.Mailer, appBaseURL string, brandNotifier brandDecisionNotifier) *ReviewUseCase {
	return &ReviewUseCase{
		repo:          repo,
		campaigns:     campaigns,
		preflight:     preflight,
		mailer:        mailer,
		appBaseURL:    cleanAppBaseURL(appBaseURL),
		brandNotifier: brandNotifier,
	}
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
	if campaign.Status == "rejected" {
		return nil, domain.ErrReviewNotShareable
	}

	ttl := reviewTokenTTLDefault
	if ttlDays > 0 {
		ttl = time.Duration(ttlDays) * 24 * time.Hour
	}
	return u.mintLink(ctx, campaign, label, createdBy, ttl, "")
}

// EmailBrandLink mints a fresh link and emails it to the brand. Before the
// campaign is sent the email asks them to review it; afterwards it points at the
// report. Tokens are stored hashed, so an email is the only way to deliver a
// link later; each send produces its own revocable link.
func (u *ReviewUseCase) EmailBrandLink(ctx context.Context, tenantID, campaignID, recipientEmail, recipientName, sentBy string) (*ReviewLink, error) {
	email := strings.TrimSpace(recipientEmail)
	addr, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(addr.Address, email) {
		return nil, domain.ErrReviewInvalidRecipient
	}

	campaign, err := u.campaigns.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	switch campaign.Status {
	case "draft", "pending_approval", "approved", "scheduled", "processing", "completed", "paused", "cancelled":
	case "rejected":
		return nil, domain.ErrReviewNotShareable
	default:
		return nil, domain.ErrReviewOutcomeUnavailable
	}

	if u.mailer == nil {
		return nil, fmt.Errorf("email is not configured")
	}

	label := strings.TrimSpace(recipientName)
	if label == "" {
		label = email
	}
	link, err := u.mintLink(ctx, campaign, label, sentBy, 2*reviewTokenTTLDefault, email)
	if err != nil {
		return nil, err
	}

	workspaceName := "Your agency"
	if brand, err := u.repo.GetBranding(ctx, tenantID); err == nil && brand != nil && strings.TrimSpace(brand.Name) != "" {
		workspaceName = brand.Name
	}
	linkURL := fmt.Sprintf("%s/review/%s", u.appBaseURL, link.Token)
	if reportStage(campaign.Status) {
		u.mailer.SendCampaignReport(email, workspaceName, campaign.Title, linkURL, link.ExpiresAt)
	} else {
		u.mailer.SendBrandReviewRequest(email, workspaceName, campaign.Title, linkURL, link.ExpiresAt)
	}

	return link, nil
}

// NotifyBrandRevision emails the brands that were previously sent a review link
// for this campaign. Their last decision may describe a version that no longer
// exists, so a fresh link is minted for each recipient (stored tokens are hashes
// and cannot be replayed). Returns how many recipients were emailed.
func (u *ReviewUseCase) NotifyBrandRevision(ctx context.Context, campaign *domain.Campaign, createdBy string) (int, error) {
	if u.mailer == nil || campaign == nil {
		return 0, nil
	}

	tokens, err := u.repo.ListTokens(ctx, campaign.TenantID, campaign.ID)
	if err != nil {
		return 0, err
	}

	type recipient struct {
		email string
		label string
	}
	seen := make(map[string]bool, len(tokens))
	recipients := make([]recipient, 0, len(tokens))
	for _, token := range tokens {
		// ListTokens is newest first, so the first label seen for an address is
		// the most recent one the operator used.
		email := strings.ToLower(strings.TrimSpace(token.RecipientEmail))
		if email == "" || token.RevokedAt != nil || seen[email] {
			continue
		}
		seen[email] = true
		recipients = append(recipients, recipient{email: email, label: token.Label})
	}
	if len(recipients) == 0 {
		return 0, nil
	}

	workspaceName := "Your agency"
	if brand, brandErr := u.repo.GetBranding(ctx, campaign.TenantID); brandErr == nil && brand != nil && strings.TrimSpace(brand.Name) != "" {
		workspaceName = brand.Name
	}

	sent := 0
	for _, recipient := range recipients {
		link, mintErr := u.mintLink(ctx, campaign, recipient.label, createdBy, 2*reviewTokenTTLDefault, recipient.email)
		if mintErr != nil {
			log.Printf("[REVIEW] could not create a revision link for campaign %s: %v\n", campaign.ID, mintErr)
			continue
		}
		linkURL := fmt.Sprintf("%s/review/%s", u.appBaseURL, link.Token)
		u.mailer.SendBrandRevisionRequest(recipient.email, workspaceName, campaign.Title, linkURL, link.ExpiresAt)
		sent++
	}
	return sent, nil
}

// reportStage separates "the brand should look at the results" from "the brand
// should approve or request changes".
func reportStage(status string) bool {
	switch status {
	case "scheduled", "processing", "completed", "paused", "cancelled":
		return true
	default:
		return false
	}
}

// mintLink persists a hashed token and returns the raw URL component once.
// recipientEmail is empty for links the operator copies manually.
func (u *ReviewUseCase) mintLink(ctx context.Context, campaign *domain.Campaign, label, createdBy string, ttl time.Duration, recipientEmail string) (*ReviewLink, error) {
	label = strings.TrimSpace(label)
	if len(label) > 120 {
		label = label[:120]
	}
	if ttl <= 0 {
		ttl = reviewTokenTTLDefault
	}
	if ttl > reviewTokenTTLMax {
		ttl = reviewTokenTTLMax
	}

	raw, err := generateReviewToken()
	if err != nil {
		return nil, err
	}

	token := &domain.ReviewToken{
		TenantID:       campaign.TenantID,
		CampaignID:     campaign.ID,
		Label:          label,
		RecipientEmail: strings.TrimSpace(recipientEmail),
		CreatedBy:      createdBy,
		ExpiresAt:      time.Now().Add(ttl).UTC(),
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

// Decide records the brand's answer. A link stays usable while the campaign is
// open for review, so a decision can be updated after the agency revises the
// content; every answer is kept and the most recent one governs. Approval
// advances a campaign that is awaiting approval; a change request is recorded
// without moving the campaign into a state the agency cannot edit out of.
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

	// Record first: if this insert fails, the campaign must not move. A later
	// re-decision appends another event; reads pick the newest one.
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

	if u.brandNotifier != nil {
		submittedBy := ""
		if campaign.SubmittedBy != nil {
			submittedBy = *campaign.SubmittedBy
		}
		u.brandNotifier.NotifyBrandDecision(ctx, token.TenantID, token.CampaignID, campaign.Title, normalized, note, token.Label, submittedBy)
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

	var latest *domain.ReviewEvent
	if event, err := u.repo.LatestDecision(ctx, token.TenantID, token.CampaignID); err == nil && event != nil {
		latest = event
		view.Decision = &domain.PublicReviewDecision{
			Decision:  event.Decision,
			Note:      event.Note,
			DecidedAt: event.CreatedAt,
		}
		if campaign.ContentRevisedAt != nil && campaign.ContentRevisedAt.After(event.CreatedAt) {
			view.RevisedSinceDecision = true
		}
	} else if err != nil {
		log.Printf("[REVIEW] decision lookup failed for campaign %s: %v\n", campaign.ID, err)
	}
	view.ApprovalTrail = u.buildApprovalTrail(ctx, token, campaign, latest, view.Brand.Name)

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

// buildApprovalTrail assembles the chain of accountability a brand cares
// about. Names, never raw user IDs, reach the public payload.
func (u *ReviewUseCase) buildApprovalTrail(ctx context.Context, token *domain.ReviewToken, campaign *domain.Campaign, event *domain.ReviewEvent, brandName string) []domain.PublicReviewTrailEntry {
	trail := make([]domain.PublicReviewTrailEntry, 0, 3)
	names := u.resolveStaffNames(ctx, campaign.TenantID, campaign.SubmittedBy, campaign.ApprovedBy)

	if campaign.SubmittedBy != nil {
		trail = append(trail, domain.PublicReviewTrailEntry{
			Stage:     "submitted",
			ActorRole: "operator",
			Actor:     staffLabel(names, *campaign.SubmittedBy),
			At:        campaign.SubmittedAt,
		})
	}

	switch {
	case campaign.ApprovedBy != nil && !strings.HasPrefix(*campaign.ApprovedBy, "client"):
		trail = append(trail, domain.PublicReviewTrailEntry{
			Stage:     "approved",
			ActorRole: "operator",
			Actor:     staffLabel(names, *campaign.ApprovedBy),
			At:        campaign.ApprovedAt,
		})
	case campaign.Status == "rejected":
		entry := domain.PublicReviewTrailEntry{Stage: "rejected", ActorRole: "operator", Actor: "Internal review"}
		if campaign.RejectionReason != nil {
			entry.Note = *campaign.RejectionReason
		}
		trail = append(trail, entry)
	}

	if event != nil {
		stage := "brand_approved"
		if event.Decision == domain.ReviewDecisionChangesRequested {
			stage = "brand_changes_requested"
		}
		actor := strings.TrimSpace(token.Label)
		if actor == "" {
			actor = strings.TrimSpace(brandName)
		}
		if actor == "" {
			actor = "Brand reviewer"
		}
		trail = append(trail, domain.PublicReviewTrailEntry{
			Stage:     stage,
			ActorRole: "brand",
			Actor:     actor,
			Note:      event.Note,
			At:        &event.CreatedAt,
		})
	}

	return trail
}

func (u *ReviewUseCase) resolveStaffNames(ctx context.Context, tenantID string, ids ...*string) map[string]string {
	wanted := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != nil && strings.TrimSpace(*id) != "" {
			wanted = append(wanted, *id)
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	names, err := u.repo.ResolveStaffNames(ctx, tenantID, wanted)
	if err != nil {
		log.Printf("[REVIEW] staff lookup failed for tenant %s: %v\n", tenantID, err)
		return nil
	}
	return names
}

// staffLabel never leaks an internal identifier to a brand. Email domains are
// dropped, and anything that still looks like a handle or user ID falls back to
// a neutral role instead of being shown as if it were a person's name.
func staffLabel(names map[string]string, userID string) string {
	raw := strings.TrimSpace(names[userID])
	if raw == "" {
		return operatorFallback
	}
	if strings.Contains(raw, "@") {
		raw = nameFromEmail(raw)
	}
	if !plausiblePersonName(raw) {
		return operatorFallback
	}
	return raw
}

const operatorFallback = "An operator"

// plausiblePersonName accepts letters, spaces and the punctuation real names
// use. Digits, underscores and other separators mark an account identifier.
func plausiblePersonName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	hasLetter := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case r == ' ' || r == '\'' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return hasLetter
}

func nameFromEmail(email string) string {
	local := email
	if at := strings.Index(local, "@"); at >= 0 {
		local = local[:at]
	}
	parts := strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
	for i, part := range parts {
		runes := []rune(strings.ToLower(part))
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		parts[i] = string(runes)
	}
	if len(parts) == 0 {
		return "An operator"
	}
	return strings.Join(parts, " ")
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
