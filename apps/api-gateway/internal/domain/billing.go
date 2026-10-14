package domain

import (
	"context"
	"errors"
	"time"
)

var ErrQuotaExceeded = errors.New("plan resource quota exceeded")

type PlanTier string
type BillingProvider string
type BillingCycle string

const (
	PlanStarter PlanTier = "starter"
	PlanGrowth  PlanTier = "growth"
	PlanAgency  PlanTier = "agency"

	ProviderFree     BillingProvider = "free"
	ProviderStripe   BillingProvider = "stripe"
	ProviderPaystack BillingProvider = "paystack"

	CycleMonthly BillingCycle = "monthly"
	CycleYearly  BillingCycle = "yearly"
)

type PlanConfig struct {
	Tier         PlanTier `json:"tier"`
	Name         string   `json:"name"`
	USDMonthly   int      `json:"usd_monthly"`   // cents (e.g. 1400 = $14)
	USDYearly    int      `json:"usd_yearly"`    // cents (e.g. 14000 = $140)
	NGNMonthly   int      `json:"ngn_monthly"`   // kobo (e.g. 650000 = ₦6,500)
	NGNYearly    int      `json:"ngn_yearly"`    // kobo (e.g. 6500000 = ₦65,000)
	MessageLimit int      `json:"message_limit"` // -1 for unlimited
	ContactLimit int      `json:"contact_limit"` // -1 for unlimited
	ChannelLimit int      `json:"channel_limit"` // -1 for unlimited
}

var AvailablePlans = map[PlanTier]PlanConfig{
	PlanStarter: {
		Tier:         PlanStarter,
		Name:         "Starter",
		USDMonthly:   0,
		USDYearly:    0,
		NGNMonthly:   0,
		NGNYearly:    0,
		MessageLimit: 1000,
		ContactLimit: 500,
		ChannelLimit: 2,
	},
	PlanGrowth: {
		Tier:         PlanGrowth,
		Name:         "Growth",
		USDMonthly:   1400,    // $14.00
		USDYearly:    14000,   // $140.00
		NGNMonthly:   650000,  // ₦6,500
		NGNYearly:    6500000, // ₦65,000
		MessageLimit: 25000,
		ContactLimit: 5000,
		ChannelLimit: -1,
	},
	PlanAgency: {
		Tier:         PlanAgency,
		Name:         "Agency",
		USDMonthly:   3400,     // $34.00
		USDYearly:    34000,    // $340.00
		NGNMonthly:   1750000,  // ₦17,500
		NGNYearly:    17500000, // ₦175,000
		MessageLimit: 100000,
		ContactLimit: 25000,
		ChannelLimit: -1,
	},
}

type Subscription struct {
	ID                  string          `json:"id"`
	TenantID            string          `json:"tenant_id"`
	Provider            BillingProvider `json:"provider"`
	CustomerID          string          `json:"customer_id,omitempty"`
	SubscriptionID      string          `json:"subscription_id,omitempty"`
	PlanTier            PlanTier        `json:"plan_tier"`
	BillingCycle        BillingCycle    `json:"billing_cycle"`
	Currency            string          `json:"currency"`
	Amount              int             `json:"amount"`
	Status              string          `json:"status"` // active, past_due, canceled, trialing
	CurrentPeriodStart  time.Time       `json:"current_period_start"`
	CurrentPeriodEnd    *time.Time      `json:"current_period_end,omitempty"`
	MonthlyMessageLimit int             `json:"monthly_message_limit"`
	ContactsLimit       int             `json:"contacts_limit"`
	ChannelsLimit       int             `json:"channels_limit"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type CheckoutRequest struct {
	PlanTier     PlanTier     `json:"plan_tier"`
	BillingCycle BillingCycle `json:"billing_cycle"`
	Currency     string       `json:"currency"` // "USD" or "NGN"
	SuccessURL   string       `json:"success_url,omitempty"`
	CancelURL    string       `json:"cancel_url,omitempty"`
}

type CheckoutResponse struct {
	CheckoutURL string `json:"checkout_url"`
	Reference   string `json:"reference,omitempty"`
	Provider    string `json:"provider"`
}

type SubscriptionRepository interface {
	GetByTenantID(ctx context.Context, tenantID string) (*Subscription, error)
	Upsert(ctx context.Context, sub *Subscription) error
	GetBySubscriptionID(ctx context.Context, provider, subscriptionID string) (*Subscription, error)
	GetByCustomerID(ctx context.Context, provider, customerID string) (*Subscription, error)
}
