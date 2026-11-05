package service

import (
	"sort"
	"sync"
	"time"
)

// HeartbeatStatus is the health of one in-process background loop.
type HeartbeatStatus struct {
	Service    string `json:"service"`
	Healthy    bool   `json:"healthy"`
	LastBeat   string `json:"last_beat"`
	AgeSeconds int64  `json:"age_seconds"`
	StaleAfter int64  `json:"stale_after_seconds"`
}

type workerHeartbeat struct {
	last       time.Time
	staleAfter time.Duration
}

// HeartbeatRegistry tracks when each background loop last ran. A loop that
// stops beating while the HTTP server is still up means silent broadcast or
// scheduling failure, which uptime monitors cannot see from /health alone.
type HeartbeatRegistry struct {
	mu      sync.RWMutex
	workers map[string]*workerHeartbeat
	now     func() time.Time
}

func NewHeartbeatRegistry() *HeartbeatRegistry {
	return &HeartbeatRegistry{
		workers: map[string]*workerHeartbeat{},
		now:     time.Now,
	}
}

// Beat records a liveness pulse for name, registering it on first call.
func (r *HeartbeatRegistry) Beat(name string, staleAfter time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.workers == nil {
		r.workers = map[string]*workerHeartbeat{}
	}
	beat, ok := r.workers[name]
	if !ok {
		beat = &workerHeartbeat{}
		r.workers[name] = beat
	}
	beat.last = r.now()
	beat.staleAfter = staleAfter
}

// Snapshot returns every registered loop sorted by name.
func (r *HeartbeatRegistry) Snapshot() []HeartbeatStatus {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.now()
	statuses := make([]HeartbeatStatus, 0, len(r.workers))
	for name, beat := range r.workers {
		age := now.Sub(beat.last)
		healthy := beat.staleAfter <= 0 || age <= beat.staleAfter
		statuses = append(statuses, HeartbeatStatus{
			Service:    name,
			Healthy:    healthy,
			LastBeat:   beat.last.UTC().Format(time.RFC3339),
			AgeSeconds: int64(age.Seconds()),
			StaleAfter: int64(beat.staleAfter.Seconds()),
		})
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Service < statuses[j].Service })
	return statuses
}

// Healthy reports whether every registered loop has beaten recently.
func (r *HeartbeatRegistry) Healthy() bool {
	for _, status := range r.Snapshot() {
		if !status.Healthy {
			return false
		}
	}
	return true
}
