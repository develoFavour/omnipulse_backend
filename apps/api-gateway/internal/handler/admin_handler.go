package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/repository"
	"omnipulse/apps/api-gateway/internal/service"
	"omnipulse/apps/api-gateway/internal/utils"
)

// AdminHandler serves the read-only platform dashboard. Reaching these methods
// means PlatformAdminGate already proved the caller is on the allowlist.
type AdminHandler struct {
	repo       *repository.PostgresAdminRepository
	heartbeats *service.HeartbeatRegistry
	natsJS     nats.JetStreamContext
}

func NewAdminHandler(repo *repository.PostgresAdminRepository, heartbeats *service.HeartbeatRegistry) *AdminHandler {
	return &AdminHandler{repo: repo, heartbeats: heartbeats}
}

// SetJetStream lets the health page report consumer depth. A nil context simply
// omits consumer data rather than failing the request.
func (h *AdminHandler) SetJetStream(js nats.JetStreamContext) {
	h.natsJS = js
}

// Matches the campaign watchdog so the dashboard and the alerting agree on
// what "stuck" means.
const adminStuckCampaignAfter = 15 * time.Minute

func adminWindowDays(r *http.Request, fallback, max int) int {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days <= 0 {
		return fallback
	}
	if days > max {
		return max
	}
	return days
}

func (h *AdminHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := h.repo.GetOverview(r.Context(), adminWindowDays(r, 30, 365))
	if err != nil {
		log.Printf("[ADMIN] overview aggregation failed: %v\n", err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load platform overview")
		return
	}
	utils.WriteJSON(w, http.StatusOK, overview)
}

func (h *AdminHandler) GetGrowth(w http.ResponseWriter, r *http.Request) {
	growth, err := h.repo.GetGrowth(r.Context(), adminWindowDays(r, 30, 180))
	if err != nil {
		log.Printf("[ADMIN] growth aggregation failed: %v\n", err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load platform growth")
		return
	}
	utils.WriteJSON(w, http.StatusOK, growth)
}

func (h *AdminHandler) GetRevenue(w http.ResponseWriter, r *http.Request) {
	revenue, err := h.repo.GetRevenue(r.Context(), adminWindowDays(r, 30, 365))
	if err != nil {
		log.Printf("[ADMIN] revenue aggregation failed: %v\n", err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load platform revenue")
		return
	}
	utils.WriteJSON(w, http.StatusOK, revenue)
}

func (h *AdminHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	health, err := h.repo.GetHealth(r.Context(), adminStuckCampaignAfter)
	if err != nil {
		log.Printf("[ADMIN] health aggregation failed: %v\n", err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load platform health")
		return
	}

	degraded := false
	for _, beat := range h.heartbeats.Snapshot() {
		if !beat.Healthy {
			degraded = true
		}
		health.Heartbeats = append(health.Heartbeats, domain.AdminHeartbeat{
			Service:           beat.Service,
			Healthy:           beat.Healthy,
			LastBeat:          beat.LastBeat,
			AgeSeconds:        beat.AgeSeconds,
			StaleAfterSeconds: beat.StaleAfter,
		})
	}

	health.Consumers = h.consumerSnapshot()
	for _, consumer := range health.Consumers {
		if !consumer.Exists {
			degraded = true
		}
	}

	if len(health.StuckCampaigns) > 0 {
		degraded = true
	}
	if degraded {
		health.Status = "degraded"
	}

	utils.WriteJSON(w, http.StatusOK, health)
}

func (h *AdminHandler) consumerSnapshot() []domain.AdminConsumer {
	consumers := make([]domain.AdminConsumer, 0, 2)
	if h.natsJS == nil {
		return consumers
	}
	for _, target := range []struct {
		service string
		durable string
	}{
		{"broadcast-worker", "broadcast-delivery-pool"},
		{"telemetry-worker", "telemetry-gateway-group"},
	} {
		consumer := domain.AdminConsumer{Service: target.service}
		if info, err := h.natsJS.ConsumerInfo("CAMPAIGNS", target.durable); err == nil {
			consumer.Exists = true
			consumer.Pending = info.NumPending
			consumer.AckPending = info.NumAckPending
			consumer.Delivered = info.Delivered.Stream
		}
		consumers = append(consumers, consumer)
	}
	return consumers
}

func (h *AdminHandler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := domain.AdminWorkspaceFilter{
		Query:     strings.TrimSpace(query.Get("q")),
		Page:      atoiOrZero(query.Get("page")),
		PageSize:  atoiOrZero(query.Get("page_size")),
		Sort:      query.Get("sort"),
		Direction: query.Get("direction"),
	}

	list, err := h.repo.ListWorkspaces(r.Context(), filter)
	if err != nil {
		log.Printf("[ADMIN] workspace list failed: %v\n", err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load workspaces")
		return
	}
	utils.WriteJSON(w, http.StatusOK, list)
}

func (h *AdminHandler) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if _, err := uuid.Parse(tenantID); err != nil {
		utils.WriteError(w, http.StatusNotFound, "Workspace not found")
		return
	}

	detail, err := h.repo.GetWorkspace(r.Context(), tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrWorkspaceNotFound) {
			utils.WriteError(w, http.StatusNotFound, "Workspace not found")
			return
		}
		log.Printf("[ADMIN] workspace detail failed tenant=%s: %v\n", tenantID, err)
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load workspace detail")
		return
	}
	utils.WriteJSON(w, http.StatusOK, detail)
}

func atoiOrZero(raw string) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}
