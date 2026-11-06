package service

import (
	"context"
	"log"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

// RetentionSweepService hard-deletes recipients whose anonymized retention
// window has elapsed. Recipients are erased by the contact use case (PII
// blanked, opted out), then linger in anonymized form for
// domain.AnonymizedRetention so operators can still see that delivery history
// existed and so the permanent deletion is never a silent surprise.
type RetentionSweepService struct {
	contactRepo domain.ContactRepository
	interval    time.Duration
	batchSize   int
	maxBatches  int
	now         func() time.Time
	heartbeat   *HeartbeatRegistry
}

func NewRetentionSweepService(repo domain.ContactRepository) *RetentionSweepService {
	return &RetentionSweepService{
		contactRepo: repo,
		interval:    time.Hour,
		batchSize:   500,
		maxBatches:  10,
		now:         time.Now,
	}
}

func (s *RetentionSweepService) SetHeartbeat(r *HeartbeatRegistry) {
	s.heartbeat = r
}

// Start launches the sweep ticker in a background goroutine. It gracefully
// stops when ctx is cancelled.
func (s *RetentionSweepService) Start(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	log.Printf("[RETENTION] ✅ Retention sweep started (interval: %s)\n", s.interval)
	s.heartbeat.Beat("retention", 6*time.Hour)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("[RETENTION] 🛑 Retention sweep stopped.")
				return
			case <-ticker.C:
				s.heartbeat.Beat("retention", 6*time.Hour)
				s.sweep(ctx)
			}
		}
	}()
}

func (s *RetentionSweepService) sweep(ctx context.Context) {
	total := 0
	for batch := 0; batch < s.maxBatches; batch++ {
		ids, err := s.contactRepo.FindAnonymizedExpired(ctx, s.now(), s.batchSize)
		if err != nil {
			log.Printf("[RETENTION] ⚠️  Failed to list expired recipients: %v\n", err)
			return
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := s.contactRepo.HardDelete(ctx, id); err != nil {
				log.Printf("[RETENTION] ❌ Failed to hard-delete recipient id=%s: %v\n", id, err)
				continue
			}
			total++
		}
		if len(ids) < s.batchSize {
			break
		}
	}
	if total > 0 {
		log.Printf("[RETENTION] 🧹 Hard-deleted %d expired recipient(s)\n", total)
	}
}
