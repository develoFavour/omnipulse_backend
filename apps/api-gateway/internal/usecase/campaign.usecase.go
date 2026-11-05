package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/ctxkeys"
	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

type CampaignUseCase struct {
	campaignRepo     domain.CampaignRepository
	contactRepo      domain.ContactRepository
	destinationRepo  domain.TelegramDestinationRepository
	publisher        domain.EventPublisher
	subRepo          domain.SubscriptionRepository
	approvalNotifier *ApprovalNotifier
}

func NewCampaignUseCase(camRepo domain.CampaignRepository, conRepo domain.ContactRepository, destRepo domain.TelegramDestinationRepository, pub domain.EventPublisher, subRepo domain.SubscriptionRepository, notifiers ...*ApprovalNotifier) *CampaignUseCase {
	uc := &CampaignUseCase{
		campaignRepo:    camRepo,
		contactRepo:     conRepo,
		destinationRepo: destRepo,
		publisher:       pub,
		subRepo:         subRepo,
	}
	if len(notifiers) > 0 {
		uc.approvalNotifier = notifiers[0]
	}
	return uc
}

func (u *CampaignUseCase) CreateCampaign(ctx context.Context, c *domain.Campaign) error {
	if err := normalizeCampaignContent(c); err != nil {
		return err
	}

	c.Status = "draft"
	c.TotalTargets = 0
	c.ProcessedTargets = 0

	return u.campaignRepo.Create(ctx, c)
}

// UpdateCampaign replaces the content of a campaign the agency can still edit.
// Drafts and rejected campaigns are always editable; anything already approved
// is editable only when the brand has asked for changes. Saving puts the
// campaign back into draft so approvals and brand review must be earned again.
func (u *CampaignUseCase) UpdateCampaign(ctx context.Context, tenantID, id string, c *domain.Campaign) (*domain.Campaign, error) {
	existing, err := u.campaignRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	if !campaignEditable(existing) {
		return nil, fmt.Errorf("this campaign can no longer be edited (current status: %s)", existing.Status)
	}

	if err := normalizeCampaignContent(c); err != nil {
		return nil, err
	}

	if err := u.campaignRepo.UpdateContent(ctx, tenantID, id, c, existing.Status != "draft"); err != nil {
		return nil, err
	}
	return u.campaignRepo.GetByID(ctx, tenantID, id)
}

func campaignEditable(c *domain.Campaign) bool {
	switch c.Status {
	case "draft", "rejected":
		return true
	case "pending_approval", "approved":
		return c.LatestReviewDecision == domain.ReviewDecisionChangesRequested
	default:
		return false
	}
}

func normalizeCampaignContent(c *domain.Campaign) error {
	c.Title = strings.TrimSpace(c.Title)
	if c.Title == "" {
		return fmt.Errorf("campaign title cannot be empty")
	}

	c.MessageBody = strings.TrimSpace(c.MessageBody)
	if c.MessageBody == "" {
		return fmt.Errorf("message body cannot be empty")
	}

	if c.SelectedChannels == "" {
		c.SelectedChannels = "[]"
	}
	if c.SelectedTelegramDestinationIDs == "" {
		c.SelectedTelegramDestinationIDs = "[]"
	}
	if c.SelectedContactIDs == "" {
		c.SelectedContactIDs = "[]"
	}
	if c.DeliveryType == "" {
		c.DeliveryType = "direct_message"
	}
	return nil
}

