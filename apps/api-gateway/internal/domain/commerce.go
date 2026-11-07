package domain

import (
	"context"
	"time"
)

type PaymentTransactionStatus string

const (
	PaymentStatusPending PaymentTransactionStatus = "pending"
	PaymentStatusPaid    PaymentTransactionStatus = "paid"
	PaymentStatusFailed  PaymentTransactionStatus = "failed"
	PaymentStatusExpired PaymentTransactionStatus = "expired"
)

// PaymentTransaction represents a conversational commerce payment request and fulfillment
type PaymentTransaction struct {
	ID             string                   `json:"id"`
	TenantID       string                   `json:"tenant_id"`
	ConversationID *string                  `json:"conversation_id,omitempty"`
	ContactID      string                   `json:"contact_id"`
	CampaignID     *string                  `json:"campaign_id,omitempty"`
	Amount         int64                    `json:"amount"` // in kobo (e.g. 2500000 = ₦25,000)
	Currency       string                   `json:"currency"`
	Description    string                   `json:"description"`
	Reference      string                   `json:"reference"`
	PaystackURL    string                   `json:"paystack_url"`
	Status         PaymentTransactionStatus `json:"status"`
	PaidAt         *time.Time               `json:"paid_at,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`

	// Populated in queries
	ContactName  string `json:"contact_name,omitempty"`
	ContactPhone string `json:"contact_phone,omitempty"`
}

type CreatePaymentLinkRequest struct {
	AmountKobo    int64   `json:"amount"` // in kobo
	Description   string  `json:"description"`
	CustomerEmail string  `json:"customer_email,omitempty"`
	CampaignID    *string `json:"campaign_id,omitempty"`
}

type PaymentRepository interface {
	CreateTransaction(ctx context.Context, tx *PaymentTransaction) error
	GetByReference(ctx context.Context, reference string) (*PaymentTransaction, error)
	MarkPaid(ctx context.Context, reference string, paidAt time.Time) (*PaymentTransaction, error)
	ListByConversation(ctx context.Context, tenantID, conversationID string) ([]PaymentTransaction, error)
	ListByContact(ctx context.Context, tenantID, contactID string) ([]PaymentTransaction, error)
	GetCampaignRevenue(ctx context.Context, tenantID, campaignID string) (totalKobo int64, paidCount int, err error)
}
