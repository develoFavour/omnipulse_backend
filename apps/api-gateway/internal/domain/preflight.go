package domain

import "time"

type PreflightReason struct {
	Code    string `json:"code"`
	Count   int    `json:"count"`
	Message string `json:"message"`
}

// CampaignPreflight is a read-only projection of what a dispatch would do.
// total_targets is the matched audience; sendable is the subset that will
// actually be published.
type CampaignPreflight struct {
	CampaignID   string `json:"campaign_id"`
	Status       string `json:"status"`
	TotalTargets int    `json:"total_targets"`
	Sendable     int    `json:"sendable"`
	OptedOut     int    `json:"opted_out"`
	NoConsent    int    `json:"no_consent"`
	// QuietHours counts targets that would be deferred outside allowed sending
	// windows. Quiet hours are not configurable yet, so this is always 0.
	QuietHours int `json:"quiet_hours"`
	Blocked    int `json:"blocked"`
	// Unreachable counts Telegram contacts that never messaged the bot. Telegram
	// bots cannot open a chat, so dispatch skips them instead of burning quota on
	// a guaranteed failure.
	Unreachable int `json:"unreachable"`
	// WhatsAppConnected is false when the audience includes WhatsApp recipients
	// but the workspace has no live WhatsApp session. Dispatch is refused.
	WhatsAppConnected bool              `json:"whatsapp_connected"`
	Reasons           []PreflightReason `json:"reasons"`
}

// AudienceReachability is the channel-level readiness of the matched audience.
type AudienceReachability struct {
	WhatsAppActive    int
	TelegramActive    int
	TelegramUnstarted int
}

// AudienceConsentCounts is the aggregate consent state of a campaign audience.
type AudienceConsentCounts struct {
	Total     int
	Active    int
	OptedOut  int
	NoConsent int
}

// AudienceConsentRow is one audit line: the contact plus its most recent
// consent decision. An empty LastEventType means no evidence was ever recorded.
type AudienceConsentRow struct {
	ContactID      string
	FirstName      string
	Channel        string
	RoutingValue   string
	Status         string
	EventCount     int
	LastEventType  string
	LastSource     string
	LastOccurredAt *time.Time
	LastEvidence   string
}
