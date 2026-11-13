package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type mockPaymentRepo struct {
	createFunc         func(ctx context.Context, tx *domain.PaymentTransaction) error
	getByRefFunc       func(ctx context.Context, ref string) (*domain.PaymentTransaction, error)
	markPaidFunc       func(ctx context.Context, ref string, at time.Time) (*domain.PaymentTransaction, error)
	listByConvFunc     func(ctx context.Context, tenantID, convID string) ([]domain.PaymentTransaction, error)
	listByContactFunc  func(ctx context.Context, tenantID, contactID string) ([]domain.PaymentTransaction, error)
	getCampaignRevFunc func(ctx context.Context, tenantID, campaignID string) (int64, int, error)
}

func (m *mockPaymentRepo) CreateTransaction(ctx context.Context, tx *domain.PaymentTransaction) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, tx)
	}
	tx.ID = "tx-123"
	return nil
}

func (m *mockPaymentRepo) GetByReference(ctx context.Context, ref string) (*domain.PaymentTransaction, error) {
	if m.getByRefFunc != nil {
		return m.getByRefFunc(ctx, ref)
	}
	return nil, nil
}

func (m *mockPaymentRepo) MarkPaid(ctx context.Context, ref string, at time.Time) (*domain.PaymentTransaction, error) {
	if m.markPaidFunc != nil {
		return m.markPaidFunc(ctx, ref, at)
	}
	convID := "conv-1"
	return &domain.PaymentTransaction{
		ID:             "tx-123",
		TenantID:       "tenant-1",
		ConversationID: &convID,
		ContactID:      "contact-1",
		Amount:         2500000,
		Description:    "Vintage Black Jacket",
		Reference:      ref,
		Status:         domain.PaymentStatusPaid,
		PaidAt:         &at,
	}, nil
}

func (m *mockPaymentRepo) ListByConversation(ctx context.Context, tenantID, convID string) ([]domain.PaymentTransaction, error) {
	if m.listByConvFunc != nil {
		return m.listByConvFunc(ctx, tenantID, convID)
	}
	return nil, nil
}

func (m *mockPaymentRepo) ListByContact(ctx context.Context, tenantID, contactID string) ([]domain.PaymentTransaction, error) {
	if m.listByContactFunc != nil {
		return m.listByContactFunc(ctx, tenantID, contactID)
	}
	return nil, nil
}

func (m *mockPaymentRepo) GetCampaignRevenue(ctx context.Context, tenantID, campaignID string) (int64, int, error) {
	if m.getCampaignRevFunc != nil {
		return m.getCampaignRevFunc(ctx, tenantID, campaignID)
	}
	return 2500000, 1, nil
}

func (m *mockPaymentRepo) GetSettlementAccount(ctx context.Context, tenantID string) (*domain.SettlementAccount, error) {
	return nil, nil
}

func (m *mockPaymentRepo) UpsertSettlementAccount(ctx context.Context, account *domain.SettlementAccount) error {
	return nil
}

type mockBroadcaster struct {
	messages []*domain.Message
}

func (b *mockBroadcaster) BroadcastMessage(tenantID string, conv *domain.Conversation, msg *domain.Message) {
	b.messages = append(b.messages, msg)
}

func (b *mockBroadcaster) BroadcastConversationAIState(tenantID, conversationID, aiState string, pausedUntil *time.Time, needsHuman bool) {
}
func (b *mockBroadcaster) BroadcastStatusUpdate(tenantID, conversationID, messageID, status string) {}
func (b *mockBroadcaster) BroadcastMessageEdit(tenantID, conversationID, messageID, newBody string) {}
func (b *mockBroadcaster) BroadcastMessageDelete(tenantID, conversationID, messageID string)        {}

func TestCommerceUseCase_HandlePaymentWebhook(t *testing.T) {
	paidCalled := false
	paymentRepo := &mockPaymentRepo{
		markPaidFunc: func(ctx context.Context, ref string, at time.Time) (*domain.PaymentTransaction, error) {
			paidCalled = true
			convID := "conv-10"
			return &domain.PaymentTransaction{
				ID:             "tx-10",
				TenantID:       "tenant-10",
				ConversationID: &convID,
				ContactID:      "contact-10",
				Amount:         1500000, // ₦15,000
				Description:    "Silk Scarf",
				Reference:      ref,
				Status:         domain.PaymentStatusPaid,
				PaidAt:         &at,
			}, nil
		},
	}

	createdMessages := []*domain.Message{}
	inboxRepo := &mockInboxRepo{
		createMessageFunc: func(ctx context.Context, msg *domain.Message) error {
			createdMessages = append(createdMessages, msg)
			return nil
		},
		getConversationFunc: func(ctx context.Context, tID, cID string) (*domain.Conversation, error) {
			return &domain.Conversation{ID: cID, TenantID: tID}, nil
		},
	}

	broadcaster := &mockBroadcaster{}

	uc := NewCommerceUseCase(paymentRepo, inboxRepo, nil, nil, nil, nil, nil, "https://app.messagerail.com")
	uc.SetBroadcaster(broadcaster)

	ref := "pstk_ref_test_123"
	err := uc.HandlePaymentWebhook(context.Background(), ref, 1500000, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !paidCalled {
		t.Fatal("expected markPaid to be called")
	}

	if len(createdMessages) != 1 {
		t.Fatalf("expected 1 system message created, got %d", len(createdMessages))
	}

	msg := createdMessages[0]
	if !strings.Contains(msg.Body, "15,000") || !strings.Contains(msg.Body, "Silk Scarf") {
		t.Fatalf("unexpected message body: %s", msg.Body)
	}

	if len(broadcaster.messages) != 1 {
		t.Fatalf("expected 1 message broadcasted, got %d", len(broadcaster.messages))
	}
}

func TestFormatNairaKobo(t *testing.T) {
	tests := []struct {
		kobo     int64
		expected string
	}{
		{kobo: 2500000, expected: "25,000"},
		{kobo: 100000, expected: "1,000"},
		{kobo: 50, expected: "0.50"},
		{kobo: 150050, expected: "1,500.50"},
		{kobo: 145000000, expected: "1,450,000"},
	}

	for _, tc := range tests {
		got := formatNairaKobo(tc.kobo)
		if got != tc.expected {
			t.Errorf("formatNairaKobo(%d) = %q, want %q", tc.kobo, got, tc.expected)
		}
	}
}
