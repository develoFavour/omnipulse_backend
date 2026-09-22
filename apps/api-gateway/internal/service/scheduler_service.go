package service

import (
	"context"
	"log"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

// SchedulerService polls the database every 15 seconds for scheduled campaigns
// whose scheduled_at timestamp has elapsed and triggers their dispatch.
type SchedulerService struct {
	campaignRepo domain.CampaignRepository
	dispatcher   CampaignDispatcher
	interval     time.Duration
}

// CampaignDispatcher is the interface the scheduler calls to fire off a campaign.
// This is implemented by CampaignUseCase.TriggerScheduledDispatch.
type CampaignDispatcher interface {
	TriggerScheduledDispatch(ctx context.Context, campaign *domain.Campaign) error
}

func NewSchedulerService(repo domain.CampaignRepository, dispatcher CampaignDispatcher) *SchedulerService {
	return &SchedulerService{
		campaignRepo: repo,
		dispatcher:   dispatcher,
		interval:     15 * time.Second,
	}
}

// Start launches the polling ticker in a background goroutine.
// It gracefully stops when ctx is cancelled.
func (s *SchedulerService) Start(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	log.Println("[SCHEDULER] ✅ Campaign Scheduler Service started (interval: 15s)")

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("[SCHEDULER] 🛑 Campaign Scheduler Service stopped.")
				return
			case <-ticker.C:
				s.processDueCampaigns(ctx)
			}
		}
	}()
}

func (s *SchedulerService) processDueCampaigns(ctx context.Context) {
	campaigns, err := s.campaignRepo.FindDueScheduledCampaigns(ctx)
	if err != nil {
		log.Printf("[SCHEDULER] ⚠️  Failed to query due campaigns: %v\n", err)
		return
	}

	if len(campaigns) == 0 {
		return
	}

	log.Printf("[SCHEDULER] 🔍 Found %d due scheduled campaign(s) — dispatching.\n", len(campaigns))

	for _, c := range campaigns {
		campaign := c // capture loop variable
		go func() {
			dispatchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			log.Printf("[SCHEDULER] ▶️  Dispatching scheduled campaign: id=%s title=%q\n", campaign.ID, campaign.Title)
			if err := s.dispatcher.TriggerScheduledDispatch(dispatchCtx, campaign); err != nil {
				log.Printf("[SCHEDULER] ❌ Failed to dispatch campaign id=%s: %v\n", campaign.ID, err)
			} else {
				log.Printf("[SCHEDULER] ✅ Scheduled campaign dispatched: id=%s\n", campaign.ID)
			}
		}()
	}
}
