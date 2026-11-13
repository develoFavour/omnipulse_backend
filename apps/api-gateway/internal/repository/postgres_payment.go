package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresPaymentRepository struct {
	db *sql.DB
}

func NewPostgresPaymentRepository(db *sql.DB) *PostgresPaymentRepository {
	return &PostgresPaymentRepository{db: db}
}

func (r *PostgresPaymentRepository) CreateTransaction(ctx context.Context, tx *domain.PaymentTransaction) error {
	query := `
		INSERT INTO payment_transactions (
			id, tenant_id, conversation_id, contact_id, campaign_id,
			amount, currency, description, reference, paystack_url,
			subaccount_code, status, created_at, updated_at
		) VALUES (
			COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()),
			$2, NULLIF($3, '')::uuid, $4, NULLIF($5, '')::uuid,
			$6, $7, $8, $9, $10,
			$11, $12, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)
		RETURNING id, created_at, updated_at;
	`
	convID := ""
	if tx.ConversationID != nil {
		convID = *tx.ConversationID
	}
	campID := ""
	if tx.CampaignID != nil {
		campID = *tx.CampaignID
	}

	err := r.db.QueryRowContext(
		ctx, query,
		tx.ID, tx.TenantID, convID, tx.ContactID, campID,
		tx.Amount, tx.Currency, tx.Description, tx.Reference, tx.PaystackURL,
		tx.SubaccountCode, string(tx.Status),
	).Scan(&tx.ID, &tx.CreatedAt, &tx.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create payment transaction: %w", err)
	}
	return nil
}

func (r *PostgresPaymentRepository) GetByReference(ctx context.Context, reference string) (*domain.PaymentTransaction, error) {
	query := `
		SELECT 
			pt.id, pt.tenant_id, pt.conversation_id, pt.contact_id, pt.campaign_id,
			pt.amount, pt.currency, pt.description, pt.reference, pt.paystack_url,
			pt.subaccount_code, pt.status, pt.paid_at, pt.created_at, pt.updated_at,
			COALESCE(c.first_name || ' ' || c.last_name, c.routing_value, '') AS contact_name,
			c.routing_value AS contact_phone
		FROM payment_transactions pt
		LEFT JOIN contacts c ON c.id = pt.contact_id
		WHERE pt.reference = $1;
	`
	var tx domain.PaymentTransaction
	var convID, campID, subaccountCode sql.NullString
	var paidAt sql.NullTime
	var statusStr string

	err := r.db.QueryRowContext(ctx, query, reference).Scan(
		&tx.ID, &tx.TenantID, &convID, &tx.ContactID, &campID,
		&tx.Amount, &tx.Currency, &tx.Description, &tx.Reference, &tx.PaystackURL,
		&subaccountCode, &statusStr, &paidAt, &tx.CreatedAt, &tx.UpdatedAt,
		&tx.ContactName, &tx.ContactPhone,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get payment transaction by reference: %w", err)
	}

	tx.Status = domain.PaymentTransactionStatus(statusStr)
	if convID.Valid {
		tx.ConversationID = &convID.String
	}
	if campID.Valid {
		tx.CampaignID = &campID.String
	}
	if subaccountCode.Valid {
		tx.SubaccountCode = &subaccountCode.String
	}
	if paidAt.Valid {
		tx.PaidAt = &paidAt.Time
	}

	return &tx, nil
}

func (r *PostgresPaymentRepository) MarkPaid(ctx context.Context, reference string, paidAt time.Time) (*domain.PaymentTransaction, error) {
	query := `
		UPDATE payment_transactions
		SET status = 'paid', paid_at = $1, updated_at = CURRENT_TIMESTAMP
		WHERE reference = $2
		RETURNING id, tenant_id, conversation_id, contact_id, campaign_id,
		          amount, currency, description, reference, paystack_url,
		          status, paid_at, created_at, updated_at;
	`
	var tx domain.PaymentTransaction
	var convID, campID sql.NullString
	var paidAtTime sql.NullTime
	var statusStr string

	err := r.db.QueryRowContext(ctx, query, paidAt, reference).Scan(
		&tx.ID, &tx.TenantID, &convID, &tx.ContactID, &campID,
		&tx.Amount, &tx.Currency, &tx.Description, &tx.Reference, &tx.PaystackURL,
		&statusStr, &paidAtTime, &tx.CreatedAt, &tx.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("payment transaction not found for reference")
		}
		return nil, fmt.Errorf("failed to mark payment transaction as paid: %w", err)
	}

	tx.Status = domain.PaymentTransactionStatus(statusStr)
	if convID.Valid {
		tx.ConversationID = &convID.String
	}
	if campID.Valid {
		tx.CampaignID = &campID.String
	}
	if paidAtTime.Valid {
		tx.PaidAt = &paidAtTime.Time
	}

	return &tx, nil
}

