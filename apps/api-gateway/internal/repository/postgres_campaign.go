package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lib/pq"
	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"
)

var ErrCampaignNotFound = errors.New("campaign not found")

type PostgresCampaignRepository struct {
	db *sql.DB
}

func NewPostgresCampaignRepository(db *sql.DB) domain.CampaignRepository {
	return &PostgresCampaignRepository{db: db}
}

func (r *PostgresCampaignRepository) Create(ctx context.Context, c *domain.Campaign) error {
	query := `
		INSERT INTO campaigns (tenant_id, title, message_body, external_template_code, media_url, delivery_type, selected_channels, selected_telegram_destination_ids, selected_contact_ids, status, scheduled_at, total_targets)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, created_at, updated_at;
	`
	err := r.db.QueryRowContext(ctx, query,
		c.TenantID, c.Title, c.MessageBody, c.ExternalTemplateCode, c.MediaURL, c.DeliveryType, c.SelectedChannels, c.SelectedTelegramDestinationIDs, c.SelectedContactIDs, c.Status, c.ScheduledAt, c.TotalTargets,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert campaign record: %w", err)
	}
	return nil
}

// UpdateContent replaces the editable content of a campaign. When resetApproval
// is true it also returns the campaign to draft and clears the previous
// submission and approval trail, so edited content is never covered by an
// approval granted for an earlier version.
func (r *PostgresCampaignRepository) UpdateContent(ctx context.Context, tenantID, id string, c *domain.Campaign, resetApproval bool) error {
	query := `
		UPDATE campaigns
		SET title = $1,
		    message_body = $2,
		    media_url = $3,
		    delivery_type = $4,
		    selected_channels = $5,
		    selected_telegram_destination_ids = $6,
		    selected_contact_ids = $7,
		    status = CASE WHEN $8 THEN 'draft' ELSE status END,
		    submitted_by = CASE WHEN $8 THEN NULL ELSE submitted_by END,
		    submitted_at = CASE WHEN $8 THEN NULL ELSE submitted_at END,
		    approved_by = CASE WHEN $8 THEN NULL ELSE approved_by END,
		    approved_at = CASE WHEN $8 THEN NULL ELSE approved_at END,
		    rejection_reason = CASE WHEN $8 THEN NULL ELSE rejection_reason END,
		    updated_at = NOW()
		WHERE id = $9 AND tenant_id = $10
		  AND status IN ('draft', 'rejected', 'pending_approval', 'approved');
	`
	result, err := r.db.ExecContext(ctx, query,
		c.Title, c.MessageBody, c.MediaURL, c.DeliveryType, c.SelectedChannels,
		c.SelectedTelegramDestinationIDs, c.SelectedContactIDs, resetApproval,
		id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to update campaign content: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func (r *PostgresCampaignRepository) ListByTenant(ctx context.Context, tenantID, status string, limit, offset int) ([]*domain.Campaign, error) {
	var query string
	var rows *sql.Rows
	var err error

	if status != "" {
		if status == "scheduled" {
			query = `
				SELECT id, tenant_id, title, message_body, external_template_code, media_url, delivery_type, selected_channels, selected_telegram_destination_ids, selected_contact_ids, status, scheduled_at, total_targets, processed_targets, created_at, updated_at,
				       submitted_by, approved_by, approved_at, rejection_reason,
			       COALESCE((SELECT r.decision FROM campaign_review_events r WHERE r.tenant_id = campaigns.tenant_id AND r.campaign_id = campaigns.id ORDER BY r.created_at DESC LIMIT 1), '') AS latest_review_decision,
			       COALESCE((SELECT r.note FROM campaign_review_events r WHERE r.tenant_id = campaigns.tenant_id AND r.campaign_id = campaigns.id ORDER BY r.created_at DESC LIMIT 1), '') AS latest_review_note
				FROM campaigns
				WHERE tenant_id = $1 AND status = $2
				ORDER BY scheduled_at ASC NULLS LAST, created_at DESC
				LIMIT $3 OFFSET $4;
			`
		} else {
			query = `
				SELECT id, tenant_id, title, message_body, external_template_code, media_url, delivery_type, selected_channels, selected_telegram_destination_ids, selected_contact_ids, status, scheduled_at, total_targets, processed_targets, created_at, updated_at,
				       submitted_by, approved_by, approved_at, rejection_reason,
			       COALESCE((SELECT r.decision FROM campaign_review_events r WHERE r.tenant_id = campaigns.tenant_id AND r.campaign_id = campaigns.id ORDER BY r.created_at DESC LIMIT 1), '') AS latest_review_decision,
			       COALESCE((SELECT r.note FROM campaign_review_events r WHERE r.tenant_id = campaigns.tenant_id AND r.campaign_id = campaigns.id ORDER BY r.created_at DESC LIMIT 1), '') AS latest_review_note
				FROM campaigns
				WHERE tenant_id = $1 AND status = $2
				ORDER BY created_at DESC
				LIMIT $3 OFFSET $4;
			`
		}
		rows, err = r.db.QueryContext(ctx, query, tenantID, status, limit, offset)
	} else {
		query = `
			SELECT id, tenant_id, title, message_body, external_template_code, media_url, delivery_type, selected_channels, selected_telegram_destination_ids, selected_contact_ids, status, scheduled_at, total_targets, processed_targets, created_at, updated_at,
			       submitted_by, approved_by, approved_at, rejection_reason,
			       COALESCE((SELECT r.decision FROM campaign_review_events r WHERE r.tenant_id = campaigns.tenant_id AND r.campaign_id = campaigns.id ORDER BY r.created_at DESC LIMIT 1), '') AS latest_review_decision,
			       COALESCE((SELECT r.note FROM campaign_review_events r WHERE r.tenant_id = campaigns.tenant_id AND r.campaign_id = campaigns.id ORDER BY r.created_at DESC LIMIT 1), '') AS latest_review_note
			FROM campaigns
			WHERE tenant_id = $1
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3;
		`
		rows, err = r.db.QueryContext(ctx, query, tenantID, limit, offset)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to execute campaign list query: %w", err)
	}
	defer rows.Close()

	campaigns := make([]*domain.Campaign, 0, limit)
	for rows.Next() {
		var c domain.Campaign
		var sched sql.NullTime
		var subBy, appBy, rejReason sql.NullString
		var appAt sql.NullTime
		err := rows.Scan(
			&c.ID, &c.TenantID, &c.Title, &c.MessageBody, &c.ExternalTemplateCode, &c.MediaURL, &c.DeliveryType, &c.SelectedChannels, &c.SelectedTelegramDestinationIDs, &c.SelectedContactIDs, &c.Status, &sched, &c.TotalTargets, &c.ProcessedTargets, &c.CreatedAt, &c.UpdatedAt,
			&subBy, &appBy, &appAt, &rejReason, &c.LatestReviewDecision, &c.LatestReviewNote,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row into campaign domain: %w", err)
		}
		if sched.Valid {
			c.ScheduledAt = &sched.Time
		}
		if subBy.Valid {
			c.SubmittedBy = &subBy.String
		}
		if appBy.Valid {
			c.ApprovedBy = &appBy.String
		}
		if appAt.Valid {
			c.ApprovedAt = &appAt.Time
		}
		if rejReason.Valid {
			c.RejectionReason = &rejReason.String
		}
		campaigns = append(campaigns, &c)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading campaign rows stream: %w", err)
	}

	return campaigns, nil
}

func (r *PostgresCampaignRepository) GetByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	query := `
		SELECT id, tenant_id, title, message_body, external_template_code, media_url, delivery_type, selected_channels, selected_telegram_destination_ids, selected_contact_ids, status, scheduled_at, total_targets, processed_targets, created_at, updated_at,
		       submitted_by, submitted_at, approved_by, approved_at, rejection_reason,
			       COALESCE((SELECT r.decision FROM campaign_review_events r WHERE r.tenant_id = campaigns.tenant_id AND r.campaign_id = campaigns.id ORDER BY r.created_at DESC LIMIT 1), '') AS latest_review_decision,
			       COALESCE((SELECT r.note FROM campaign_review_events r WHERE r.tenant_id = campaigns.tenant_id AND r.campaign_id = campaigns.id ORDER BY r.created_at DESC LIMIT 1), '') AS latest_review_note
		FROM campaigns
		WHERE tenant_id = $1 AND id = $2;
	`
	var c domain.Campaign
	var sched, subAt, appAt sql.NullTime
	var subBy, appBy, rejReason sql.NullString
	err := r.db.QueryRowContext(ctx, query, tenantID, id).Scan(
		&c.ID, &c.TenantID, &c.Title, &c.MessageBody, &c.ExternalTemplateCode, &c.MediaURL, &c.DeliveryType, &c.SelectedChannels, &c.SelectedTelegramDestinationIDs, &c.SelectedContactIDs, &c.Status, &sched, &c.TotalTargets, &c.ProcessedTargets, &c.CreatedAt, &c.UpdatedAt,
		&subBy, &subAt, &appBy, &appAt, &rejReason, &c.LatestReviewDecision, &c.LatestReviewNote,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCampaignNotFound
		}
		return nil, fmt.Errorf("failed to fetch campaign row: %w", err)
	}
	if sched.Valid {
		c.ScheduledAt = &sched.Time
	}
	if subBy.Valid {
		c.SubmittedBy = &subBy.String
	}
	if subAt.Valid {
		c.SubmittedAt = &subAt.Time
	}
	if appBy.Valid {
		c.ApprovedBy = &appBy.String
	}
	if appAt.Valid {
		c.ApprovedAt = &appAt.Time
	}
	if rejReason.Valid {
		c.RejectionReason = &rejReason.String
	}
	return &c, nil
}