// ScheduleCampaign persists a campaign with status="scheduled" and the given scheduled_at time.
func (u *CampaignUseCase) ScheduleCampaign(ctx context.Context, tenantID, campaignID string, scheduledAt time.Time) error {
	campaign, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if scheduledAt.Before(time.Now().Add(1 * time.Minute)) {
		return fmt.Errorf("scheduled time must be at least 1 minute in the future")
	}

	if campaign.BrandChangesOutstanding() {
		return fmt.Errorf("the brand requested changes on this campaign. Address them before scheduling")
	}
	if campaign.BrandReviewStale() {
		return fmt.Errorf("the brand has not reviewed the latest version of this campaign. Ask them to review it again before scheduling")
	}

	reqApproval, _ := u.campaignRepo.GetApprovalSetting(ctx, tenantID)

	if reqApproval && campaign.Status != "approved" {
		if campaign.Status == "pending_approval" {
			return fmt.Errorf("campaign is pending approval from an Owner or Admin before it can be scheduled")
		}
		return fmt.Errorf("campaign approval is required before scheduling. Please submit this campaign for approval")
	}

	if campaign.Status != "draft" && campaign.Status != "approved" && campaign.Status != "pending_approval" {
		return fmt.Errorf("only draft, approved, or pending approval campaigns can be scheduled (current status: %s)", campaign.Status)
	}

	return u.campaignRepo.SetScheduled(ctx, tenantID, campaignID, scheduledAt)
}

// CancelScheduledCampaign reverts a scheduled campaign back to draft.
func (u *CampaignUseCase) CancelScheduledCampaign(ctx context.Context, tenantID, campaignID string) error {
	return u.campaignRepo.CancelScheduledCampaign(ctx, tenantID, campaignID)
}

func (u *CampaignUseCase) PauseCampaign(ctx context.Context, tenantID, campaignID string) error {
	campaign, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if campaign.Status != "processing" {
		return fmt.Errorf("only an active campaign can be paused (current status: %s)", campaign.Status)
	}
	return u.campaignRepo.PauseCampaign(ctx, tenantID, campaignID)
}

func (u *CampaignUseCase) ResumeCampaign(ctx context.Context, tenantID, campaignID string) error {
	campaign, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if campaign.Status != "paused" {
		return fmt.Errorf("only a paused campaign can be resumed (current status: %s)", campaign.Status)
	}
	return u.campaignRepo.ResumeCampaign(ctx, tenantID, campaignID)
}

func (u *CampaignUseCase) CancelCampaign(ctx context.Context, tenantID, campaignID string) error {
	campaign, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	switch campaign.Status {
	case "draft", "scheduled", "pending_approval", "approved":
		return u.campaignRepo.CancelCampaign(ctx, tenantID, campaignID)
	default:
		return fmt.Errorf("only a pending campaign can be cancelled (current status: %s)", campaign.Status)
	}
}

func (u *CampaignUseCase) DeleteDraft(ctx context.Context, tenantID, campaignID string) error {
	campaign, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if campaign.Status != "draft" {
		return fmt.Errorf("only a draft campaign can be deleted (current status: %s)", campaign.Status)
	}
	return u.campaignRepo.DeleteDraft(ctx, tenantID, campaignID)
}

// TriggerScheduledDispatch is called by SchedulerService for campaigns whose scheduled_at has elapsed.
// It re-uses the same TriggerDispatch path so the full fan-out logic is shared.
func (u *CampaignUseCase) TriggerScheduledDispatch(ctx context.Context, campaign *domain.Campaign) error {
	log.Printf("[SCHEDULER] 🕐 Firing scheduled campaign: id=%s tenant=%s title=%q\n", campaign.ID, campaign.TenantID, campaign.Title)
	return u.TriggerDispatch(ctx, campaign.TenantID, campaign.ID)
}

func (u *CampaignUseCase) ListCampaigns(ctx context.Context, tenantID, status string, page, pageSize int) ([]*domain.Campaign, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return u.campaignRepo.ListByTenant(ctx, tenantID, status, pageSize, (page-1)*pageSize)
}

