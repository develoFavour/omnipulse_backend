package domain

import (
	"errors"
	"time"
)

// ErrWorkspaceNotFound is returned when an admin drill-down targets a tenant
// that no longer exists.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// The admin surface is cross-tenant and read-only. Every number here is derived
// from existing rows; there is no event pipeline, so growth is reconstructed
// from created_at timestamps and is directional rather than exact.

type AdminOverview struct {
	WindowDays  int                   `json:"window_days"`
	GeneratedAt time.Time             `json:"generated_at"`
	Workspaces  AdminWorkspaceSummary `json:"workspaces"`
	Users       AdminUserSummary      `json:"users"`
	Messages    AdminMessageSummary   `json:"messages"`
	Channels    AdminChannelSummary   `json:"channels"`
	Revenue     AdminRevenueSummary   `json:"revenue"`
	Attention   AdminAttention        `json:"attention"`
}

type AdminWorkspaceSummary struct {
	Total     int `json:"total"`
	New       int `json:"new_in_window"`
	Previous  int `json:"new_previous_window"`
	Onboarded int `json:"onboarded"`
}

type AdminUserSummary struct {
	Total    int `json:"total"`
	New      int `json:"new_in_window"`
	Previous int `json:"new_previous_window"`
}

type AdminMessageSummary struct {
	Successful         int     `json:"successful"`
	Failed             int     `json:"failed"`
	DeliveryRate       float64 `json:"delivery_rate"`
	PreviousSuccessful int     `json:"previous_successful"`
}

type AdminChannelSummary struct {
	ActiveConnections     int            `json:"active_connections"`
	WorkspacesWithChannel int            `json:"workspaces_with_channel"`
	ByPlatform            map[string]int `json:"by_platform"`
}

type AdminCurrencyRevenue struct {
	Currency string  `json:"currency"`
	MRR      float64 `json:"monthly_recurring"`
	Paying   int     `json:"paying_workspaces"`
}

// AdminRevenueSummary deliberately reports recurring revenue per currency:
// USD and NGN subscriptions cannot be summed into one trustworthy figure.
type AdminRevenueSummary struct {
	ByCurrency []AdminCurrencyRevenue `json:"by_currency"`
	ByPlan     map[string]int         `json:"by_plan"`
	ByStatus   map[string]int         `json:"by_status"`
}

type AdminAttention struct {
	PastDueSubscriptions int `json:"past_due_subscriptions"`
	StuckCampaigns       int `json:"stuck_campaigns"`
	DegradedChannels     int `json:"degraded_channels"`
}

type AdminGrowth struct {
	WindowDays      int                `json:"window_days"`
	GeneratedAt     time.Time          `json:"generated_at"`
	WorkspacesTotal int                `json:"workspaces_total"`
	Series          []AdminGrowthPoint `json:"series"`
	Funnel          []AdminFunnelStep  `json:"funnel"`
}

type AdminGrowthPoint struct {
	Date          string `json:"date"`
	NewWorkspaces int    `json:"new_workspaces"`
	NewUsers      int    `json:"new_users"`
}

type AdminFunnelStep struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Workspaces int    `json:"workspaces"`
}

