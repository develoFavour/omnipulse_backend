package repository

import (
	"context"
	"database/sql"
	"fmt"
	"omnipulse/apps/api-gateway/internal/domain"
	"time"
)

type PostgresSubscriptionRepository struct {
	db *sql.DB
}

func NewPostgresSubscriptionRepository(db *sql.DB) domain.SubscriptionRepository {
	return &PostgresSubscriptionRepository{db: db}
}

func (r *PostgresSubscriptionRepository) GetByTenantID(ctx context.Context, tenantID string) (*domain.Subscription, error) {
	query := `
		SELECT 
			id, tenant_id, provider, customer_id, subscription_id, 
			plan_tier, billing_cycle, currency, amount, status, 
			current_period_start, current_period_end, 
			monthly_message_limit, contacts_limit, channels_limit, 
			created_at, updated_at
		FROM subscriptions
		WHERE tenant_id = $1
	`

	var s domain.Subscription
	var custID, subID sql.NullString
	var periodEnd sql.NullTime

	err := r.db.QueryRowContext(ctx, query, tenantID).Scan(
		&s.ID, &s.TenantID, &s.Provider, &custID, &subID,
		&s.PlanTier, &s.BillingCycle, &s.Currency, &s.Amount, &s.Status,
		&s.CurrentPeriodStart, &periodEnd,
		&s.MonthlyMessageLimit, &s.ContactsLimit, &s.ChannelsLimit,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			// Default to Free Starter if no record exists
			starter := domain.AvailablePlans[domain.PlanStarter]
			return &domain.Subscription{
				TenantID:            tenantID,
				Provider:            domain.ProviderFree,
				PlanTier:            domain.PlanStarter,
				BillingCycle:        domain.CycleMonthly,
				Currency:            "USD",
				Amount:              0,
				Status:              "active",
				CurrentPeriodStart:  time.Now(),
				MonthlyMessageLimit: starter.MessageLimit,
				ContactsLimit:       starter.ContactLimit,
				ChannelsLimit:       starter.ChannelLimit,
				CreatedAt:           time.Now(),
				UpdatedAt:           time.Now(),
			}, nil
		}
		return nil, fmt.Errorf("failed to query subscription by tenant: %w", err)
	}

	if custID.Valid {
		s.CustomerID = custID.String
	}
	if subID.Valid {
		s.SubscriptionID = subID.String
	}
	if periodEnd.Valid {
		s.CurrentPeriodEnd = &periodEnd.Time
	}

	return &s, nil
}

func (r *PostgresSubscriptionRepository) Upsert(ctx context.Context, sub *domain.Subscription) error {
	query := `
		INSERT INTO subscriptions (
			tenant_id, provider, customer_id, subscription_id, 
			plan_tier, billing_cycle, currency, amount, status, 
			current_period_start, current_period_end, 
			monthly_message_limit, contacts_limit, channels_limit, 
			updated_at
		) VALUES (
			$1, $2, $3, $4, 
			$5, $6, $7, $8, $9, 
			$10, $11, 
			$12, $13, $14, 
			NOW()
		)
		ON CONFLICT (tenant_id) DO UPDATE SET
			provider = EXCLUDED.provider,
			customer_id = COALESCE(EXCLUDED.customer_id, subscriptions.customer_id),
			subscription_id = COALESCE(EXCLUDED.subscription_id, subscriptions.subscription_id),
			plan_tier = EXCLUDED.plan_tier,
			billing_cycle = EXCLUDED.billing_cycle,
			currency = EXCLUDED.currency,
			amount = EXCLUDED.amount,
			status = EXCLUDED.status,
			current_period_start = EXCLUDED.current_period_start,
			current_period_end = EXCLUDED.current_period_end,
			monthly_message_limit = EXCLUDED.monthly_message_limit,
			contacts_limit = EXCLUDED.contacts_limit,
			channels_limit = EXCLUDED.channels_limit,
			updated_at = NOW()
		RETURNING id;
	`

	var custID, subID *string
	if sub.CustomerID != "" {
		custID = &sub.CustomerID
	}
	if sub.SubscriptionID != "" {
		subID = &sub.SubscriptionID
	}

	return r.db.QueryRowContext(ctx, query,
		sub.TenantID, sub.Provider, custID, subID,
		sub.PlanTier, sub.BillingCycle, sub.Currency, sub.Amount, sub.Status,
		sub.CurrentPeriodStart, sub.CurrentPeriodEnd,
		sub.MonthlyMessageLimit, sub.ContactsLimit, sub.ChannelsLimit,
	).Scan(&sub.ID)
}

func (r *PostgresSubscriptionRepository) GetBySubscriptionID(ctx context.Context, provider, subscriptionID string) (*domain.Subscription, error) {
	query := `
		SELECT 
			id, tenant_id, provider, customer_id, subscription_id, 
			plan_tier, billing_cycle, currency, amount, status, 
			current_period_start, current_period_end, 
			monthly_message_limit, contacts_limit, channels_limit, 
			created_at, updated_at
		FROM subscriptions
		WHERE provider = $1 AND subscription_id = $2
	`

	var s domain.Subscription
	var custID, subID sql.NullString
	var periodEnd sql.NullTime

	err := r.db.QueryRowContext(ctx, query, provider, subscriptionID).Scan(
		&s.ID, &s.TenantID, &s.Provider, &custID, &subID,
		&s.PlanTier, &s.BillingCycle, &s.Currency, &s.Amount, &s.Status,
		&s.CurrentPeriodStart, &periodEnd,
		&s.MonthlyMessageLimit, &s.ContactsLimit, &s.ChannelsLimit,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query subscription by id: %w", err)
	}

	if custID.Valid {
		s.CustomerID = custID.String
	}
	if subID.Valid {
		s.SubscriptionID = subID.String
	}
	if periodEnd.Valid {
		s.CurrentPeriodEnd = &periodEnd.Time
	}

	return &s, nil
}

func (r *PostgresSubscriptionRepository) GetByCustomerID(ctx context.Context, provider, customerID string) (*domain.Subscription, error) {
	query := `
		SELECT 
			id, tenant_id, provider, customer_id, subscription_id, 
			plan_tier, billing_cycle, currency, amount, status, 
			current_period_start, current_period_end, 
			monthly_message_limit, contacts_limit, channels_limit, 
			created_at, updated_at
		FROM subscriptions
		WHERE provider = $1 AND customer_id = $2
	`

	var s domain.Subscription
	var custID, subID sql.NullString
	var periodEnd sql.NullTime

	err := r.db.QueryRowContext(ctx, query, provider, customerID).Scan(
		&s.ID, &s.TenantID, &s.Provider, &custID, &subID,
		&s.PlanTier, &s.BillingCycle, &s.Currency, &s.Amount, &s.Status,
		&s.CurrentPeriodStart, &periodEnd,
		&s.MonthlyMessageLimit, &s.ContactsLimit, &s.ChannelsLimit,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query subscription by customer: %w", err)
	}

	if custID.Valid {
		s.CustomerID = custID.String
	}
	if subID.Valid {
		s.SubscriptionID = subID.String
	}
	if periodEnd.Valid {
		s.CurrentPeriodEnd = &periodEnd.Time
	}

	return &s, nil
}