func (u *CampaignUseCase) TriggerDispatch(ctx context.Context, tenantID, campaignID string) error {
	log.Printf("[DISPATCH-TRACE] === TriggerDispatch START === tenant=%s campaign=%s\n", tenantID, campaignID)

	campaign, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		log.Printf("[DISPATCH-TRACE] ❌ Campaign lookup failed: %v\n", err)
		return err
	}
	log.Printf("[DISPATCH-TRACE] Campaign found: title=%q status=%s channels=%s destinations=%s contacts=%s\n",
		campaign.Title, campaign.Status, campaign.SelectedChannels, campaign.SelectedTelegramDestinationIDs, campaign.SelectedContactIDs)

	if campaign.Status == "processing" || campaign.Status == "completed" || campaign.Status == "paused" || campaign.Status == "cancelled" {
		log.Printf("[DISPATCH-TRACE] ❌ Rejected: status already %s\n", campaign.Status)
		return fmt.Errorf("campaign execution rejected: status is already %s", campaign.Status)
	}

	if campaign.Status == "rejected" {
		reason := "campaign was rejected"
		if campaign.RejectionReason != nil && *campaign.RejectionReason != "" {
			reason = fmt.Sprintf("campaign was rejected: %s", *campaign.RejectionReason)
		}
		return errors.New(reason)
	}

	if campaign.BrandChangesOutstanding() {
		return errors.New("the brand requested changes on this campaign. Address them before dispatching")
	}
	if campaign.BrandReviewStale() {
		return errors.New("the brand has not reviewed the latest version of this campaign. Ask them to review it again before dispatching")
	}

	reqApproval, _ := u.campaignRepo.GetApprovalSetting(ctx, tenantID)
	if reqApproval && campaign.Status != "approved" && campaign.Status != "scheduled" {
		if campaign.Status == "pending_approval" {
			return fmt.Errorf("campaign is pending approval from an Owner or Admin before dispatching")
		}
		return fmt.Errorf("campaign approval is required in this workspace. Please submit this campaign for approval before dispatching")
	}

	// Enforce Plan Monthly Message Quota
	if u.subRepo != nil {
		sub, err := u.subRepo.GetByTenantID(ctx, tenantID)
		if err == nil && sub != nil && sub.MonthlyMessageLimit != -1 {
			since := sub.CurrentPeriodStart
			if since.IsZero() {
				now := time.Now()
				since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			}
			sentCount, err := u.campaignRepo.CountDeliveriesSince(ctx, tenantID, since)
			if err == nil && sentCount >= sub.MonthlyMessageLimit {
				log.Printf("[DISPATCH-TRACE] ❌ Rejected: monthly limit %d reached (sent: %d)\n", sub.MonthlyMessageLimit, sentCount)
				return fmt.Errorf("%w: monthly broadcast limit of %d messages reached for %s tier (dispatched: %d). Upgrade to Growth or Agency to dispatch.", domain.ErrQuotaExceeded, sub.MonthlyMessageLimit, sub.PlanTier, sentCount)
			}
		}
	}

	selectedChannels := parseStringList(campaign.SelectedChannels)
	selectedDestinations := parseStringList(campaign.SelectedTelegramDestinationIDs)
	selectedContacts := parseStringList(campaign.SelectedContactIDs)
	log.Printf("[DISPATCH-TRACE] Parsed channels=%v destinations=%v contacts=%v\n", selectedChannels, selectedDestinations, selectedContacts)

	publishedTargets := 0

	// 1. Dispatch to contacts
	if len(selectedContacts) > 0 {
		// Target ONLY explicitly selected contacts
		selectedMap := make(map[string]bool, len(selectedContacts))
		for _, id := range selectedContacts {
			selectedMap[id] = true
		}

		pageSize := 100
		page := 1
		for {
			contacts, err := u.contactRepo.ListByTenant(ctx, tenantID, "", pageSize, (page-1)*pageSize)
			if err != nil {
				log.Printf("[DISPATCH-TRACE] ❌ Contact query failed on page %d: %v\n", page, err)
				return fmt.Errorf("database reading failed mid-flight during chunk stream: %w", err)
			}
			if len(contacts) == 0 {
				break
			}
			for _, contact := range contacts {
				if !selectedMap[contact.ID] {
					continue
				}
				if contact.Status != "active" {
					log.Printf("[DISPATCH-TRACE] Skipping selected contact %s: inactive status\n", contact.ID)
					continue
				}
				if len(selectedChannels) > 0 && !containsString(selectedChannels, contact.Channel) {
					continue
				}
				log.Printf("[DISPATCH-TRACE] Emitting task for selected contact %s (%s) on %s\n",
					contact.ID, contact.RoutingValue, contact.Channel)
				u.emitContactTask(ctx, campaign, contact)
				publishedTargets++
			}
			if len(contacts) < pageSize {
				break
			}
			page++
		}
	} else if len(selectedChannels) > 0 {
		// Default: broadcast to all active contacts in selected channels
		pageSize := 100
		page := 1
		for {
			contacts, err := u.contactRepo.ListByTenant(ctx, tenantID, "", pageSize, (page-1)*pageSize)
			if err != nil {
				log.Printf("[DISPATCH-TRACE] ❌ Contact query failed on page %d: %v\n", page, err)
				return fmt.Errorf("database reading failed mid-flight during chunk stream: %w", err)
			}
			log.Printf("[DISPATCH-TRACE] Contact page %d: fetched %d contacts\n", page, len(contacts))
			if len(contacts) == 0 {
				break
			}
			for _, contact := range contacts {
				if contact.Status != "active" || !containsString(selectedChannels, contact.Channel) {
					log.Printf("[DISPATCH-TRACE] Skipping contact %s: status=%s channel=%s (not in %v)\n",
						contact.ID, contact.Status, contact.Channel, selectedChannels)
					continue
				}
				log.Printf("[DISPATCH-TRACE] Emitting task for contact %s (%s) on %s\n",
					contact.ID, contact.RoutingValue, contact.Channel)
				u.emitContactTask(ctx, campaign, contact)
				publishedTargets++
			}
			if len(contacts) < pageSize {
				break
			}
			page++
		}
	}

	if len(selectedDestinations) > 0 {
		destinations, err := u.destinationRepo.ListByIDs(ctx, tenantID, selectedDestinations)
		if err != nil {
			log.Printf("[DISPATCH-TRACE] ❌ Telegram destination lookup failed: %v\n", err)
			return fmt.Errorf("telegram destination lookup failed: %w", err)
		}
		log.Printf("[DISPATCH-TRACE] Found %d telegram destinations\n", len(destinations))
		for _, destination := range destinations {
			log.Printf("[DISPATCH-TRACE] Emitting task for destination %s (%s)\n", destination.ID, destination.Title)
			u.emitDestinationTask(ctx, campaign, &destination)
			publishedTargets++
		}
	}

	log.Printf("[DISPATCH-TRACE] === TriggerDispatch END === total published targets: %d\n", publishedTargets)

	if publishedTargets == 0 {
		log.Printf("[DISPATCH-TRACE] ❌ No targets matched! Campaign will NOT dispatch.\n")
		return fmt.Errorf("campaign execution rejected: no active targets matched this campaign")
	}

	// Mark campaign as 'processing' and record total_targets.
	// Status will only transition to 'completed' once all delivery receipts arrive via telemetryWorker.
	if err := u.campaignRepo.SetDispatching(ctx, tenantID, campaignID, publishedTargets); err != nil {
		log.Printf("[DISPATCH-TRACE] ❌ Failed to set campaign to processing: %v\n", err)
		return err
	}
	log.Printf("[DISPATCH-TRACE] ✅ Campaign %s set to processing with %d total targets\n", campaignID, publishedTargets)
	return nil
}

