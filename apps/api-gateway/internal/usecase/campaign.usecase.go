package usecase

import (
	"context"
	"fmt"
	"strconv"

	"omnipulse/apps/api-gateway/internal/domain"
)

type CampaignUseCase struct {
	campaignRepo domain.CampaignRepository
	contactRepo  domain.ContactRepository
	publisher    domain.EventPublisher
}

func NewCampaignUseCase(camRepo domain.CampaignRepository, conRepo domain.ContactRepository, pub domain.EventPublisher) *CampaignUseCase {
	return &CampaignUseCase{
		campaignRepo: camRepo,
		contactRepo:  conRepo,
		publisher:    pub,
	}
}

func (u *CampaignUseCase) TriggerDispatch(ctx context.Context, campaignID string) error {
	// 1. Validate campaign existence
	campaign, err := u.campaignRepo.GetByID(ctx, campaignID)
	if err != nil {
		return err
	}

	// 2. Lock state transitions to prevent race condition double-dispatches
	if campaign.Status == "processing" || campaign.Status == "completed" {
		return fmt.Errorf("campaign execution rejected: status is already %s", campaign.Status)
	}

	// Flip database indicator status to active processing
	if err := u.campaignRepo.UpdateStatus(ctx, campaignID, "processing"); err != nil {
		return err
	}

	// 3. Kick off Memory-Safe Audience Chunking Stream Loop
	pageSize := 100
	page := 1

	for {
		limit := pageSize
		offset := (page - 1) * pageSize

		// Pull current window of targets down from database layer
		contacts, err := u.contactRepo.List(ctx, limit, offset)
		if err != nil {
			return fmt.Errorf("database reading failed mid-flight during chunk stream: %w", err)
		}

		// Break loop if we have successfully drained the audience table records
		if len(contacts) == 0 {
			break
		}

		// Iterate across the current memory window chunk
		for _, contact := range contacts {
			// Skip users who have explicitly opted out at the structural level
			if !contact.IsOptedIn {
				continue
			}

			// Generate independent event routing tasks for each active communication channel
			if contact.WhatsAppPhone != nil {
				u.emitTask(ctx, campaign, contact, "whatsapp", *contact.WhatsAppPhone)
			}
			if contact.TelegramChatID != nil {
				u.emitTask(ctx, campaign, contact, "telegram", strconv.FormatInt(*contact.TelegramChatID, 10))
			}
			if contact.XUsername != nil {
				u.emitTask(ctx, campaign, contact, "x", *contact.XUsername)
			}
		}

		// Advance pagination window forward
		page++
	}

	// 4. Finalize execution cycle tracking
	return u.campaignRepo.UpdateStatus(ctx, campaignID, "completed")
}

// Private helper to wrap domain elements and offload execution straight onto NATS JetStream
func (u *CampaignUseCase) emitTask(ctx context.Context, cmp *domain.Campaign, con *domain.Contact, platform, routeValue string) {
	task := domain.TargetDispatchTask{
		CampaignID:     cmp.ID,
		ContactID:      con.ID,
		FirstName:      con.FirstName,
		TargetPlatform: platform,
		RoutingValue:   routeValue,
		MessageBody:    cmp.MessageBody,
		MediaURL:       cmp.MediaURL,
	}

	// Fire and forget down onto the high-speed message bus stream
	_ = u.publisher.PublishDispatchTask(ctx, &task)
}

func (u *CampaignUseCase) GetStats(ctx context.Context, campaignID string) (map[string]int, error) {
	// 1. Verify the campaign exists before running aggregations
	_, err := u.campaignRepo.GetByID(ctx, campaignID)
	if err != nil {
		return nil, err // Bubbles up ErrCampaignNotFound cleanly
	}

	// 2. Fetch aggregated counting lines from the database
	return u.campaignRepo.GetCampaignStats(ctx, campaignID)
}
