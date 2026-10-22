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
	campaignRepo    domain.CampaignRepository
	contactRepo     domain.ContactRepository
	destinationRepo domain.TelegramDestinationRepository
	publisher       domain.EventPublisher
	subRepo         domain.SubscriptionRepository
}

func NewCampaignUseCase(camRepo domain.CampaignRepository, conRepo domain.ContactRepository, destRepo domain.TelegramDestinationRepository, pub domain.EventPublisher, subRepo domain.SubscriptionRepository) *CampaignUseCase {
	return &CampaignUseCase{
		campaignRepo:    camRepo,
		contactRepo:     conRepo,
		destinationRepo: destRepo,
		publisher:       pub,
		subRepo:         subRepo,
	}
}

func (u *CampaignUseCase) CreateCampaign(ctx context.Context, c *domain.Campaign) error {
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

	c.Status = "draft"
	c.TotalTargets = 0
	c.ProcessedTargets = 0

	return u.campaignRepo.Create(ctx, c)
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

	role, _ := ctx.Value(ctxkeys.UserRoleKey).(string)
	reqApproval, _ := u.campaignRepo.GetApprovalSetting(ctx, tenantID)

	if reqApproval && strings.EqualFold(role, "member") && campaign.Status != "approved" {
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

	role, _ := ctx.Value(ctxkeys.UserRoleKey).(string)
	reqApproval, _ := u.campaignRepo.GetApprovalSetting(ctx, tenantID)

	if reqApproval {
		if campaign.Status == "pending_approval" && strings.EqualFold(role, "member") {
			return fmt.Errorf("campaign is pending approval from an Owner or Admin before dispatching")
		}
		if campaign.Status == "rejected" {
			reason := "campaign was rejected"
			if campaign.RejectionReason != nil && *campaign.RejectionReason != "" {
				reason = fmt.Sprintf("campaign was rejected: %s", *campaign.RejectionReason)
			}
			return errors.New(reason)
		}
		if campaign.Status == "draft" && strings.EqualFold(role, "member") {
			return fmt.Errorf("campaign approval is required in this workspace. Please submit this campaign for approval before dispatching")
		}
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

func (u *CampaignUseCase) ListDeliveries(ctx context.Context, tenantID, campaignID string, page, pageSize int) ([]*domain.CampaignDelivery, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	return u.campaignRepo.ListDeliveriesByCampaign(ctx, tenantID, campaignID, pageSize, (page-1)*pageSize)
}

func (u *CampaignUseCase) SubmitForApproval(ctx context.Context, tenantID, campaignID, submittedBy string) error {
	c, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if c.Status != "draft" && c.Status != "rejected" {
		return fmt.Errorf("only draft or rejected campaigns can be submitted for approval (current status: %s)", c.Status)
	}
	return u.campaignRepo.SubmitForApproval(ctx, tenantID, campaignID, submittedBy)
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
	deliveries, _ := u.campaignRepo.ListDeliveriesByCampaign(ctx, tenantID, campaignID, 500, 0)

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
