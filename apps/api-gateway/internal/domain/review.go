package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrReviewLinkNotFound       = errors.New("review link not found or revoked")
	ErrReviewLinkExpired        = errors.New("review link has expired")
	ErrReviewNotReviewable      = errors.New("campaign is no longer open for client review")
	ErrReviewInvalidDecision    = errors.New("invalid review decision")
	ErrReviewNoteRequired       = errors.New("a note is required when requesting changes")
	ErrReviewOutcomeUnavailable = errors.New("outcome report is not available yet")
	ErrReviewNotShareable       = errors.New("this campaign cannot be shared with a brand")
	ErrReviewInvalidRecipient   = errors.New("enter a valid email address")
)

// Stored decision values. Request payloads use "approve" / "request_changes".
const (
	ReviewDecisionApproved         = "approved"
	ReviewDecisionChangesRequested = "changes_requested"
)

// ReviewToken is a shared, revocable handle to a campaign's client review.
// Only the SHA-256 hash of the bearer token is persisted.
type ReviewToken struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	CampaignID string `json:"campaign_id"`
	Label      string `json:"label"`
	// RecipientEmail is set when the link was emailed to a brand, so revisions
	// can be sent to the same reviewer without asking the operator again.
	RecipientEmail string     `json:"recipient_email,omitempty"`
	CreatedBy      string     `json:"created_by"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	LastViewedAt   *time.Time `json:"last_viewed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	Decision       string     `json:"decision,omitempty"`
}

type ReviewEvent struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	CampaignID string    `json:"campaign_id"`
	TokenID    string    `json:"token_id"`
	Decision   string    `json:"decision"`
	Note       string    `json:"note"`
	ReviewerIP string    `json:"reviewer_ip,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type ReviewBrand struct {
	Name    string `json:"name"`
	LogoURL string `json:"logo_url,omitempty"`
}

// PublicReview is the PII-free projection shown to an unauthenticated client.
// It deliberately exposes audience counts, never contact rows.
type PublicReview struct {
	CampaignID   string                `json:"campaign_id"`
	Title        string                `json:"title"`
	MessageBody  string                `json:"message_body"`
	MediaURL     *string               `json:"media_url,omitempty"`
	DeliveryType string                `json:"delivery_type"`
	Channels     []string              `json:"channels"`
	Status       string                `json:"status"`
	ScheduledAt  *time.Time            `json:"scheduled_at,omitempty"`
	Audience     PublicReviewAudience  `json:"audience"`
	Brand        ReviewBrand           `json:"brand"`
	ExpiresAt    time.Time             `json:"expires_at"`
	Decision     *PublicReviewDecision `json:"decision,omitempty"`
	// RevisedSinceDecision tells the brand the content changed after their last
	// answer, so they are looking at a version they have not judged yet.
	RevisedSinceDecision bool                     `json:"revised_since_decision,omitempty"`
	ApprovalTrail        []PublicReviewTrailEntry `json:"approval_trail"`
	Report               *PublicOutcomeReport     `json:"report,omitempty"`
}

// PublicReviewTrailEntry is one step in the accountability chain a brand sees:
// who sent the campaign for approval, who cleared it internally, and what the
// brand itself decided.
type PublicReviewTrailEntry struct {
	Stage     string     `json:"stage"`      // submitted, approved, rejected, brand_approved, brand_changes_requested
	ActorRole string     `json:"actor_role"` // operator | brand
	Actor     string     `json:"actor"`
	Note      string     `json:"note,omitempty"`
	At        *time.Time `json:"at,omitempty"`
}

type PublicReviewAudience struct {
	Total     int `json:"total"`
	Sendable  int `json:"sendable"`
	OptedOut  int `json:"opted_out"`
	NoConsent int `json:"no_consent"`
}

type PublicReviewDecision struct {
	Decision  string    `json:"decision"`
	Note      string    `json:"note,omitempty"`
	DecidedAt time.Time `json:"decided_at"`
}

type PublicOutcomeReport struct {
	CampaignID   string    `json:"campaign_id"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	TotalTargets int       `json:"total_targets"`
	Sent         int       `json:"sent"`
	Delivered    int       `json:"delivered"`
	Failed       int       `json:"failed"`
	DeliveryRate float64   `json:"delivery_rate"`
	GeneratedAt  time.Time `json:"generated_at"`
}

type ReviewRepository interface {
	CreateToken(ctx context.Context, token *ReviewToken, tokenHash string) error
	ListTokens(ctx context.Context, tenantID, campaignID string) ([]*ReviewToken, error)
	RevokeToken(ctx context.Context, tenantID, campaignID, tokenID string) error
	FindTokenByHash(ctx context.Context, tokenHash string) (*ReviewToken, error)
	TouchToken(ctx context.Context, tokenID string) error
	LatestDecision(ctx context.Context, tenantID, campaignID string) (*ReviewEvent, error)
	RecordDecision(ctx context.Context, event *ReviewEvent) error
	GetBranding(ctx context.Context, tenantID string) (*ReviewBrand, error)
	// ResolveStaffNames maps workspace user IDs to a display name, falling back
	// to email when the user has not set a name.
	ResolveStaffNames(ctx context.Context, tenantID string, userIDs []string) (map[string]string, error)
}