func (r *PostgresCampaignRepository) FindDueScheduledCampaigns(ctx context.Context) ([]*domain.Campaign, error) {
	query := `
		SELECT id, tenant_id, title, message_body, external_template_code, media_url, delivery_type, 
		       selected_channels, selected_telegram_destination_ids, selected_contact_ids, status, 
		       scheduled_at, total_targets, processed_targets, created_at, updated_at
		FROM campaigns
		WHERE status = 'scheduled' AND scheduled_at <= CURRENT_TIMESTAMP
		ORDER BY scheduled_at ASC
		LIMIT 20;
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query due scheduled campaigns: %w", err)
	}
	defer rows.Close()

	due := make([]*domain.Campaign, 0)
	for rows.Next() {
		var c domain.Campaign
		var sched sql.NullTime
		err := rows.Scan(
			&c.ID, &c.TenantID, &c.Title, &c.MessageBody, &c.ExternalTemplateCode, &c.MediaURL, &c.DeliveryType,
			&c.SelectedChannels, &c.SelectedTelegramDestinationIDs, &c.SelectedContactIDs, &c.Status,
			&sched, &c.TotalTargets, &c.ProcessedTargets, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan due campaign: %w", err)
		}
		if sched.Valid {
			c.ScheduledAt = &sched.Time
		}
		due = append(due, &c)
	}
	return due, nil
}

func (r *PostgresCampaignRepository) CancelScheduledCampaign(ctx context.Context, tenantID, id string) error {
	query := `
		UPDATE campaigns
		SET status = 'draft', scheduled_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $1 AND id = $2 AND status = 'scheduled';
	`
	res, err := r.db.ExecContext(ctx, query, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to cancel scheduled campaign: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("campaign is not in scheduled state or does not exist")
	}
	return nil
}

func (r *PostgresCampaignRepository) UpdateStatus(ctx context.Context, tenantID, id string, status string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tx for status transition: %w", err)
	}
	defer tx.Rollback()

	query := `
		UPDATE campaigns 
		SET status = $1, updated_at = CURRENT_TIMESTAMP 
		WHERE tenant_id = $2 AND id = $3;
	`
	_, err = tx.ExecContext(ctx, query, status, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to transition campaign status state: %w", err)
	}
	return tx.Commit()
}

// SetDispatching atomically marks the campaign as 'processing' and records total_targets.
// Called once when all tasks are queued to NATS — NOT when they're delivered.
func (r *PostgresCampaignRepository) SetDispatching(ctx context.Context, tenantID, id string, totalTargets int) error {
	query := `
		UPDATE campaigns
		SET status = 'processing', total_targets = $1, processed_targets = 0, updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $2 AND id = $3;
	`
	_, err := r.db.ExecContext(ctx, query, totalTargets, tenantID, id)
	return err
}

func (r *PostgresCampaignRepository) RecordDeliveryResult(ctx context.Context, res *contracts.TargetDeliveryResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	auditQuery := `
		INSERT INTO campaign_deliveries (
			campaign_id, contact_id, target_type, platform, routing_value, first_name,
			status, error_message, failure_code, retryable, attempt_count,
			last_attempt_at, provider_message_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1, CURRENT_TIMESTAMP, $11)
		ON CONFLICT (campaign_id, target_type, routing_value) DO UPDATE SET
			status = EXCLUDED.status,
			error_message = EXCLUDED.error_message,
			failure_code = EXCLUDED.failure_code,
			retryable = EXCLUDED.retryable,
			attempt_count = campaign_deliveries.attempt_count + 1,
			last_attempt_at = CURRENT_TIMESTAMP,
			provider_message_id = COALESCE(EXCLUDED.provider_message_id, campaign_deliveries.provider_message_id),
			contact_id = COALESCE(campaign_deliveries.contact_id, EXCLUDED.contact_id),
			first_name = COALESCE(NULLIF(EXCLUDED.first_name, ''), campaign_deliveries.first_name);
	`
	_, err = tx.ExecContext(ctx, auditQuery,
		res.CampaignID,
		nullableContactID(res),
		normalizedTargetType(res.TargetType),
		res.Platform,
		res.RoutingValue,
		res.FirstName,
		res.Status,
		res.ErrorMessage,
		res.FailureCode,
		res.Retryable,
		res.ProviderMessageID,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert delivery audit record: %w", err)
	}

	// processed_targets is derived from distinct delivery rows, so retries cannot
	// inflate it. A campaign only completes once every target has reported and no
	// retry is still in flight.
	counterQuery := `
		UPDATE campaigns c
		SET processed_targets = s.total, updated_at = CURRENT_TIMESTAMP,
		    status = CASE
		        WHEN c.status = 'processing' AND c.total_targets > 0
		             AND s.total >= c.total_targets AND s.pending = 0 THEN 'completed'
		        ELSE c.status
		    END
		FROM (
		    SELECT COUNT(*) AS total,
		           COUNT(*) FILTER (WHERE status = 'queued') AS pending
		    FROM campaign_deliveries
		    WHERE campaign_id = $1
		) s
		WHERE c.id = $1;
	`
	_, err = tx.ExecContext(ctx, counterQuery, res.CampaignID)
	if err != nil {
		return fmt.Errorf("failed to recompute campaign progress: %w", err)
	}

	return tx.Commit()
}

func (r *PostgresCampaignRepository) GetCampaignStats(ctx context.Context, tenantID, campaignID string) (*domain.CampaignStats, error) {
	var tID, status string
	var totalTargets, processedTargets int
	headerQuery := "SELECT tenant_id, status, total_targets, processed_targets FROM campaigns WHERE id = $1"
	headerArgs := []interface{}{campaignID}
	if tenantID != "" {
		headerQuery += " AND tenant_id = $2"
		headerArgs = append(headerArgs, tenantID)
	}
	err := r.db.QueryRowContext(ctx, headerQuery, headerArgs...).Scan(&tID, &status, &totalTargets, &processedTargets)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCampaignNotFound
		}
		return nil, fmt.Errorf("failed to fetch campaign stats header: %w", err)
	}

	query := `
		SELECT status, COUNT(*) 
		FROM campaign_deliveries 
		WHERE campaign_id = $1 
		GROUP BY status;
	`
	rows, err := r.db.QueryContext(ctx, query, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{"sent": 0, "delivered": 0, "failed": 0}
	for rows.Next() {
		var st string
		var cnt int
		if err := rows.Scan(&st, &cnt); err != nil {
			return nil, err
		}
		counts[st] = cnt
	}

	progress := 0.0
	if totalTargets > 0 {
		progress = (float64(processedTargets) / float64(totalTargets)) * 100.0
		if progress > 100.0 {
			progress = 100.0
		}
	} else if status == "completed" {
		progress = 100.0
	}

	// Aggregate failure breakdown and retryability
	breakdownQuery := `
		SELECT COALESCE(failure_code, 'unknown'), retryable, COUNT(*) 
		FROM campaign_deliveries 
		WHERE campaign_id = $1 AND status = 'failed'
		GROUP BY failure_code, retryable;
	`
	bRows, err := r.db.QueryContext(ctx, breakdownQuery, campaignID)
	retryableFailed := 0
	terminalFailed := 0
	failureBreakdown := make(map[string]int)
	if err == nil {
		defer bRows.Close()
		for bRows.Next() {
			var code string
			var retryable bool
			var cnt int
			if err := bRows.Scan(&code, &retryable, &cnt); err == nil {
				failureBreakdown[code] = cnt
				if retryable {
					retryableFailed += cnt
				} else {
					terminalFailed += cnt
				}
			}
		}
	}

	return &domain.CampaignStats{
		CampaignID:       campaignID,
		TenantID:         tID,
		Status:           status,
		TotalTargets:     totalTargets,
		ProcessedTargets: processedTargets,
		Sent:             counts["sent"],
		Delivered:        counts["delivered"],
		Failed:           counts["failed"],
		ProgressPercent:  math.Round(progress*10) / 10,
		RetryableFailed:  retryableFailed,
		TerminalFailed:   terminalFailed,
		FailureBreakdown: failureBreakdown,
	}, nil
}

func (r *PostgresCampaignRepository) GetCampaignHealth(ctx context.Context, tenantID, campaignID string) (*domain.CampaignHealth, error) {
	stats, err := r.GetCampaignStats(ctx, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	breakdown := stats.FailureBreakdown
	if breakdown == nil {
		breakdown = make(map[string]int)
	}
	return &domain.CampaignHealth{
		CampaignID:       stats.CampaignID,
		Status:           stats.Status,
		TotalTargets:     stats.TotalTargets,
		ProcessedTargets: stats.ProcessedTargets,
		Delivered:        stats.Delivered,
		Sent:             stats.Sent,
		Failed:           stats.Failed,
		RetryableFailed:  stats.RetryableFailed,
		TerminalFailed:   stats.TerminalFailed,
		FailureBreakdown: breakdown,
	}, nil
}

func (r *PostgresCampaignRepository) ListDeliveriesByCampaign(
	ctx context.Context,
	tenantID, campaignID string,
	status, failureCode string,
	retryable *bool,
	limit, offset int,
) ([]*domain.CampaignDelivery, error) {
	if tenantID != "" {
		var exists bool
		err := r.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM campaigns WHERE id = $1 AND tenant_id = $2)", campaignID, tenantID).Scan(&exists)
		if err != nil || !exists {
			return nil, ErrCampaignNotFound
		}
	}

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(`
		SELECT d.id, d.campaign_id, d.contact_id, d.target_type, d.platform, d.routing_value,
		       d.first_name, d.status, d.error_message, d.failure_code, d.retryable, d.attempt_count,
		       d.last_attempt_at, d.provider_message_id, d.created_at
		FROM campaign_deliveries d
		WHERE d.campaign_id = $1
	`)
	args := []interface{}{campaignID}
	argIdx := 2

	if status != "" {
		queryBuilder.WriteString(fmt.Sprintf(" AND d.status = $%d", argIdx))
		args = append(args, status)
		argIdx++
	}
	if failureCode != "" {
		queryBuilder.WriteString(fmt.Sprintf(" AND d.failure_code = $%d", argIdx))
		args = append(args, failureCode)
		argIdx++
	}
	if retryable != nil {
		queryBuilder.WriteString(fmt.Sprintf(" AND d.retryable = $%d", argIdx))
		args = append(args, *retryable)
		argIdx++
	}

	queryBuilder.WriteString(fmt.Sprintf(" ORDER BY d.created_at DESC LIMIT $%d OFFSET $%d;", argIdx, argIdx+1))
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, queryBuilder.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query campaign deliveries: %w", err)
	}
	defer rows.Close()

	deliveries := make([]*domain.CampaignDelivery, 0, limit)
	for rows.Next() {
		var d domain.CampaignDelivery
		var contactID, errMsg, failCode, provMsgID sql.NullString
		var lastAttempt sql.NullTime
		if err := rows.Scan(
			&d.ID, &d.CampaignID, &contactID, &d.TargetType, &d.Platform, &d.RoutingValue,
			&d.FirstName, &d.Status, &errMsg, &failCode, &d.Retryable, &d.AttemptCount,
			&lastAttempt, &provMsgID, &d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan campaign delivery: %w", err)
		}
		if contactID.Valid {
			d.ContactID = &contactID.String
		}
		if errMsg.Valid {
			d.ErrorMessage = &errMsg.String
		}
		if failCode.Valid {
			d.FailureCode = &failCode.String
		}
		if lastAttempt.Valid {
			d.LastAttemptAt = &lastAttempt.Time
		}
		if provMsgID.Valid {
			d.ProviderMessageID = &provMsgID.String
		}
		deliveries = append(deliveries, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deliveries, nil
}

// ClaimDeliveriesForRetry atomically moves retryable failed rows to 'queued' and
// returns exactly the rows this caller claimed. Concurrent retries of the same row
// lose the race and receive nothing, so a message can never be published twice.
func (r *PostgresCampaignRepository) ClaimDeliveriesForRetry(ctx context.Context, tenantID, campaignID string, ids []string) ([]*domain.CampaignDelivery, error) {
	var idFilter interface{}
	if len(ids) > 0 {
		idFilter = pq.Array(ids)
	}
	var tenantFilter interface{}
	if tenantID != "" {
		tenantFilter = tenantID
	}

	query := `
		UPDATE campaign_deliveries d
		SET status = 'queued', last_attempt_at = CURRENT_TIMESTAMP
		FROM campaigns c
		WHERE d.campaign_id = c.id
		  AND d.campaign_id = $1
		  AND ($2::uuid IS NULL OR c.tenant_id = $2::uuid)
		  AND d.status = 'failed'
		  AND d.retryable = TRUE
		  AND ($3::uuid[] IS NULL OR d.id = ANY($3::uuid[]))
		RETURNING d.id, d.campaign_id, d.contact_id, d.target_type, d.platform, d.routing_value,
		          d.first_name, d.attempt_count, d.created_at;
	`
	rows, err := r.db.QueryContext(ctx, query, campaignID, tenantFilter, idFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to claim deliveries for retry: %w", err)
	}
	defer rows.Close()

	var deliveries []*domain.CampaignDelivery
	for rows.Next() {
		var d domain.CampaignDelivery
		var contactID sql.NullString
		if err := rows.Scan(
			&d.ID, &d.CampaignID, &contactID, &d.TargetType, &d.Platform, &d.RoutingValue,
			&d.FirstName, &d.AttemptCount, &d.CreatedAt,
		); err != nil {
			return nil, err
		}
		if contactID.Valid {
			d.ContactID = &contactID.String
		}
		d.Status = "queued"
		deliveries = append(deliveries, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deliveries, nil
}

// ReleaseDeliveryClaims returns claimed rows to 'failed' when publishing failed,
// so the operator can try again instead of leaving them stuck in 'queued'.
func (r *PostgresCampaignRepository) ReleaseDeliveryClaims(ctx context.Context, campaignID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE campaign_deliveries
		SET status = 'failed'
		WHERE campaign_id = $1 AND id = ANY($2::uuid[]) AND status = 'queued'
	`, campaignID, pq.Array(ids))
	return err
}

