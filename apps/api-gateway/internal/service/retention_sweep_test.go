package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type sweepRepo struct {
	domain.ContactRepository
	expiredBatches [][]string
	expiredErr     error
	deleted        []string
	deleteErr      error

	mu          sync.Mutex
	nowReceived time.Time
	calls       int
}

func (m *sweepRepo) FindAnonymizedExpired(ctx context.Context, now time.Time, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nowReceived = now
	m.calls++
	if m.expiredErr != nil {
		return nil, m.expiredErr
	}
	if len(m.expiredBatches) == 0 {
		return nil, nil
	}
	ids := m.expiredBatches[0]
	m.expiredBatches = m.expiredBatches[1:]
	return ids, nil
}

func (m *sweepRepo) HardDelete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, id)
	return nil
}

func TestRetentionSweepDeletesExpiredRecipients(t *testing.T) {
	repo := &sweepRepo{expiredBatches: [][]string{{"a", "b"}, {"c"}}}
	svc := NewRetentionSweepService(repo)
	svc.batchSize = 2 // force each prepared batch to count as a full one
	sweepNow := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return sweepNow }

	svc.sweep(context.Background())

	// Two full/short batch reads: {"a","b"} fills the batch, {"c"} is the
	// short tail that ends the run.
	if repo.calls != 2 {
		t.Fatalf("expected 2 find calls, got %d", repo.calls)
	}
	if repo.nowReceived != sweepNow {
		t.Fatalf("sweep queried with wrong time: %v", repo.nowReceived)
	}
	if len(repo.deleted) != 3 {
		t.Fatalf("expected 3 hard-deletes, got %v", repo.deleted)
	}
	for _, id := range []string{"a", "b", "c"} {
		found := false
		for _, d := range repo.deleted {
			if d == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("recipient %s was not hard-deleted", id)
		}
	}
}

func TestRetentionSweepStopsWhenNothingExpired(t *testing.T) {
	repo := &sweepRepo{}
	svc := NewRetentionSweepService(repo)

	svc.sweep(context.Background())

	if repo.calls != 1 {
		t.Fatalf("expected a single find call, got %d", repo.calls)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("no deletions expected, got %v", repo.deleted)
	}
}

func TestRetentionSweepToleratesDeleteFailures(t *testing.T) {
	repo := &sweepRepo{
		expiredBatches: [][]string{{"ok", "broken"}},
		deleteErr:      errors.New("db gone"),
	}
	svc := NewRetentionSweepService(repo)
	// Suppress error logging noise in test output.
	svc.sweep(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.deleted) != 0 {
		t.Fatalf("all deletes fail, none should be recorded: %v", repo.deleted)
	}
}

func TestRetentionSweepCapsBatchesPerRun(t *testing.T) {
	batches := make([][]string, 12)
	for i := range batches {
		batches[i] = []string{"id"}
	}
	repo := &sweepRepo{expiredBatches: batches}
	svc := NewRetentionSweepService(repo)
	svc.batchSize = 1 // every prepared batch counts as full, so only maxBatches stops the run

	svc.sweep(context.Background())

	// maxBatches caps a run so one backlog cannot monopolize the loop; the
	// rest is picked up on the next tick.
	if repo.calls != 10 {
		t.Fatalf("expected 10 find calls, got %d", repo.calls)
	}
}
