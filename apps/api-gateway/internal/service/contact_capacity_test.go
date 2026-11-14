package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type stubCapacitySubRepo struct {
	domain.SubscriptionRepository
	sub *domain.Subscription
	err error
}

func (s *stubCapacitySubRepo) GetByTenantID(ctx context.Context, tenantID string) (*domain.Subscription, error) {
	return s.sub, s.err
}

type stubCapacityContactRepo struct {
	domain.ContactRepository
	total int
	err   error
}

func (s *stubCapacityContactRepo) ListWithFilter(ctx context.Context, tenantID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
	return nil, s.total, s.err
}

func newTestGuard(limit, used int) (*ContactCapacityGuard, *domain.Subscription) {
	sub := &domain.Subscription{TenantID: "tenant-1", PlanTier: domain.PlanStarter, ContactsLimit: limit}
	guard := NewContactCapacityGuard(
		&stubCapacitySubRepo{sub: sub},
		&stubCapacityContactRepo{total: used},
	)
	return guard, sub
}

func TestContactCapacity_ReadsPlanAllowance(t *testing.T) {
	t.Run("Reports remaining slots for a partly filled workspace", func(t *testing.T) {
		guard, _ := newTestGuard(500, 120)

		capacity := guard.Capacity(context.Background(), "tenant-1")
		if capacity.Limit != 500 || capacity.Used != 120 || capacity.Remaining != 380 {
			t.Fatalf("unexpected capacity: %+v", capacity)
		}
		if capacity.Unlimited() || capacity.OverCapacity() {
			t.Fatalf("expected a capped, non-over-capacity workspace: %+v", capacity)
		}
	})

	t.Run("Clamps remaining at zero for a grandfathered over-capacity workspace", func(t *testing.T) {
		guard, _ := newTestGuard(500, 812)

		capacity := guard.Capacity(context.Background(), "tenant-1")
		if capacity.Remaining != 0 {
			t.Fatalf("expected remaining to clamp at 0, got %d", capacity.Remaining)
		}
		if !capacity.OverCapacity() {
			t.Fatal("expected workspace above its plan limit to report over-capacity")
		}
	})

	t.Run("Treats -1 as unlimited", func(t *testing.T) {
		guard, _ := newTestGuard(-1, 50000)

		capacity := guard.Capacity(context.Background(), "tenant-1")
		if !capacity.Unlimited() || capacity.OverCapacity() {
			t.Fatalf("expected unlimited capacity: %+v", capacity)
		}
	})

	t.Run("Falls back to unlimited when the plan cannot be read", func(t *testing.T) {
		guard := NewContactCapacityGuard(
			&stubCapacitySubRepo{err: errors.New("db down")},
			&stubCapacityContactRepo{total: 10},
		)

		if !guard.Capacity(context.Background(), "tenant-1").Unlimited() {
			t.Fatal("expected a failed billing lookup to leave imports unrestricted")
		}
	})

	t.Run("Is unlimited without a guard or tenant", func(t *testing.T) {
		var nilGuard *ContactCapacityGuard
		if !nilGuard.Capacity(context.Background(), "tenant-1").Unlimited() {
			t.Fatal("expected nil guard to be unlimited")
		}
		guard, _ := newTestGuard(10, 0)
		if !guard.Capacity(context.Background(), "").Unlimited() {
			t.Fatal("expected empty tenant id to be unlimited")
		}
	})
}

func TestContactBudget_SpendsOnlyRemainingSlots(t *testing.T) {
	t.Run("Refuses new recipients once the allowance is spent", func(t *testing.T) {
		guard, _ := newTestGuard(3, 1) // 2 slots left
		budget := guard.Budget(context.Background(), "tenant-1")

		for i := 0; i < 2; i++ {
			if !budget.Allows() {
				t.Fatalf("expected slot %d to be available", i+1)
			}
			budget.RecordNew()
		}

		if budget.Allows() {
			t.Fatal("expected budget to be exhausted after using both slots")
		}
		budget.RecordSkipped()
		if budget.Created() != 2 || budget.Skipped() != 1 {
			t.Fatalf("unexpected counters: created=%d skipped=%d", budget.Created(), budget.Skipped())
		}
	})

	t.Run("Never limits an unlimited plan", func(t *testing.T) {
		guard, _ := newTestGuard(-1, 100000)
		budget := guard.Budget(context.Background(), "tenant-1")

		for i := 0; i < 100; i++ {
			budget.RecordNew()
		}
		if !budget.Allows() {
			t.Fatal("unlimited plans must always allow another recipient")
		}
		if budget.OverCapacity() {
			t.Fatal("unlimited plans cannot be over capacity")
		}
	})

	t.Run("Blocks new rows for a grandfathered workspace but reports it", func(t *testing.T) {
		guard, _ := newTestGuard(500, 640)
		budget := guard.Budget(context.Background(), "tenant-1")

		if budget.Allows() {
			t.Fatal("expected an over-capacity workspace to reject new recipients")
		}
		if !budget.OverCapacity() {
			t.Fatal("expected the budget to surface the grandfathered state")
		}
	})
}

func TestContactCapacityGuard_LockSerialisesPerTenant(t *testing.T) {
	guard := NewContactCapacityGuard(nil, nil)
	const wait = 2 * time.Second

	unlockA := guard.Lock("tenant-a")

	// A different workspace must not be blocked by tenant-a's import.
	lockedB := make(chan struct{})
	go func() {
		unlockB := guard.Lock("tenant-b")
		unlockB()
		close(lockedB)
	}()
	select {
	case <-lockedB:
	case <-time.After(wait):
		t.Fatal("another workspace's import was blocked by tenant-a")
	}

	// The same workspace must wait until its in-flight import finishes.
	acquiredA := make(chan struct{})
	go func() {
		unlockAgain := guard.Lock("tenant-a")
		unlockAgain()
		close(acquiredA)
	}()
	select {
	case <-acquiredA:
		t.Fatal("second lock for the same workspace acquired while the first was held")
	case <-time.After(100 * time.Millisecond):
	}

	unlockA()
	select {
	case <-acquiredA:
	case <-time.After(wait):
		t.Fatal("released workspace lock was never handed to the waiting import")
	}
}

func TestContactCapacityGuard_NilLockIsNoop(t *testing.T) {
	var guard *ContactCapacityGuard
	guard.Lock("tenant-1")()
}
