package usecase_test

import (
	"context"
	"errors"
	"testing"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/usecase"
)

type mockCannedReplyRepo struct {
	replies map[string]*domain.CannedReply
}

func (m *mockCannedReplyRepo) Create(ctx context.Context, r *domain.CannedReply) error {
	r.ID = "cr-1"
	m.replies[r.ID] = r
	return nil
}

func (m *mockCannedReplyRepo) ListByTenant(ctx context.Context, tenantID string) ([]*domain.CannedReply, error) {
	var res []*domain.CannedReply
	for _, r := range m.replies {
		if r.TenantID == tenantID {
			res = append(res, r)
		}
	}
	return res, nil
}

func (m *mockCannedReplyRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.CannedReply, error) {
	r, ok := m.replies[id]
	if !ok || r.TenantID != tenantID {
		return nil, errors.New("not found")
	}
	return r, nil
}

func (m *mockCannedReplyRepo) Update(ctx context.Context, r *domain.CannedReply) error {
	m.replies[r.ID] = r
	return nil
}

func (m *mockCannedReplyRepo) Delete(ctx context.Context, tenantID, id string) error {
	delete(m.replies, id)
	return nil
}

func TestCannedReplyUseCase(t *testing.T) {
	repo := &mockCannedReplyRepo{replies: make(map[string]*domain.CannedReply)}
	uc := usecase.NewCannedReplyUseCase(repo)
	ctx := context.Background()

	// 1. Create with leading slash should normalize
	r, err := uc.Create(ctx, "t-1", "/payment", "Payment Request", "Please pay here: {{link}}", "u-1")
	if err != nil {
		t.Fatalf("unexpected error creating canned reply: %v", err)
	}
	if r.Shortcut != "payment" {
		t.Fatalf("expected shortcut 'payment', got '%s'", r.Shortcut)
	}

	// 2. List
	list, err := uc.List(ctx, "t-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 reply in list, got %d, err: %v", len(list), err)
	}

	// 3. Update
	updated, err := uc.Update(ctx, "t-1", r.ID, "/pay", "Updated Title", "Updated Body")
	if err != nil {
		t.Fatalf("unexpected error updating: %v", err)
	}
	if updated.Shortcut != "pay" {
		t.Fatalf("expected shortcut 'pay', got '%s'", updated.Shortcut)
	}

	// 4. Delete
	if err := uc.Delete(ctx, "t-1", r.ID); err != nil {
		t.Fatalf("unexpected error deleting: %v", err)
	}
}