func (r *PostgresCampaignRepository) SetScheduled(ctx context.Context, tenantID, id string, scheduledAt time.Time) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE campaigns SET status = 'scheduled', scheduled_at = $1, updated_at = NOW() WHERE id = $2 AND tenant_id = $3 AND status IN ('draft', 'approved')`,
		scheduledAt, id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark campaign as scheduled: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func (r *PostgresCampaignRepository) PauseCampaign(ctx context.Context, tenantID, id string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE campaigns
		SET status = 'paused', updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $1 AND id = $2 AND status = 'processing'
	`, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to pause campaign: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func (r *PostgresCampaignRepository) ResumeCampaign(ctx context.Context, tenantID, id string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE campaigns
		SET status = 'processing', updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $1 AND id = $2 AND status = 'paused'
	`, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to resume campaign: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func (r *PostgresCampaignRepository) CancelCampaign(ctx context.Context, tenantID, id string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE campaigns
		SET status = 'cancelled', scheduled_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE tenant_id = $1 AND id = $2
		  AND status IN ('draft', 'scheduled', 'pending_approval', 'approved')
	`, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to cancel campaign: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func (r *PostgresCampaignRepository) DeleteDraft(ctx context.Context, tenantID, id string) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM campaigns
		WHERE tenant_id = $1 AND id = $2 AND status = 'draft'
	`, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to delete draft: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func nullableContactID(res *contracts.TargetDeliveryResult) interface{} {
	if res.TargetType == "telegram_destination" || res.ContactID == "" {
		return nil
	}
	return res.ContactID
}

func normalizedTargetType(targetType string) string {
	if targetType == "telegram_destination" {
		return targetType
	}
	return "contact"
}

func (r *PostgresCampaignRepository) CountDeliveriesSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	query := `
		SELECT count(*)
		FROM campaign_deliveries d
		JOIN campaigns c ON d.campaign_id = c.id
		WHERE c.tenant_id = $1 AND d.created_at >= $2
	`
	var count sql.NullInt64
	err := r.db.QueryRowContext(ctx, query, tenantID, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count deliveries since timestamp: %w", err)
	}
	if count.Valid {
		return int(count.Int64), nil
	}
	return 0, nil
}

func (r *PostgresCampaignRepository) SubmitForApproval(ctx context.Context, tenantID, id, submittedBy string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE campaigns 
		 SET status = 'pending_approval', submitted_by = $1, submitted_at = NOW(), rejection_reason = NULL, updated_at = NOW() 
		 WHERE id = $2 AND tenant_id = $3 AND status IN ('draft', 'rejected')`,
		submittedBy, id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to submit campaign for approval: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func (r *PostgresCampaignRepository) ApproveCampaign(ctx context.Context, tenantID, id, approvedBy string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE campaigns 
		 SET status = 'approved', approved_by = $1, approved_at = NOW(), updated_at = NOW() 
		 WHERE id = $2 AND tenant_id = $3 AND status = 'pending_approval'`,
		approvedBy, id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to approve campaign: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func (r *PostgresCampaignRepository) RejectCampaign(ctx context.Context, tenantID, id, reason string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE campaigns 
		 SET status = 'rejected', rejection_reason = $1, updated_at = NOW() 
		 WHERE id = $2 AND tenant_id = $3 AND status = 'pending_approval'`,
		reason, id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to reject campaign: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrCampaignNotFound
	}
	return nil
}

