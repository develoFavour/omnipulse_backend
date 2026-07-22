package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

type CampaignUseCase struct {
	campaignRepo    domain.CampaignRepository
	contactRepo     domain.ContactRepository
	destinationRepo domain.TelegramDestinationRepository
	publisher       domain.EventPublisher
}

func NewCampaignUseCase(camRepo domain.CampaignRepository, conRepo domain.ContactRepository, destRepo domain.TelegramDestinationRepository, pub domain.EventPublisher) *CampaignUseCase {
	return &CampaignUseCase{
		campaignRepo:    camRepo,
		contactRepo:     conRepo,
		destinationRepo: destRepo,
		publisher:       pub,
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
	if c.DeliveryType == "" {
		c.DeliveryType = "direct_message"
	}

	c.Status = "draft"
	c.TotalTargets = 0
	c.ProcessedTargets = 0

	return u.campaignRepo.Create(ctx, c)
}

func (u *CampaignUseCase) ListCampaigns(ctx context.Context, tenantID string, page, pageSize int) ([]*domain.Campaign, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return u.campaignRepo.ListByTenant(ctx, tenantID, pageSize, (page-1)*pageSize)
}

func (u *CampaignUseCase) TriggerDispatch(ctx context.Context, tenantID, campaignID string) error {
	campaign, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return err
	}
	if campaign.Status == "processing" || campaign.Status == "completed" {
		return fmt.Errorf("campaign execution rejected: status is already %s", campaign.Status)
	}
	if err := u.campaignRepo.UpdateStatus(ctx, tenantID, campaignID, "processing"); err != nil {
		return err
	}

	selectedChannels := parseStringList(campaign.SelectedChannels)
	selectedDestinations := parseStringList(campaign.SelectedTelegramDestinationIDs)
	publishedTargets := 0

	if len(selectedChannels) > 0 {
		pageSize := 100
		page := 1
		for {
			contacts, err := u.contactRepo.ListByTenant(ctx, tenantID, "", pageSize, (page-1)*pageSize)
			if err != nil {
				return fmt.Errorf("database reading failed mid-flight during chunk stream: %w", err)
			}
			if len(contacts) == 0 {
				break
			}
			for _, contact := range contacts {
				if contact.Status != "active" || !containsString(selectedChannels, contact.Channel) {
					continue
				}
				u.emitContactTask(ctx, campaign, contact)
				publishedTargets++
			}
			page++
		}
	}

	if len(selectedDestinations) > 0 {
		destinations, err := u.destinationRepo.ListByIDs(ctx, tenantID, selectedDestinations)
		if err != nil {
			return fmt.Errorf("telegram destination lookup failed: %w", err)
		}
		for _, destination := range destinations {
			u.emitDestinationTask(ctx, campaign, &destination)
			publishedTargets++
		}
	}

	if publishedTargets == 0 {
		return fmt.Errorf("campaign execution rejected: no active targets matched this campaign")
	}
	return u.campaignRepo.UpdateStatus(ctx, tenantID, campaignID, "completed")
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
	}
	if err := u.publisher.PublishDispatchTask(ctx, task); err != nil {
		log.Printf("[USECASE-ERROR] Failed to emit telegram destination task for %s: %v\n", dest.ID, err)
	}
}

func (u *CampaignUseCase) GetStats(ctx context.Context, tenantID, campaignID string) (map[string]int, error) {
	_, err := u.campaignRepo.GetByID(ctx, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	return u.campaignRepo.GetCampaignStats(ctx, tenantID, campaignID)
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