func (u *CampaignUseCase) emitContactTask(ctx context.Context, cmp *domain.Campaign, con *domain.Contact) {
	task := &contracts.TargetDispatchTask{
		CampaignID:     cmp.ID,
		TenantID:       cmp.TenantID,
		ContactID:      con.ID,
		TargetType:     "contact",
		FirstName:      con.FirstName,
		TargetPlatform: con.Channel,
		RoutingValue:   con.RoutingValue,
		MessageBody:    cmp.MessageBody,
		MediaURL:       cmp.MediaURL,
		ExpiresAt:      time.Now().Add(2 * time.Hour).Unix(),
	}
	if err := u.publisher.PublishDispatchTask(ctx, task); err != nil {
		log.Printf("[USECASE-ERROR] Failed to emit dispatch task for contact %s: %v\n", con.ID, err)
	}
}

func (u *CampaignUseCase) emitDestinationTask(ctx context.Context, cmp *domain.Campaign, dest *domain.TelegramDestination) {
	task := &contracts.TargetDispatchTask{
		CampaignID:     cmp.ID,
		TenantID:       cmp.TenantID,
		ContactID:      "",
		TargetType:     "telegram_destination",
		FirstName:      dest.Title,
		TargetPlatform: "telegram",
		RoutingValue:   dest.TelegramChatID,
		MessageBody:    cmp.MessageBody,
		MediaURL:       cmp.MediaURL,
		ExpiresAt:      time.Now().Add(2 * time.Hour).Unix(),
	}
	if err := u.publisher.PublishDispatchTask(ctx, task); err != nil {
		log.Printf("[USECASE-ERROR] Failed to emit telegram destination task for %s: %v\n", dest.ID, err)
	}
}