func (r *PostgresPaymentRepository) ListByConversation(ctx context.Context, tenantID, conversationID string) ([]domain.PaymentTransaction, error) {
	query := `
		SELECT 
			id, tenant_id, conversation_id, contact_id, campaign_id,
			amount, currency, description, reference, paystack_url,
			status, paid_at, created_at, updated_at
		FROM payment_transactions
		WHERE tenant_id = $1 AND conversation_id = $2
		ORDER BY created_at DESC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to list payment transactions by conversation: %w", err)
	}
	defer rows.Close()

	var results []domain.PaymentTransaction
	for rows.Next() {
		var tx domain.PaymentTransaction
		var convID, campID sql.NullString
		var paidAt sql.NullTime
		var statusStr string

		if err := rows.Scan(
			&tx.ID, &tx.TenantID, &convID, &tx.ContactID, &campID,
			&tx.Amount, &tx.Currency, &tx.Description, &tx.Reference, &tx.PaystackURL,
			&statusStr, &paidAt, &tx.CreatedAt, &tx.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan payment transaction: %w", err)
		}

		tx.Status = domain.PaymentTransactionStatus(statusStr)
		if convID.Valid {
			tx.ConversationID = &convID.String
		}
		if campID.Valid {
			tx.CampaignID = &campID.String
		}
		if paidAt.Valid {
			tx.PaidAt = &paidAt.Time
		}
		results = append(results, tx)
	}

	return results, nil
}

func (r *PostgresPaymentRepository) ListByContact(ctx context.Context, tenantID, contactID string) ([]domain.PaymentTransaction, error) {
	query := `
		SELECT 
			id, tenant_id, conversation_id, contact_id, campaign_id,
			amount, currency, description, reference, paystack_url,
			status, paid_at, created_at, updated_at
		FROM payment_transactions
		WHERE tenant_id = $1 AND contact_id = $2
		ORDER BY created_at DESC;
	`
	rows, err := r.db.QueryContext(ctx, query, tenantID, contactID)
	if err != nil {
		return nil, fmt.Errorf("failed to list payment transactions by contact: %w", err)
	}
	defer rows.Close()

	var results []domain.PaymentTransaction
	for rows.Next() {
		var tx domain.PaymentTransaction
		var convID, campID sql.NullString
		var paidAt sql.NullTime
		var statusStr string

		if err := rows.Scan(
			&tx.ID, &tx.TenantID, &convID, &tx.ContactID, &campID,
			&tx.Amount, &tx.Currency, &tx.Description, &tx.Reference, &tx.PaystackURL,
			&statusStr, &paidAt, &tx.CreatedAt, &tx.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan payment transaction: %w", err)
		}

		tx.Status = domain.PaymentTransactionStatus(statusStr)
		if convID.Valid {
			tx.ConversationID = &convID.String
		}
		if campID.Valid {
			tx.CampaignID = &campID.String
		}
		if paidAt.Valid {
			tx.PaidAt = &paidAt.Time
		}
		results = append(results, tx)
	}

	return results, nil
}

func (r *PostgresPaymentRepository) GetCampaignRevenue(ctx context.Context, tenantID, campaignID string) (int64, int, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0), COUNT(*)
		FROM payment_transactions
		WHERE tenant_id = $1 AND campaign_id = $2 AND status = 'paid';
	`
	var totalKobo int64
	var count int
	err := r.db.QueryRowContext(ctx, query, tenantID, campaignID).Scan(&totalKobo, &count)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query campaign revenue: %w", err)
	}
	return totalKobo, count, nil
}

func (r *PostgresPaymentRepository) GetSettlementAccount(ctx context.Context, tenantID string) (*domain.SettlementAccount, error) {
	query := `
		SELECT id, tenant_id, business_name, settlement_bank, bank_name,
		       account_number, account_name, subaccount_code, percentage_charge,
		       currency, is_verified, created_at, updated_at
		FROM tenant_settlement_accounts
		WHERE tenant_id = $1;
	`
	var acc domain.SettlementAccount
	err := r.db.QueryRowContext(ctx, query, tenantID).Scan(
		&acc.ID, &acc.TenantID, &acc.BusinessName, &acc.SettlementBank, &acc.BankName,
		&acc.AccountNumber, &acc.AccountName, &acc.SubaccountCode, &acc.PercentageCharge,
		&acc.Currency, &acc.IsVerified, &acc.CreatedAt, &acc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get settlement account: %w", err)
	}
	return &acc, nil
}

func (r *PostgresPaymentRepository) UpsertSettlementAccount(ctx context.Context, account *domain.SettlementAccount) error {
	query := `
		INSERT INTO tenant_settlement_accounts (
			tenant_id, business_name, settlement_bank, bank_name,
			account_number, account_name, subaccount_code, percentage_charge,
			currency, is_verified, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)
		ON CONFLICT (tenant_id) DO UPDATE SET
			business_name = EXCLUDED.business_name,
			settlement_bank = EXCLUDED.settlement_bank,
			bank_name = EXCLUDED.bank_name,
			account_number = EXCLUDED.account_number,
			account_name = EXCLUDED.account_name,
			subaccount_code = EXCLUDED.subaccount_code,
			percentage_charge = EXCLUDED.percentage_charge,
			currency = EXCLUDED.currency,
			is_verified = EXCLUDED.is_verified,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id, created_at, updated_at;
	`
	err := r.db.QueryRowContext(
		ctx, query,
		account.TenantID, account.BusinessName, account.SettlementBank, account.BankName,
		account.AccountNumber, account.AccountName, account.SubaccountCode, account.PercentageCharge,
		account.Currency, account.IsVerified,
	).Scan(&account.ID, &account.CreatedAt, &account.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to upsert settlement account: %w", err)
	}
	return nil
}
