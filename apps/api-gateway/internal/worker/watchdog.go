package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

// CampaignWatchdog periodically scans for campaigns stuck in 'processing'
// with no delivery activity, and fires an in-app system alert notification
// so operators know the broadcast worker is down before users complain.
type CampaignWatchdog struct {
	db           *sql.DB
	notifRepo    domain.NotificationRepository
	pollInterval time.Duration
	stuckAfter   time.Duration
}

// NewCampaignWatchdog creates a watchdog with sensible defaults:
// polls every 5 minutes, flags campaigns stuck longer than 15 minutes.
func NewCampaignWatchdog(db *sql.DB, notifRepo domain.NotificationRepository) *CampaignWatchdog {
	return &CampaignWatchdog{
		db:           db,
		notifRepo:    notifRepo,
		pollInterval: 5 * time.Minute,
		stuckAfter:   15 * time.Minute,
	}
}

// Start launches the watchdog loop in the background.
func (w *CampaignWatchdog) Start(ctx context.Context) {
	go w.run(ctx)
	log.Println("[WATCHDOG] Campaign delivery watchdog active — polling every 5m for stuck campaigns")
}

func (w *CampaignWatchdog) run(ctx context.Context) {
	// Run an initial scan shortly after boot so we catch any pre-existing stuck campaigns
	initialTimer := time.NewTimer(30 * time.Second)
	select {
	case <-ctx.Done():
		initialTimer.Stop()
		return
	case <-initialTimer.C:
		w.scan(ctx)
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[WATCHDOG] Campaign delivery watchdog shutting down.")
			return
		case <-ticker.C:
			w.scan(ctx)
		}
	}
}

func (w *CampaignWatchdog) scan(ctx context.Context) {
	// Find campaigns stuck in 'processing' beyond the threshold
	// with no confirmed deliveries (worker never picked them up).
	rows, err := w.db.QueryContext(ctx, `
		SELECT c.id, c.tenant_id, c.title, c.total_targets, c.updated_at
		FROM campaigns c
		WHERE c.status = 'processing'
		  AND c.updated_at < NOW() - $1::interval
		  AND NOT EXISTS (
			SELECT 1 FROM campaign_deliveries cd
			WHERE cd.campaign_id = c.id AND cd.status = 'delivered'
		  )
		ORDER BY c.updated_at ASC
		LIMIT 10
	`, fmt.Sprintf("%.0f seconds", w.stuckAfter.Seconds()))
	if err != nil {
		log.Printf("[WATCHDOG] Error scanning for stuck campaigns: %v\n", err)
		return
	}
	defer rows.Close()

	type stuckCampaign struct {
		id        string
		tenantID  string
		title     string
		total     int
		updatedAt time.Time
	}

	var stuck []stuckCampaign
	for rows.Next() {
		var sc stuckCampaign
		if scanErr := rows.Scan(&sc.id, &sc.tenantID, &sc.title, &sc.total, &sc.updatedAt); scanErr == nil {
			stuck = append(stuck, sc)
		}
	}

	if len(stuck) == 0 {
		return
	}

	log.Printf("[WATCHDOG] ⚠️  Detected %d stuck campaign(s) — broadcast worker may be down!\n", len(stuck))

	// Fire one in-app alert per tenant (avoid duplicate flooding).
	seen := map[string]bool{}
	for _, sc := range stuck {
		if seen[sc.tenantID] {
			continue
		}
		seen[sc.tenantID] = true

		stuckAge := time.Since(sc.updatedAt).Truncate(time.Minute)

		meta, _ := json.Marshal(map[string]interface{}{
			"campaign_id":    sc.id,
			"campaign_title": sc.title,
			"stuck_age_min":  int(stuckAge.Minutes()),
		})

		notif := &domain.Notification{
			TenantID: sc.tenantID,
			Type:     domain.NotifSystemAlert,
			Title:    "⚠️ Broadcast Delivery Stalled",
			Body: fmt.Sprintf(
				"Campaign \"%s\" has been processing for %s with no deliveries confirmed. "+
					"The broadcast worker may be down — please check your deployment on Render.",
				sc.title, stuckAge,
			),
			Metadata: json.RawMessage(meta),
		}

		if err := w.notifRepo.Create(ctx, notif); err != nil {
			log.Printf("[WATCHDOG] Failed to emit stuck-campaign alert for tenant %s: %v\n", sc.tenantID, err)
		} else {
			log.Printf("[WATCHDOG] 🔔 Alert fired for tenant %s — campaign %q stuck for %s\n",
				sc.tenantID, sc.title, stuckAge)
		}
	}
}
