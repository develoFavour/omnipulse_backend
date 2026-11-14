package service

import (
	"context"
	"sync"

	"omnipulse/apps/api-gateway/internal/domain"
)

// ContactCapacity is a workspace's recipient allowance as it stood when the
// subscription and directory were read. Limit is -1 when the plan is unlimited.
type ContactCapacity struct {
	Limit     int
	Used      int
	Remaining int
	PlanTier  domain.PlanTier
}

func (c ContactCapacity) Unlimited() bool { return c.Limit == -1 }

// OverCapacity is true when the directory already holds more recipients than
// the plan allows. Those rows are grandfathered: they are kept, but imports
// cannot make the gap wider.
func (c ContactCapacity) OverCapacity() bool {
	return !c.Unlimited() && c.Used > c.Limit
}

// ContactCapacityGuard is the single gate every bulk recipient source
// (WhatsApp store sync, HistorySync, Telegram sync) goes through. It reads the
// plan allowance from the subscription and serialises imports per workspace,
// so two concurrent syncs cannot both spend the same remaining slots.
type ContactCapacityGuard struct {
	subRepo     domain.SubscriptionRepository
	contactRepo domain.ContactRepository

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewContactCapacityGuard(subRepo domain.SubscriptionRepository, contactRepo domain.ContactRepository) *ContactCapacityGuard {
	return &ContactCapacityGuard{
		subRepo:     subRepo,
		contactRepo: contactRepo,
		locks:       make(map[string]*sync.Mutex),
	}
}

// Lock blocks until no other import is running for this workspace and returns
// the matching unlock. Safe to call on a nil guard.
func (g *ContactCapacityGuard) Lock(tenantID string) func() {
	if g == nil || tenantID == "" {
		return func() {}
	}

	g.mu.Lock()
	tenantLock, ok := g.locks[tenantID]
	if !ok {
		tenantLock = &sync.Mutex{}
		g.locks[tenantID] = tenantLock
	}
	g.mu.Unlock()

	tenantLock.Lock()
	return tenantLock.Unlock
}

// Capacity reads the plan allowance and current directory size. A billing or
// count failure falls back to unlimited, matching RegisterContact: a lookup
// problem must not block recipients from being recorded.
func (g *ContactCapacityGuard) Capacity(ctx context.Context, tenantID string) ContactCapacity {
	unlimited := ContactCapacity{Limit: -1}
	if g == nil || g.subRepo == nil || g.contactRepo == nil || tenantID == "" {
		return unlimited
	}

	sub, err := g.subRepo.GetByTenantID(ctx, tenantID)
	if err != nil || sub == nil || sub.ContactsLimit == -1 {
		return unlimited
	}

	_, used, err := g.contactRepo.ListWithFilter(ctx, tenantID, domain.ContactFilter{Page: 1, PageSize: 1, Limit: 1})
	if err != nil {
		return unlimited
	}

	remaining := sub.ContactsLimit - used
	if remaining < 0 {
		remaining = 0
	}

	return ContactCapacity{
		Limit:     sub.ContactsLimit,
		Used:      used,
		Remaining: remaining,
		PlanTier:  sub.PlanTier,
	}
}

// Budget snapshots the capacity for one import run. Rows that already exist
// are updates and cost nothing, so re-syncing a full directory is always safe.
func (g *ContactCapacityGuard) Budget(ctx context.Context, tenantID string) *ContactBudget {
	return &ContactBudget{capacity: g.Capacity(ctx, tenantID)}
}

type ContactBudget struct {
	capacity ContactCapacity
	created  int
	skipped  int
}

// Allows reports whether one more new recipient fits in the plan.
func (b *ContactBudget) Allows() bool {
	if b == nil || b.capacity.Unlimited() {
		return true
	}
	return b.capacity.Remaining-b.created > 0
}

func (b *ContactBudget) RecordNew() {
	if b != nil {
		b.created++
	}
}

func (b *ContactBudget) RecordSkipped() {
	if b != nil {
		b.skipped++
	}
}

func (b *ContactBudget) Created() int {
	if b == nil {
		return 0
	}
	return b.created
}

func (b *ContactBudget) Skipped() int {
	if b == nil {
		return 0
	}
	return b.skipped
}

func (b *ContactBudget) Limit() int {
	if b == nil {
		return -1
	}
	return b.capacity.Limit
}

func (b *ContactBudget) OverCapacity() bool {
	if b == nil {
		return false
	}
	return b.capacity.OverCapacity()
}

// ContactSyncResult is what a bulk import reports back to the operator so a
// partially applied sync can explain itself instead of failing outright.
type ContactSyncResult struct {
	Created      int
	Skipped      int
	Total        int
	Limit        int // -1 when unlimited
	OverCapacity bool
}

// LimitReached indicates this run actually left recipients behind.
func (r ContactSyncResult) LimitReached() bool { return r.Skipped > 0 }