func (u *CampaignUseCase) GetCampaign(ctx context.Context, tenantID, campaignID string) (*domain.Campaign, error) {
	return u.campaignRepo.GetByID(ctx, tenantID, campaignID)
}

func (u *CampaignUseCase) GetStats(ctx context.Context, tenantID, campaignID string) (*domain.CampaignStats, error) {
	return u.campaignRepo.GetCampaignStats(ctx, tenantID, campaignID)
}

func (u *CampaignUseCase) ListDeliveries(ctx context.Context, tenantID, campaignID string, status, failureCode string, retryable *bool, page, pageSize int) ([]*domain.CampaignDelivery, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	return u.campaignRepo.ListDeliveriesByCampaign(ctx, tenantID, campaignID, status, failureCode, retryable, pageSize, (page-1)*pageSize)
}

func (u *CampaignUseCase) GetHealth(ctx context.Context, tenantID, campaignID string) (*domain.CampaignHealth, error) {
	return u.campaignRepo.GetCampaignHealth(ctx, tenantID, campaignID)
}

// Preflight reports what a dispatch would actually do without publishing
// anything. Its audience filtering mirrors TriggerDispatch so the numbers a
// client approves are the numbers that get sent.
func (u *CampaignUseCase) Preflight(ctx context.Context, tenantID, campaignID string) (*domain.CampaignPreflight, error) {
	cmp, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return nil, err
	}

	selectedChannels := parseStringList(cmp.SelectedChannels)
	selectedContacts := parseStringList(cmp.SelectedContactIDs)
	selectedDestinations := parseStringList(cmp.SelectedTelegramDestinationIDs)

	preflight := &domain.CampaignPreflight{
		CampaignID: cmp.ID,
		Status:     cmp.Status,
		Reasons:    []domain.PreflightReason{},
	}

	if len(selectedContacts) > 0 || len(selectedChannels) > 0 {
		counts, err := u.campaignRepo.CountAudienceConsent(ctx, tenantID, selectedContacts, selectedChannels)
		if err != nil {
			return nil, err
		}
		preflight.TotalTargets += counts.Total
		preflight.Sendable += counts.Active
		preflight.OptedOut += counts.OptedOut
		preflight.NoConsent += counts.NoConsent

		if counts.OptedOut > 0 {
			preflight.Reasons = append(preflight.Reasons, domain.PreflightReason{
				Code:    "opted_out",
				Count:   counts.OptedOut,
				Message: fmt.Sprintf("%d contact(s) opted out and will be skipped", counts.OptedOut),
			})
		}
		if counts.NoConsent > 0 {
			preflight.Reasons = append(preflight.Reasons, domain.PreflightReason{
				Code:    "no_consent",
				Count:   counts.NoConsent,
				Message: fmt.Sprintf("%d contact(s) have no recorded consent evidence; they will still be messaged", counts.NoConsent),
			})
		}
	}

	if len(selectedDestinations) > 0 {
		destinations, err := u.destinationRepo.ListByIDs(ctx, tenantID, selectedDestinations)
		if err != nil {
			return nil, fmt.Errorf("telegram destination lookup failed: %w", err)
		}
		preflight.TotalTargets += len(destinations)
		preflight.Sendable += len(destinations)
	}

	// Quiet hours and non-consent suppression are not modelled yet, so these
	// stay at zero rather than inventing blocked targets.
	preflight.QuietHours = 0
	preflight.Blocked = 0

	if preflight.Sendable == 0 {
		preflight.Reasons = append(preflight.Reasons, domain.PreflightReason{
			Code:    "no_sendable_targets",
			Count:   0,
			Message: "No sendable targets match this campaign; dispatch would be rejected",
		})
	}

	return preflight, nil
}