func (r *PostgresCampaignRepository) GetApprovalSetting(ctx context.Context, tenantID string) (bool, error) {
	var req bool
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(require_campaign_approval, false) FROM tenant_settings WHERE tenant_id = $1`, tenantID).Scan(&req)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return req, nil
}

// CountAudienceConsent aggregates the consent state of the audience a campaign
// would target. Filtering mirrors TriggerDispatch exactly: an explicit contact
// list wins, and channels narrow the result when set.
func (r *PostgresCampaignRepository) CountAudienceConsent(ctx context.Context, tenantID string, contactIDs, channels []string) (*domain.AudienceConsentCounts, error) {
	var idFilter, channelFilter interface{}
	if len(contactIDs) > 0 {
		idFilter = pq.Array(contactIDs)
	}
	if len(channels) > 0 {
		channelFilter = pq.Array(channels)
	}

	var counts domain.AudienceConsentCounts
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE c.status = 'active'),
		       COUNT(*) FILTER (WHERE c.status = 'opted_out'),
		       COUNT(*) FILTER (WHERE NOT EXISTS (
		           SELECT 1 FROM consent_events e WHERE e.contact_id = c.id
		       ))
		FROM contacts c
		WHERE c.tenant_id = $1
		  AND ($2::uuid[] IS NULL OR c.id = ANY($2::uuid[]))
		  AND ($3::text[] IS NULL OR c.channel = ANY($3::text[]));
	`, tenantID, idFilter, channelFilter).Scan(
		&counts.Total, &counts.Active, &counts.OptedOut, &counts.NoConsent,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to summarize audience consent: %w", err)
	}
	return &counts, nil
}

