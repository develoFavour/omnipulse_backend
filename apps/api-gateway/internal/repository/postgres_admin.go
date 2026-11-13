package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

// PostgresAdminRepository serves the cross-tenant platform dashboard. Every
// method here is read-only and tenant-agnostic; access control lives in
// handler.PlatformAdminGate, never in this layer.
type PostgresAdminRepository struct {
	db *sql.DB
}

func NewPostgresAdminRepository(db *sql.DB) *PostgresAdminRepository {
	return &PostgresAdminRepository{db: db}
}

func (r *PostgresAdminRepository) GetOverview(ctx context.Context, days int) (*domain.AdminOverview, error) {
	overview := &domain.AdminOverview{
		WindowDays:  days,
		GeneratedAt: time.Now().UTC(),
	}

	var wsTotal, wsNew, wsPrevious, wsOnboarded int
	var usersTotal, usersNew, usersPrevious int
	var successful, failed, previousSuccessful int
	var activeConnections, workspacesWithChannel int
	var pastDue, stuckCampaigns, degradedChannels int

	err := r.db.QueryRowContext(ctx, `
		WITH win AS (SELECT make_interval(days => $1::int) AS span)
		SELECT
			(SELECT COUNT(*) FROM tenants),
			(SELECT COUNT(*) FROM tenants t, win w WHERE t.created_at >= NOW() - w.span),
			(SELECT COUNT(*) FROM tenants t, win w WHERE t.created_at >= NOW() - (w.span * 2) AND t.created_at < NOW() - w.span),
			(SELECT COUNT(*) FROM tenants WHERE COALESCE(onboarding_completed, FALSE)),
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM users u, win w WHERE u.created_at >= NOW() - w.span),
			(SELECT COUNT(*) FROM users u, win w WHERE u.created_at >= NOW() - (w.span * 2) AND u.created_at < NOW() - w.span),
			(SELECT COUNT(*) FROM campaign_deliveries d, win w WHERE d.status IN ('sent', 'delivered') AND d.created_at >= NOW() - w.span),
			(SELECT COUNT(*) FROM campaign_deliveries d, win w WHERE d.status = 'failed' AND d.created_at >= NOW() - w.span),
			(SELECT COUNT(*) FROM campaign_deliveries d, win w WHERE d.status IN ('sent', 'delivered') AND d.created_at >= NOW() - (w.span * 2) AND d.created_at < NOW() - w.span),
			(SELECT COUNT(*) FROM tenant_channels WHERE status = 'active'),
			(SELECT COUNT(DISTINCT tenant_id) FROM tenant_channels WHERE status = 'active'),
			(SELECT COUNT(*) FROM subscriptions WHERE status = 'past_due'),
			(SELECT COUNT(*) FROM campaigns WHERE status = 'processing' AND updated_at < NOW() - INTERVAL '15 minutes'),
			(SELECT COUNT(*) FROM tenant_channels
			  WHERE status <> 'active' OR COALESCE(health_status, 'unknown') NOT IN ('healthy', 'unknown'));
	`, days).Scan(
		&wsTotal, &wsNew, &wsPrevious, &wsOnboarded,
		&usersTotal, &usersNew, &usersPrevious,
		&successful, &failed, &previousSuccessful,
		&activeConnections, &workspacesWithChannel,
		&pastDue, &stuckCampaigns, &degradedChannels,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate admin overview: %w", err)
	}

	overview.Workspaces = domain.AdminWorkspaceSummary{Total: wsTotal, New: wsNew, Previous: wsPrevious, Onboarded: wsOnboarded}
	overview.Users = domain.AdminUserSummary{Total: usersTotal, New: usersNew, Previous: usersPrevious}
	overview.Messages = domain.AdminMessageSummary{
		Successful:         successful,
		Failed:             failed,
		DeliveryRate:       percentage(successful, successful+failed),
		PreviousSuccessful: previousSuccessful,
	}
	overview.Channels = domain.AdminChannelSummary{
		ActiveConnections:     activeConnections,
		WorkspacesWithChannel: workspacesWithChannel,
		ByPlatform:            map[string]int{},
	}
	overview.Attention = domain.AdminAttention{
		PastDueSubscriptions: pastDue,
		StuckCampaigns:       stuckCampaigns,
		DegradedChannels:     degradedChannels,
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT platform_name, COUNT(*) FROM tenant_channels
		WHERE status = 'active'
		GROUP BY platform_name
		ORDER BY platform_name;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to count active channels by platform: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var platform string
		var count int
		if err := rows.Scan(&platform, &count); err != nil {
			return nil, fmt.Errorf("failed to scan channel platform count: %w", err)
		}
		overview.Channels.ByPlatform[platform] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read channel platform counts: %w", err)
	}

	summary, _, err := r.revenueSummary(ctx)
	if err != nil {
		return nil, err
	}
	overview.Revenue = summary

	return overview, nil
}