// StreamConsentExport writes an audit row per audience contact to the caller's
// writer so large audiences are never buffered in memory.
func (u *CampaignUseCase) StreamConsentExport(ctx context.Context, tenantID, campaignID string, yield func(*domain.AudienceConsentRow) error) error {
	cmp, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	selectedChannels := parseStringList(cmp.SelectedChannels)
	selectedContacts := parseStringList(cmp.SelectedContactIDs)
	if len(selectedContacts) == 0 && len(selectedChannels) == 0 {
		return nil
	}
	return u.campaignRepo.StreamAudienceConsent(ctx, tenantID, selectedContacts, selectedChannels, yield)
}

func (u *CampaignUseCase) RetryDeliveries(ctx context.Context, tenantID, campaignID string, deliveryIDs []string) (*domain.RetryDeliveriesResponse, error) {
	cmp, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	if cmp.Status != "processing" && cmp.Status != "completed" {
		return nil, fmt.Errorf("deliveries can only be retried for processing or completed campaigns (current status: %s)", cmp.Status)
	}

	claimed, err := u.campaignRepo.ClaimDeliveriesForRetry(ctx, tenantID, campaignID, deliveryIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to claim deliveries for retry: %w", err)
	}

	// Reopen the campaign before publishing: the worker drops tasks for campaigns
	// that are still marked completed.
	reopened := false
	if len(claimed) > 0 && cmp.Status == "completed" {
		if err := u.campaignRepo.UpdateStatus(ctx, tenantID, campaignID, "processing"); err != nil {
			log.Printf("[RETRY-DELIVERY-WARN] Failed to reopen campaign %s: %v\n", campaignID, err)
		} else {
			reopened = true
		}
	}

	published := 0
	var failedClaims []string
	for _, d := range claimed {
		contactID := ""
		if d.ContactID != nil {
			contactID = *d.ContactID
		}
		task := &contracts.TargetDispatchTask{
			CampaignID:     cmp.ID,
			TenantID:       cmp.TenantID,
			ContactID:      contactID,
			TargetType:     d.TargetType,
			FirstName:      d.FirstName,
			TargetPlatform: d.Platform,
			RoutingValue:   d.RoutingValue,
			MessageBody:    cmp.MessageBody,
			MediaURL:       cmp.MediaURL,
			ExpiresAt:      time.Now().Add(2 * time.Hour).Unix(),
		}
		if pubErr := u.publisher.PublishDispatchTask(ctx, task); pubErr != nil {
			log.Printf("[RETRY-DELIVERY-WARN] Failed to re-dispatch task for delivery %s: %v\n", d.ID, pubErr)
			failedClaims = append(failedClaims, d.ID)
			continue
		}
		published++
	}

	if len(failedClaims) > 0 {
		if relErr := u.campaignRepo.ReleaseDeliveryClaims(ctx, campaignID, failedClaims); relErr != nil {
			log.Printf("[RETRY-DELIVERY-ERROR] Failed to release %d un-published claims: %v\n", len(failedClaims), relErr)
		}
	}

	if published == 0 && reopened {
		if err := u.campaignRepo.UpdateStatus(ctx, tenantID, campaignID, "completed"); err != nil {
			log.Printf("[RETRY-DELIVERY-WARN] Nothing published; failed to restore campaign %s to completed: %v\n", campaignID, err)
		}
	}

	return &domain.RetryDeliveriesResponse{
		Message:    fmt.Sprintf("Successfully re-enqueued %d failed messages for delivery", published),
		Retried:    published,
		TotalTasks: len(claimed),
	}, nil
}