type AdminRevenue struct {
	WindowDays    int                 `json:"window_days"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Summary       AdminRevenueSummary `json:"summary"`
	Slices        []AdminPlanSlice    `json:"slices"`
	Renewals      []AdminRenewal      `json:"renewals_due"`
	Cancellations []AdminCancellation `json:"recent_cancellations"`
}

type AdminPlanSlice struct {
	PlanTier          string  `json:"plan_tier"`
	Status            string  `json:"status"`
	Currency          string  `json:"currency"`
	Workspaces        int     `json:"workspaces"`
	MonthlyEquivalent float64 `json:"monthly_equivalent"`
}

type AdminRenewal struct {
	TenantID     string     `json:"tenant_id"`
	CompanyName  string     `json:"company_name"`
	PlanTier     string     `json:"plan_tier"`
	Currency     string     `json:"currency"`
	BillingCycle string     `json:"billing_cycle"`
	Amount       int        `json:"amount"`
	RenewsAt     *time.Time `json:"renews_at"`
}

type AdminCancellation struct {
	TenantID    string    `json:"tenant_id"`
	CompanyName string    `json:"company_name"`
	PlanTier    string    `json:"plan_tier"`
	Currency    string    `json:"currency"`
	Amount      int       `json:"amount"`
	CancelledAt time.Time `json:"cancelled_at"`
}

type AdminHealth struct {
	GeneratedAt    time.Time            `json:"generated_at"`
	Status         string               `json:"status"`
	Heartbeats     []AdminHeartbeat     `json:"heartbeats"`
	Consumers      []AdminConsumer      `json:"consumers"`
	Queue          []AdminQueueDepth    `json:"queue"`
	StuckCampaigns []AdminStuckCampaign `json:"stuck_campaigns"`
	Failures       []AdminFailureBucket `json:"failures_by_platform"`
	FailureCodes   []AdminFailureCode   `json:"top_failure_codes"`
	Channels       []AdminChannelCount  `json:"channels"`
	PastDue        []AdminPastDue       `json:"past_due"`
}

type AdminHeartbeat struct {
	Service           string `json:"service"`
	Healthy           bool   `json:"healthy"`
	LastBeat          string `json:"last_beat"`
	AgeSeconds        int64  `json:"age_seconds"`
	StaleAfterSeconds int64  `json:"stale_after_seconds"`
}

type AdminConsumer struct {
	Service    string `json:"service"`
	Exists     bool   `json:"exists"`
	Pending    uint64 `json:"pending"`
	AckPending int    `json:"ack_pending"`
	Delivered  uint64 `json:"delivered"`
}

type AdminQueueDepth struct {
	Status     string     `json:"status"`
	Dispatches int        `json:"dispatches"`
	OldestAt   *time.Time `json:"oldest_at"`
}

type AdminStuckCampaign struct {
	CampaignID   string    `json:"campaign_id"`
	TenantID     string    `json:"tenant_id"`
	CompanyName  string    `json:"company_name"`
	Title        string    `json:"title"`
	TotalTargets int       `json:"total_targets"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type AdminFailureBucket struct {
	Platform string `json:"platform"`
	Failed   int    `json:"failed"`
	Total    int    `json:"total"`
	// DeliveryRate is percent of deliveries that did not fail in the window.
	DeliveryRate float64 `json:"delivery_rate"`
}

type AdminFailureCode struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type AdminChannelCount struct {
	Platform string `json:"platform"`
	Status   string `json:"status"`
	Health   string `json:"health"`
	Count    int    `json:"count"`
}

type AdminPastDue struct {
	TenantID    string    `json:"tenant_id"`
	CompanyName string    `json:"company_name"`
	PlanTier    string    `json:"plan_tier"`
	Currency    string    `json:"currency"`
	Amount      int       `json:"amount"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AdminWorkspaceFilter struct {
	Query     string
	Page      int
	PageSize  int
	Sort      string
	Direction string
}

type AdminWorkspaceRow struct {
	ID                  string    `json:"id"`
	CompanyName         string    `json:"company_name"`
	CreatedAt           time.Time `json:"created_at"`
	OnboardingCompleted bool      `json:"onboarding_completed"`
	PlanTier            string    `json:"plan_tier"`
	SubscriptionStatus  string    `json:"subscription_status"`
	Currency            string    `json:"currency"`
	Amount              int       `json:"amount"`
	Members             int       `json:"members"`
	Contacts            int       `json:"contacts"`
	Campaigns           int       `json:"campaigns"`
	ActiveChannels      int       `json:"active_channels"`
	LastActivityAt      time.Time `json:"last_activity_at"`
}

type AdminWorkspaceList struct {
	Data       []AdminWorkspaceRow `json:"data"`
	Total      int                 `json:"total"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
	TotalPages int                 `json:"total_pages"`
}

type AdminWorkspaceDetail struct {
	Workspace       AdminWorkspaceRow    `json:"workspace"`
	Subscription    *AdminSubscription   `json:"subscription"`
	Members         []AdminMemberRow     `json:"members"`
	Channels        []AdminChannelRow    `json:"channels"`
	RecentCampaigns []AdminCampaignRow   `json:"recent_campaigns"`
	Deliveries      AdminDeliverySummary `json:"deliveries"`
	Consent         AdminConsentSummary  `json:"consent"`
}

type AdminSubscription struct {
	PlanTier            string     `json:"plan_tier"`
	BillingCycle        string     `json:"billing_cycle"`
	Currency            string     `json:"currency"`
	Amount              int        `json:"amount"`
	Status              string     `json:"status"`
	Provider            string     `json:"provider"`
	CurrentPeriodStart  *time.Time `json:"current_period_start"`
	CurrentPeriodEnd    *time.Time `json:"current_period_end"`
	MonthlyMessageLimit int        `json:"monthly_message_limit"`
	ContactsLimit       int        `json:"contacts_limit"`
	ChannelsLimit       int        `json:"channels_limit"`
}

type AdminMemberRow struct {
	UserID   string    `json:"user_id"`
	Email    string    `json:"email"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type AdminChannelRow struct {
	Platform         string     `json:"platform"`
	Status           string     `json:"status"`
	Health           string     `json:"health"`
	AccountIdentity  string     `json:"account_identity"`
	LastHeartbeatAt  *time.Time `json:"last_heartbeat_at"`
	LastSuccessfulAt *time.Time `json:"last_successful_at"`
	LastError        string     `json:"last_error"`
	CreatedAt        time.Time  `json:"created_at"`
}

type AdminCampaignRow struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Status           string    `json:"status"`
	TotalTargets     int       `json:"total_targets"`
	ProcessedTargets int       `json:"processed_targets"`
	CreatedAt        time.Time `json:"created_at"`
}

type AdminDeliverySummary struct {
	Total        int     `json:"total"`
	Delivered    int     `json:"delivered"`
	Failed       int     `json:"failed"`
	DeliveryRate float64 `json:"delivery_rate"`
}

type AdminConsentSummary struct {
	Contacts      int `json:"contacts"`
	OptedOut      int `json:"opted_out"`
	GrantedEvents int `json:"granted_events"`
	RevokedEvents int `json:"revoked_events"`
}