func (r *PostgresAdminRepository) GetGrowth(ctx context.Context, days int) (*domain.AdminGrowth, error) {
	growth := &domain.AdminGrowth{
		WindowDays:  days,
		GeneratedAt: time.Now().UTC(),
	}

	rows, err := r.db.QueryContext(ctx, `
		WITH span AS (
			SELECT generate_series(
				(NOW() - make_interval(days => $1::int))::date,
				NOW()::date,
				INTERVAL '1 day'
			)::date AS day
		)
		SELECT
			s.day,
			(SELECT COUNT(*) FROM tenants t WHERE t.created_at >= s.day AND t.created_at < s.day + INTERVAL '1 day'),
			(SELECT COUNT(*) FROM users u WHERE u.created_at >= s.day AND u.created_at < s.day + INTERVAL '1 day')
		FROM span s
		ORDER BY s.day;
	`, days)
	if err != nil {
		return nil, fmt.Errorf("failed to build admin growth series: %w", err)
	}
	defer rows.Close()

	growth.Series = make([]domain.AdminGrowthPoint, 0, days+1)
	for rows.Next() {
		var day time.Time
		var newWorkspaces, newUsers int
		if err := rows.Scan(&day, &newWorkspaces, &newUsers); err != nil {
			return nil, fmt.Errorf("failed to scan growth point: %w", err)
		}
		growth.Series = append(growth.Series, domain.AdminGrowthPoint{
			Date:          day.Format("2006-01-02"),
			NewWorkspaces: newWorkspaces,
			NewUsers:      newUsers,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read growth series: %w", err)
	}

	var total, withChannel, withContacts, withCampaign, withReview, withSend, withRepeat int
	err = r.db.QueryRowContext(ctx, `
		WITH sent AS (
			SELECT c.tenant_id, c.id AS campaign_id, MIN(d.created_at) AS first_sent
			FROM campaigns c
			JOIN campaign_deliveries d ON d.campaign_id = c.id
			GROUP BY c.tenant_id, c.id
		), ranked AS (
			SELECT tenant_id, first_sent,
			       ROW_NUMBER() OVER (PARTITION BY tenant_id ORDER BY first_sent) AS rn
			FROM sent
		)
		SELECT
			(SELECT COUNT(*) FROM tenants),
			(SELECT COUNT(DISTINCT tenant_id) FROM tenant_channels WHERE status = 'active'),
			(SELECT COUNT(DISTINCT tenant_id) FROM contacts),
			(SELECT COUNT(DISTINCT tenant_id) FROM campaigns),
			(SELECT COUNT(DISTINCT tenant_id) FROM campaigns WHERE submitted_at IS NOT NULL),
			(SELECT COUNT(DISTINCT c.tenant_id) FROM campaign_deliveries d JOIN campaigns c ON c.id = d.campaign_id),
			(SELECT COUNT(*) FROM ranked f
			   JOIN ranked s ON s.tenant_id = f.tenant_id AND s.rn = 2
			  WHERE f.rn = 1 AND s.first_sent <= f.first_sent + INTERVAL '14 days');
	`).Scan(&total, &withChannel, &withContacts, &withCampaign, &withReview, &withSend, &withRepeat)
	if err != nil {
		return nil, fmt.Errorf("failed to compute activation funnel: %w", err)
	}

	growth.WorkspacesTotal = total
	growth.Funnel = []domain.AdminFunnelStep{
		{Key: "workspaces", Label: "Workspaces created", Workspaces: total},
		{Key: "channel", Label: "Connected a channel", Workspaces: withChannel},
		{Key: "contacts", Label: "Imported a recipient", Workspaces: withContacts},
		{Key: "campaign", Label: "Drafted a campaign", Workspaces: withCampaign},
		{Key: "review", Label: "Submitted for brand review", Workspaces: withReview},
		{Key: "sent", Label: "Sent to recipients", Workspaces: withSend},
		{Key: "repeat", Label: "Second campaign within 14 days", Workspaces: withRepeat},
	}
	return growth, nil
}

func (r *PostgresAdminRepository) GetRevenue(ctx context.Context, days int) (*domain.AdminRevenue, error) {
	revenue := &domain.AdminRevenue{
		WindowDays:  days,
		GeneratedAt: time.Now().UTC(),
	}

	summary, slices, err := r.revenueSummary(ctx)
	if err != nil {
		return nil, err
	}
	revenue.Summary = summary
	revenue.Slices = slices

	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.company_name, s.plan_tier, COALESCE(NULLIF(TRIM(s.currency), ''), 'UNSET'),
		       s.billing_cycle, s.amount, s.current_period_end
		FROM subscriptions s
		JOIN tenants t ON t.id = s.tenant_id
		WHERE s.status IN ('active', 'trialing')
		  AND s.current_period_end IS NOT NULL
		  AND s.current_period_end BETWEEN NOW() AND NOW() + make_interval(days => $1::int)
		ORDER BY s.current_period_end ASC
		LIMIT 25;
	`, days)
	if err != nil {
		return nil, fmt.Errorf("failed to list renewals: %w", err)
	}
	defer rows.Close()

	revenue.Renewals = make([]domain.AdminRenewal, 0)
	for rows.Next() {
		var row domain.AdminRenewal
		var renewsAt *time.Time
		if err := rows.Scan(&row.TenantID, &row.CompanyName, &row.PlanTier, &row.Currency, &row.BillingCycle, &row.Amount, &renewsAt); err != nil {
			return nil, fmt.Errorf("failed to scan renewal: %w", err)
		}
		row.RenewsAt = renewsAt
		revenue.Renewals = append(revenue.Renewals, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read renewals: %w", err)
	}
	rows.Close()

	cancelled, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.company_name, s.plan_tier, COALESCE(NULLIF(TRIM(s.currency), ''), 'UNSET'),
		       s.amount, s.updated_at
		FROM subscriptions s
		JOIN tenants t ON t.id = s.tenant_id
		WHERE s.status = 'canceled'
		  AND s.updated_at >= NOW() - make_interval(days => $1::int)
		ORDER BY s.updated_at DESC
		LIMIT 25;
	`, days)
	if err != nil {
		return nil, fmt.Errorf("failed to list cancellations: %w", err)
	}
	defer cancelled.Close()

	revenue.Cancellations = make([]domain.AdminCancellation, 0)
	for cancelled.Next() {
		var row domain.AdminCancellation
		if err := cancelled.Scan(&row.TenantID, &row.CompanyName, &row.PlanTier, &row.Currency, &row.Amount, &row.CancelledAt); err != nil {
			return nil, fmt.Errorf("failed to scan cancellation: %w", err)
		}
		revenue.Cancellations = append(revenue.Cancellations, row)
	}
	if err := cancelled.Err(); err != nil {
		return nil, fmt.Errorf("failed to read cancellations: %w", err)
	}

	return revenue, nil
}

type revenueRow struct {
	Currency string
	PlanTier string
	Status   string
	Cycle    string
	Count    int
	Monthly  float64
}

func (r *PostgresAdminRepository) revenueSummary(ctx context.Context) (domain.AdminRevenueSummary, []domain.AdminPlanSlice, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(currency), ''), 'UNSET'),
		       COALESCE(NULLIF(TRIM(plan_tier), ''), 'free'),
		       COALESCE(NULLIF(TRIM(status), ''), 'unknown'),
		       COALESCE(NULLIF(TRIM(billing_cycle), ''), 'monthly'),
		       COUNT(*),
		       COALESCE(SUM(CASE WHEN billing_cycle = 'yearly' THEN amount / 12.0 ELSE amount END), 0)::float8
		FROM subscriptions
		GROUP BY 1, 2, 3, 4
		ORDER BY 1, 2, 3, 4;
	`)
	if err != nil {
		return domain.AdminRevenueSummary{}, nil, fmt.Errorf("failed to aggregate revenue: %w", err)
	}
	defer rows.Close()

	summary := domain.AdminRevenueSummary{
		ByPlan:   map[string]int{},
		ByStatus: map[string]int{},
	}
	byCurrency := map[string]*domain.AdminCurrencyRevenue{}
	slices := make([]domain.AdminPlanSlice, 0)

	for rows.Next() {
		var row revenueRow
		if err := rows.Scan(&row.Currency, &row.PlanTier, &row.Status, &row.Cycle, &row.Count, &row.Monthly); err != nil {
			return domain.AdminRevenueSummary{}, nil, fmt.Errorf("failed to scan revenue row: %w", err)
		}

		summary.ByPlan[row.PlanTier] += row.Count
		summary.ByStatus[row.Status] += row.Count

		current, ok := byCurrency[row.Currency]
		if !ok {
			current = &domain.AdminCurrencyRevenue{Currency: row.Currency}
			byCurrency[row.Currency] = current
		}
		// Only active, non-zero subscriptions count as recurring revenue;
		// trial and past_due rows would inflate it.
		if row.Status == "active" && row.Monthly > 0 {
			current.MRR += round2(row.Monthly)
			current.Paying += row.Count
		}

		slices = append(slices, domain.AdminPlanSlice{
			PlanTier:          row.PlanTier,
			Status:            row.Status,
			Currency:          row.Currency,
			Workspaces:        row.Count,
			MonthlyEquivalent: round2(row.Monthly),
		})
	}
	if err := rows.Err(); err != nil {
		return domain.AdminRevenueSummary{}, nil, fmt.Errorf("failed to read revenue rows: %w", err)
	}

	for _, currency := range byCurrency {
		currency.MRR = round2(currency.MRR)
		summary.ByCurrency = append(summary.ByCurrency, *currency)
	}
	sort.Slice(summary.ByCurrency, func(i, j int) bool {
		return summary.ByCurrency[i].Currency < summary.ByCurrency[j].Currency
	})

	return summary, slices, nil
}

func (r *PostgresAdminRepository) GetHealth(ctx context.Context, stuckAfter time.Duration) (*domain.AdminHealth, error) {
	health := &domain.AdminHealth{
		GeneratedAt:    time.Now().UTC(),
		Status:         "healthy",
		Heartbeats:     []domain.AdminHeartbeat{},
		Consumers:      []domain.AdminConsumer{},
		Queue:          []domain.AdminQueueDepth{},
		StuckCampaigns: []domain.AdminStuckCampaign{},
		Failures:       []domain.AdminFailureBucket{},
		FailureCodes:   []domain.AdminFailureCode{},
		Channels:       []domain.AdminChannelCount{},
		PastDue:        []domain.AdminPastDue{},
	}

	queueRows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*), MIN(created_at)
		FROM campaign_dispatches
		WHERE status IN ('queued', 'in_flight', 'rate_limited')
		GROUP BY status
		ORDER BY status;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to read dispatch queue depth: %w", err)
	}
	for queueRows.Next() {
		var row domain.AdminQueueDepth
		var oldest *time.Time
		if err := queueRows.Scan(&row.Status, &row.Dispatches, &oldest); err != nil {
			queueRows.Close()
			return nil, fmt.Errorf("failed to scan queue depth: %w", err)
		}
		row.OldestAt = oldest
		health.Queue = append(health.Queue, row)
	}
	queueRows.Close()
	if err := queueRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read dispatch queue depth: %w", err)
	}

	stuckRows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.tenant_id, COALESCE(t.company_name, ''), c.title, c.total_targets, c.updated_at
		FROM campaigns c
		LEFT JOIN tenants t ON t.id = c.tenant_id
		WHERE c.status = 'processing'
		  AND c.updated_at < NOW() - make_interval(secs => $1::int)
		  AND NOT EXISTS (
		      SELECT 1 FROM campaign_deliveries cd
		      WHERE cd.campaign_id = c.id AND cd.status = 'delivered'
		  )
		ORDER BY c.updated_at ASC
		LIMIT 10;
	`, int(stuckAfter.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("failed to scan stuck campaigns: %w", err)
	}
	for stuckRows.Next() {
		var row domain.AdminStuckCampaign
		if err := stuckRows.Scan(&row.CampaignID, &row.TenantID, &row.CompanyName, &row.Title, &row.TotalTargets, &row.UpdatedAt); err != nil {
			stuckRows.Close()
			return nil, fmt.Errorf("failed to scan stuck campaign: %w", err)
		}
		health.StuckCampaigns = append(health.StuckCampaigns, row)
	}
	stuckRows.Close()
	if err := stuckRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read stuck campaigns: %w", err)
	}

	failureRows, err := r.db.QueryContext(ctx, `
		SELECT platform,
		       COUNT(*) FILTER (WHERE status = 'failed'),
		       COUNT(*)
		FROM campaign_deliveries
		WHERE created_at >= NOW() - INTERVAL '24 hours'
		GROUP BY platform
		ORDER BY 2 DESC;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate delivery failures: %w", err)
	}
	for failureRows.Next() {
		var platform string
		var failed, total int
		if err := failureRows.Scan(&platform, &failed, &total); err != nil {
			failureRows.Close()
			return nil, fmt.Errorf("failed to scan delivery failures: %w", err)
		}
		health.Failures = append(health.Failures, domain.AdminFailureBucket{
			Platform:     platform,
			Failed:       failed,
			Total:        total,
			DeliveryRate: percentage(total-failed, total),
		})
	}
	failureRows.Close()
	if err := failureRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read delivery failures: %w", err)
	}

	codeRows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(failure_code), ''), 'unknown') AS code, COUNT(*) AS failures
		FROM campaign_deliveries
		WHERE status = 'failed' AND created_at >= NOW() - INTERVAL '24 hours'
		GROUP BY 1
		ORDER BY 2 DESC
		LIMIT 5;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate failure codes: %w", err)
	}
	for codeRows.Next() {
		var row domain.AdminFailureCode
		if err := codeRows.Scan(&row.Code, &row.Count); err != nil {
			codeRows.Close()
			return nil, fmt.Errorf("failed to scan failure code: %w", err)
		}
		health.FailureCodes = append(health.FailureCodes, row)
	}
	codeRows.Close()
	if err := codeRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read failure codes: %w", err)
	}

	channelRows, err := r.db.QueryContext(ctx, `
		SELECT platform_name, status, COALESCE(NULLIF(TRIM(health_status), ''), 'unknown'), COUNT(*)
		FROM tenant_channels
		GROUP BY 1, 2, 3
		ORDER BY 1, 2, 3;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate channel health: %w", err)
	}
	for channelRows.Next() {
		var row domain.AdminChannelCount
		if err := channelRows.Scan(&row.Platform, &row.Status, &row.Health, &row.Count); err != nil {
			channelRows.Close()
			return nil, fmt.Errorf("failed to scan channel health: %w", err)
		}
		health.Channels = append(health.Channels, row)
	}
	channelRows.Close()
	if err := channelRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read channel health: %w", err)
	}

	pastDueRows, err := r.db.QueryContext(ctx, `
		SELECT t.id, COALESCE(t.company_name, ''), s.plan_tier,
		       COALESCE(NULLIF(TRIM(s.currency), ''), 'UNSET'), s.amount, s.updated_at
		FROM subscriptions s
		JOIN tenants t ON t.id = s.tenant_id
		WHERE s.status = 'past_due'
		ORDER BY s.updated_at ASC
		LIMIT 10;
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list past-due subscriptions: %w", err)
	}
	for pastDueRows.Next() {
		var row domain.AdminPastDue
		if err := pastDueRows.Scan(&row.TenantID, &row.CompanyName, &row.PlanTier, &row.Currency, &row.Amount, &row.UpdatedAt); err != nil {
			pastDueRows.Close()
			return nil, fmt.Errorf("failed to scan past-due subscription: %w", err)
		}
		health.PastDue = append(health.PastDue, row)
	}
	pastDueRows.Close()
	if err := pastDueRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read past-due subscriptions: %w", err)
	}

	return health, nil
}

var adminWorkspaceSortColumns = map[string]string{
	"created_at":    "t.created_at",
	"company_name":  "t.company_name",
	"contacts":      "contact_count",
	"campaigns":     "campaign_count",
	"last_activity": "last_activity_at",
}

func (r *PostgresAdminRepository) ListWorkspaces(ctx context.Context, filter domain.AdminWorkspaceFilter) (*domain.AdminWorkspaceList, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 25
	}
	sortColumn, ok := adminWorkspaceSortColumns[filter.Sort]
	if !ok {
		sortColumn = "last_activity_at"
	}
	direction := "DESC"
	if strings.EqualFold(filter.Direction, "asc") {
		direction = "ASC"
	}

	var total int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tenants t
		WHERE ($1 = '' OR t.company_name ILIKE '%' || $1 || '%');
	`, filter.Query).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count workspaces: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT t.id, COALESCE(t.company_name, ''), t.created_at,
		       COALESCE(t.onboarding_completed, FALSE),
		       COALESCE(NULLIF(TRIM(s.plan_tier), ''), 'free'),
		       COALESCE(NULLIF(TRIM(s.status), ''), 'none'),
		       COALESCE(NULLIF(TRIM(s.currency), ''), 'UNSET'),
		       COALESCE(s.amount, 0),
		       (SELECT COUNT(*) FROM tenant_members m WHERE m.tenant_id = t.id),
		       (SELECT COUNT(*) FROM contacts c WHERE c.tenant_id = t.id) AS contact_count,
		       (SELECT COUNT(*) FROM campaigns c WHERE c.tenant_id = t.id) AS campaign_count,
		       (SELECT COUNT(*) FROM tenant_channels ch WHERE ch.tenant_id = t.id AND ch.status = 'active'),
		       GREATEST(t.updated_at,
		           COALESCE((SELECT MAX(c.created_at) FROM campaigns c WHERE c.tenant_id = t.id), t.updated_at)) AS last_activity_at
		FROM tenants t
		LEFT JOIN subscriptions s ON s.tenant_id = t.id
		WHERE ($1 = '' OR t.company_name ILIKE '%%' || $1 || '%%')
		ORDER BY %s %s
		LIMIT $2 OFFSET $3;
	`, sortColumn, direction)

	rows, err := r.db.QueryContext(ctx, query, filter.Query, filter.PageSize, (filter.Page-1)*filter.PageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}
	defer rows.Close()

	list := &domain.AdminWorkspaceList{
		Data:     make([]domain.AdminWorkspaceRow, 0, filter.PageSize),
		Total:    total,
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}
	for rows.Next() {
		var row domain.AdminWorkspaceRow
		if err := rows.Scan(
			&row.ID, &row.CompanyName, &row.CreatedAt, &row.OnboardingCompleted,
			&row.PlanTier, &row.SubscriptionStatus, &row.Currency, &row.Amount,
			&row.Members, &row.Contacts, &row.Campaigns, &row.ActiveChannels, &row.LastActivityAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan workspace row: %w", err)
		}
		list.Data = append(list.Data, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read workspace rows: %w", err)
	}

	list.TotalPages = (total + filter.PageSize - 1) / filter.PageSize
	return list, nil
}

func (r *PostgresAdminRepository) GetWorkspace(ctx context.Context, tenantID string) (*domain.AdminWorkspaceDetail, error) {
	detail := &domain.AdminWorkspaceDetail{
		Members:         []domain.AdminMemberRow{},
		Channels:        []domain.AdminChannelRow{},
		RecentCampaigns: []domain.AdminCampaignRow{},
	}

	err := r.db.QueryRowContext(ctx, `
		SELECT t.id, COALESCE(t.company_name, ''), t.created_at,
		       COALESCE(t.onboarding_completed, FALSE),
		       COALESCE(NULLIF(TRIM(s.plan_tier), ''), 'free'),
		       COALESCE(NULLIF(TRIM(s.status), ''), 'none'),
		       COALESCE(NULLIF(TRIM(s.currency), ''), 'UNSET'),
		       COALESCE(s.amount, 0),
		       (SELECT COUNT(*) FROM tenant_members m WHERE m.tenant_id = t.id),
		       (SELECT COUNT(*) FROM contacts c WHERE c.tenant_id = t.id),
		       (SELECT COUNT(*) FROM campaigns c WHERE c.tenant_id = t.id),
		       (SELECT COUNT(*) FROM tenant_channels ch WHERE ch.tenant_id = t.id AND ch.status = 'active'),
		       GREATEST(t.updated_at,
		           COALESCE((SELECT MAX(c.created_at) FROM campaigns c WHERE c.tenant_id = t.id), t.updated_at))
		FROM tenants t
		LEFT JOIN subscriptions s ON s.tenant_id = t.id
		WHERE t.id = $1;
	`, tenantID).Scan(
		&detail.Workspace.ID, &detail.Workspace.CompanyName, &detail.Workspace.CreatedAt,
		&detail.Workspace.OnboardingCompleted,
		&detail.Workspace.PlanTier, &detail.Workspace.SubscriptionStatus,
		&detail.Workspace.Currency, &detail.Workspace.Amount,
		&detail.Workspace.Members, &detail.Workspace.Contacts, &detail.Workspace.Campaigns,
		&detail.Workspace.ActiveChannels, &detail.Workspace.LastActivityAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrWorkspaceNotFound
		}
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	var start, end *time.Time
	subscription := &domain.AdminSubscription{}
	err = r.db.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(plan_tier), ''), 'free'),
		       COALESCE(NULLIF(TRIM(billing_cycle), ''), 'monthly'),
		       COALESCE(NULLIF(TRIM(currency), ''), 'UNSET'),
		       COALESCE(amount, 0),
		       COALESCE(NULLIF(TRIM(status), ''), 'none'),
		       COALESCE(NULLIF(TRIM(provider), ''), 'free'),
		       current_period_start, current_period_end,
		       COALESCE(monthly_message_limit, 0),
		       COALESCE(contacts_limit, 0),
		       COALESCE(channels_limit, 0)
		FROM subscriptions
		WHERE tenant_id = $1;
	`, tenantID).Scan(
		&subscription.PlanTier, &subscription.BillingCycle, &subscription.Currency,
		&subscription.Amount, &subscription.Status, &subscription.Provider,
		&start, &end,
		&subscription.MonthlyMessageLimit, &subscription.ContactsLimit, &subscription.ChannelsLimit,
	)
	if err == nil {
		subscription.CurrentPeriodStart = start
		subscription.CurrentPeriodEnd = end
		detail.Subscription = subscription
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to load workspace subscription: %w", err)
	}

	memberRows, err := r.db.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.email, ''),
		       TRIM(CONCAT(COALESCE(u.first_name, ''), ' ', COALESCE(u.last_name, ''))),
		       m.role, m.created_at
		FROM tenant_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.tenant_id = $1
		ORDER BY m.created_at ASC;
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace members: %w", err)
	}
	for memberRows.Next() {
		var row domain.AdminMemberRow
		if err := memberRows.Scan(&row.UserID, &row.Email, &row.Name, &row.Role, &row.JoinedAt); err != nil {
			memberRows.Close()
			return nil, fmt.Errorf("failed to scan member: %w", err)
		}
		detail.Members = append(detail.Members, row)
	}
	memberRows.Close()
	if err := memberRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read workspace members: %w", err)
	}

	channelRows, err := r.db.QueryContext(ctx, `
		SELECT platform_name, status, COALESCE(NULLIF(TRIM(health_status), ''), 'unknown'),
		       COALESCE(account_identity, ''), last_heartbeat_at, last_successful_at,
		       COALESCE(last_error, ''), created_at
		FROM tenant_channels
		WHERE tenant_id = $1
		ORDER BY platform_name;
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace channels: %w", err)
	}
	for channelRows.Next() {
		var row domain.AdminChannelRow
		var heartbeat, success *time.Time
		if err := channelRows.Scan(&row.Platform, &row.Status, &row.Health, &row.AccountIdentity, &heartbeat, &success, &row.LastError, &row.CreatedAt); err != nil {
			channelRows.Close()
			return nil, fmt.Errorf("failed to scan channel: %w", err)
		}
		row.LastHeartbeatAt = heartbeat
		row.LastSuccessfulAt = success
		detail.Channels = append(detail.Channels, row)
	}
	channelRows.Close()
	if err := channelRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read workspace channels: %w", err)
	}

	campaignRows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(title, ''), status, COALESCE(total_targets, 0), COALESCE(processed_targets, 0), created_at
		FROM campaigns
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT 10;
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load recent campaigns: %w", err)
	}
	for campaignRows.Next() {
		var row domain.AdminCampaignRow
		if err := campaignRows.Scan(&row.ID, &row.Title, &row.Status, &row.TotalTargets, &row.ProcessedTargets, &row.CreatedAt); err != nil {
			campaignRows.Close()
			return nil, fmt.Errorf("failed to scan campaign: %w", err)
		}
		detail.RecentCampaigns = append(detail.RecentCampaigns, row)
	}
	campaignRows.Close()
	if err := campaignRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read recent campaigns: %w", err)
	}

	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE d.status = 'delivered'),
		       COUNT(*) FILTER (WHERE d.status = 'failed')
		FROM campaign_deliveries d
		JOIN campaigns c ON c.id = d.campaign_id
		WHERE c.tenant_id = $1 AND d.created_at >= NOW() - INTERVAL '30 days';
	`, tenantID).Scan(&detail.Deliveries.Total, &detail.Deliveries.Delivered, &detail.Deliveries.Failed); err != nil {
		return nil, fmt.Errorf("failed to aggregate workspace deliveries: %w", err)
	}
	detail.Deliveries.DeliveryRate = percentage(detail.Deliveries.Delivered, detail.Deliveries.Total)

	if err := r.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM contacts WHERE tenant_id = $1),
		       (SELECT COUNT(*) FROM contacts WHERE tenant_id = $1 AND status = 'opted_out'),
		       (SELECT COUNT(*) FROM consent_events WHERE tenant_id = $1 AND event_type = 'granted'),
		       (SELECT COUNT(*) FROM consent_events WHERE tenant_id = $1 AND event_type = 'revoked');
	`, tenantID).Scan(
		&detail.Consent.Contacts, &detail.Consent.OptedOut,
		&detail.Consent.GrantedEvents, &detail.Consent.RevokedEvents,
	); err != nil {
		return nil, fmt.Errorf("failed to aggregate workspace consent: %w", err)
	}

	return detail, nil
}

func percentage(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return round2(float64(part) / float64(total) * 100)
}

func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}
