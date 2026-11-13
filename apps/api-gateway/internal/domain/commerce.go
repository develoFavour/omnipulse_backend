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

// PlatformCommissionPercent defines the default platform fee on conversational commerce checkouts
const PlatformCommissionPercent = 1.50

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
	SubaccountCode *string                  `json:"subaccount_code,omitempty"`
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

// SettlementAccount holds the linked agency/brand bank account on Paystack
type SettlementAccount struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	BusinessName     string    `json:"business_name"`
	SettlementBank   string    `json:"settlement_bank"` // e.g. "058"
	BankName         string    `json:"bank_name"`       // e.g. "Guaranty Trust Bank"
	AccountNumber    string    `json:"account_number"`
	AccountName      string    `json:"account_name"` // Resolved via Paystack API
	SubaccountCode   string    `json:"subaccount_code"`
	PercentageCharge float64   `json:"percentage_charge"` // Platform fee % (e.g. 1.5)
	Currency         string    `json:"currency"`
	IsVerified       bool      `json:"is_verified"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type SaveSettlementAccountRequest struct {
	BusinessName   string `json:"business_name"`
	SettlementBank string `json:"settlement_bank"` // bank code
	BankName       string `json:"bank_name"`
	AccountNumber  string `json:"account_number"`
}

type BankInfo struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Active bool   `json:"active"`
}

type ResolveAccountResponse struct {
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

type PaymentRepository interface {
	CreateTransaction(ctx context.Context, tx *PaymentTransaction) error
	GetByReference(ctx context.Context, reference string) (*PaymentTransaction, error)
	MarkPaid(ctx context.Context, reference string, paidAt time.Time) (*PaymentTransaction, error)
	ListByConversation(ctx context.Context, tenantID, conversationID string) ([]PaymentTransaction, error)
	ListByContact(ctx context.Context, tenantID, contactID string) ([]PaymentTransaction, error)
	GetCampaignRevenue(ctx context.Context, tenantID, campaignID string) (totalKobo int64, paidCount int, err error)

	GetSettlementAccount(ctx context.Context, tenantID string) (*SettlementAccount, error)
	UpsertSettlementAccount(ctx context.Context, account *SettlementAccount) error
}