// StreamAudienceConsent yields one audit row per contact with its latest
// consent decision, so large exports never buffer in memory.
func (r *PostgresCampaignRepository) StreamAudienceConsent(ctx context.Context, tenantID string, contactIDs, channels []string, yield func(*domain.AudienceConsentRow) error) error {
	var idFilter, channelFilter interface{}
	if len(contactIDs) > 0 {
		idFilter = pq.Array(contactIDs)
	}
	if len(channels) > 0 {
		channelFilter = pq.Array(channels)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.first_name, c.channel, c.routing_value, c.status,
		       (SELECT COUNT(*) FROM consent_events e WHERE e.contact_id = c.id),
		       COALESCE(l.event_type, ''), COALESCE(l.source, ''),
		       l.occurred_at, COALESCE(l.evidence, '{}')
		FROM contacts c
		LEFT JOIN LATERAL (
		    SELECT e.event_type, e.source, e.occurred_at, e.evidence::text AS evidence
		    FROM consent_events e
		    WHERE e.contact_id = c.id
		    ORDER BY e.occurred_at DESC, e.recorded_at DESC
		    LIMIT 1
		) l ON TRUE
		WHERE c.tenant_id = $1
		  AND ($2::uuid[] IS NULL OR c.id = ANY($2::uuid[]))
		  AND ($3::text[] IS NULL OR c.channel = ANY($3::text[]))
		ORDER BY c.first_name, c.id;
	`, tenantID, idFilter, channelFilter)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var row domain.AudienceConsentRow
		if err := rows.Scan(
			&row.ContactID, &row.FirstName, &row.Channel, &row.RoutingValue, &row.Status,
			&row.EventCount, &row.LastEventType, &row.LastSource,
			&row.LastOccurredAt, &row.LastEvidence,
		); err != nil {
			return err
		}
		if err := yield(&row); err != nil {
			return err
		}
	}
	return rows.Err()
}
