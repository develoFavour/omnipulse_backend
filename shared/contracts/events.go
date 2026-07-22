package contracts

// TargetDispatchTask represents the thin event payload transmitted over NATS JetStream.
type TargetDispatchTask struct {
	CampaignID     string  `json:"campaign_id"`
	TenantID       string  `json:"tenant_id"`
	ContactID      string  `json:"contact_id"`
	TargetType     string  `json:"target_type"` // contact or telegram_destination
	FirstName      string  `json:"first_name"`
	TargetPlatform string  `json:"target_platform"` // whatsapp, telegram, x
	RoutingValue   string  `json:"routing_value"`   // Phone number, ChatID, or Handle
	MessageBody    string  `json:"message_body"`
	MediaURL       *string `json:"media_url,omitempty"`
}

// TargetDeliveryResult represents the event payload streaming back from workers
type TargetDeliveryResult struct {
	CampaignID   string  `json:"campaign_id"`
	ContactID    string  `json:"contact_id"`
	TargetType   string  `json:"target_type"`
	Platform     string  `json:"platform"`
	RoutingValue string  `json:"routing_value"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message,omitempty"`
}
