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
	ProviderAvailable       bool              `json:"provider_available"`
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

type IngestDocumentRequest struct {
	Content string `json:"content"`
	Title   string `json:"title,omitempty"`
}

type KnowledgeIngestionStatus string

const (
	KnowledgeIngestionQueued     KnowledgeIngestionStatus = "queued"
	KnowledgeIngestionProcessing KnowledgeIngestionStatus = "processing"
	KnowledgeIngestionReady      KnowledgeIngestionStatus = "ready"
	KnowledgeIngestionFailed     KnowledgeIngestionStatus = "failed"
)

type KnowledgeIngestionJob struct {
	ID           string                   `json:"id"`
	TenantID     string                   `json:"tenant_id,omitempty"`
	Filename     string                   `json:"filename"`
	ContentType  string                   `json:"content_type,omitempty"`
	Status       KnowledgeIngestionStatus `json:"status"`
	Items        []AIKnowledgeItem        `json:"items,omitempty"`
	ItemCount    int                      `json:"item_count,omitempty"`
	ErrorMessage string                   `json:"error_message,omitempty"`
	CreatedAt    time.Time                `json:"created_at"`
	StartedAt    *time.Time               `json:"started_at,omitempty"`
	CompletedAt  *time.Time               `json:"completed_at,omitempty"`
	SourceData   []byte                   `json:"-"`
}

type KnowledgePage struct {
	Items  []AIKnowledgeItem `json:"items"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

type KnowledgeIngestionPage struct {
	Jobs   []KnowledgeIngestionJob `json:"jobs"`
	Total  int                     `json:"total"`
	Limit  int                     `json:"limit"`
	Offset int                     `json:"offset"`
}

type ExtractedLeadInfo struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type AIAgentRepository interface {
	GetConfig(ctx context.Context, tenantID string) (*AIAgentConfig, error)
	SaveConfig(ctx context.Context, cfg *AIAgentConfig) error
	SetEnabled(ctx context.Context, tenantID string, enabled bool) error
	ListKnowledgeItems(ctx context.Context, tenantID string) ([]AIKnowledgeItem, error)
	ListKnowledgeItemsPage(ctx context.Context, tenantID, search, category string, limit, offset int) (*KnowledgePage, error)
	CreateKnowledgeItem(ctx context.Context, item *AIKnowledgeItem) error
	BulkCreateKnowledgeItems(ctx context.Context, tenantID string, items []AIKnowledgeItem) error
	UpdateKnowledgeItem(ctx context.Context, item *AIKnowledgeItem) error
	DeleteKnowledgeItem(ctx context.Context, tenantID, itemID string) error

	CreateKnowledgeIngestionJob(ctx context.Context, tenantID, filename, contentType string, sourceData []byte) (*KnowledgeIngestionJob, error)
	GetKnowledgeIngestionJob(ctx context.Context, tenantID, jobID string) (*KnowledgeIngestionJob, error)
	ListKnowledgeIngestionJobs(ctx context.Context, tenantID, status string, limit, offset int) (*KnowledgeIngestionPage, error)
	ClaimKnowledgeIngestionJob(ctx context.Context, jobID string) (*KnowledgeIngestionJob, error)
	CompleteKnowledgeIngestionJob(ctx context.Context, jobID string, items []AIKnowledgeItem) error
	FailKnowledgeIngestionJob(ctx context.Context, jobID, message string) error

	// Conversation AI state tracking
	SetConversationAIState(ctx context.Context, tenantID, conversationID string, state string, pausedUntil *time.Time, needsHuman bool) error
	GetConversationAIState(ctx context.Context, tenantID, conversationID string) (state string, pausedUntil *time.Time, needsHuman bool, err error)
}
