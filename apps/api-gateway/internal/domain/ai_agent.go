package domain

import (
	"context"
	"time"
)

type AIOperationalMode string

const (
	AIOperationalModeAlwaysOn     AIOperationalMode = "always_on"
	AIOperationalModeAfterHours   AIOperationalMode = "after_hours"
	AIOperationalModeIdleFallback AIOperationalMode = "idle_fallback"
	AIOperationalModeCopilotOnly  AIOperationalMode = "copilot_only"
)

type AIAgentConfig struct {
	TenantID                string            `json:"tenant_id"`
	IsEnabled               bool              `json:"is_enabled"`
	OperationalMode         AIOperationalMode `json:"operational_mode"`
	IdleTimeoutSeconds      int               `json:"idle_timeout_seconds"`
	BusinessName            string            `json:"business_name"`
	BusinessSummary         string            `json:"business_summary"`
	Tone                    string            `json:"tone"`
	Language                string            `json:"language"`
	CustomSystemPrompt      string            `json:"custom_system_prompt"`
	BusinessHoursStart      string            `json:"business_hours_start"`
	BusinessHoursEnd        string            `json:"business_hours_end"`
	Timezone                string            `json:"timezone"`
	WorkDays                []int             `json:"work_days"`
	HumanHandoffMessage     string            `json:"human_handoff_message"`
	UnsupportedQueryMessage string            `json:"unsupported_query_message"`
	ConfidenceThreshold     float64           `json:"confidence_threshold"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
}

type AIKnowledgeItem struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	Category        string    `json:"category"` // "faq", "pricing", "product", "policy", "hours"
	QuestionOrTopic string    `json:"question_or_topic"`
	AnswerOrContent string    `json:"answer_or_content"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AIAgentRepository interface {
	GetConfig(ctx context.Context, tenantID string) (*AIAgentConfig, error)
	SaveConfig(ctx context.Context, cfg *AIAgentConfig) error
	ListKnowledgeItems(ctx context.Context, tenantID string) ([]AIKnowledgeItem, error)
	CreateKnowledgeItem(ctx context.Context, item *AIKnowledgeItem) error
	UpdateKnowledgeItem(ctx context.Context, item *AIKnowledgeItem) error
	DeleteKnowledgeItem(ctx context.Context, tenantID, itemID string) error

	// Conversation AI state tracking
	SetConversationAIState(ctx context.Context, tenantID, conversationID string, state string, pausedUntil *time.Time, needsHuman bool) error
	GetConversationAIState(ctx context.Context, tenantID, conversationID string) (state string, pausedUntil *time.Time, needsHuman bool, err error)
}