func (u *CampaignUseCase) SubmitForApproval(ctx context.Context, tenantID, campaignID, submittedBy string) error {
	c, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if c.Status != "draft" && c.Status != "rejected" {
		return fmt.Errorf("only draft or rejected campaigns can be submitted for approval (current status: %s)", c.Status)
	}
	if err := u.campaignRepo.SubmitForApproval(ctx, tenantID, campaignID, submittedBy); err != nil {
		return err
	}
	if u.approvalNotifier != nil {
		u.approvalNotifier.NotifySubmitted(ctx, tenantID, campaignID, c.Title, submittedBy)
	}
	return nil
}

func (u *CampaignUseCase) ApproveCampaign(ctx context.Context, tenantID, campaignID, approvedBy string) error {
	role, _ := ctx.Value(ctxkeys.UserRoleKey).(string)
	if !strings.EqualFold(role, "owner") && !strings.EqualFold(role, "admin") {
		return fmt.Errorf("only workspace owners or admins can approve campaigns")
	}
	c, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if c.Status != "pending_approval" {
		return fmt.Errorf("only campaigns pending approval can be approved (current status: %s)", c.Status)
	}
	// A change request on content the operator has since revised must not block
	// internal approval: the new version is what needs approving.
	if c.BrandChangesOutstanding() {
		return fmt.Errorf("the brand requested changes on this campaign. Address them before approving")
	}
	return u.campaignRepo.ApproveCampaign(ctx, tenantID, campaignID, approvedBy)
}

func (u *CampaignUseCase) RejectCampaign(ctx context.Context, tenantID, campaignID, reason string) error {
	role, _ := ctx.Value(ctxkeys.UserRoleKey).(string)
	if !strings.EqualFold(role, "owner") && !strings.EqualFold(role, "admin") {
		return fmt.Errorf("only workspace owners or admins can reject campaigns")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("a rejection reason must be provided")
	}
	c, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if c.Status != "pending_approval" {
		return fmt.Errorf("only campaigns pending approval can be rejected (current status: %s)", c.Status)
	}
	return u.campaignRepo.RejectCampaign(ctx, tenantID, campaignID, reason)
}

func (u *CampaignUseCase) GetCampaignReport(ctx context.Context, tenantID, campaignID string) (*domain.CampaignReport, error) {
	campaign, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	stats, err := u.campaignRepo.GetCampaignStats(ctx, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	deliveries, _ := u.campaignRepo.ListDeliveriesByCampaign(ctx, tenantID, campaignID, "", "", nil, 500, 0)

	deliveryRate := 0.0
	if stats.TotalTargets > 0 {
		deliveryRate = (float64(stats.Delivered) / float64(stats.TotalTargets)) * 100.0
	}
	readRate := 0.0
	readCount := 0
	for _, d := range deliveries {
		if d.Status == "read" {
			readCount++
		}
	}
	if stats.Delivered > 0 {
		readRate = (float64(readCount) / float64(stats.Delivered)) * 100.0
	}

	return &domain.CampaignReport{
		Campaign:     campaign,
		Stats:        stats,
		Deliveries:   deliveries,
		ReadRate:     math.Round(readRate*10) / 10,
		DeliveryRate: math.Round(deliveryRate*10) / 10,
	}, nil
}

func parseStringList(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{}
	}
	return values
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
