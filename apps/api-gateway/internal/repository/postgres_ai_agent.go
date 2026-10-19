package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresAIAgentRepository struct {
	db *sql.DB
}

func NewPostgresAIAgentRepository(db *sql.DB) domain.AIAgentRepository {
	return &PostgresAIAgentRepository{db: db}
}

func (r *PostgresAIAgentRepository) GetConfig(ctx context.Context, tenantID string) (*domain.AIAgentConfig, error) {
	query := `
		SELECT 
			tenant_id, is_enabled, operational_mode, idle_timeout_seconds,
			business_name, business_summary, tone, language, custom_system_prompt,
			COALESCE(business_hours_start::text, '09:00:00'),
			COALESCE(business_hours_end::text, '17:00:00'),
			timezone, work_days,
			human_handoff_message, unsupported_query_message, confidence_threshold,
			created_at, updated_at
		FROM ai_agent_configs
		WHERE tenant_id = $1
	`

	var cfg domain.AIAgentConfig
	var modeStr string
	var workDays []int64

	err := r.db.QueryRowContext(ctx, query, tenantID).Scan(
		&cfg.TenantID, &cfg.IsEnabled, &modeStr, &cfg.IdleTimeoutSeconds,
		&cfg.BusinessName, &cfg.BusinessSummary, &cfg.Tone, &cfg.Language, &cfg.CustomSystemPrompt,
		&cfg.BusinessHoursStart, &cfg.BusinessHoursEnd, &cfg.Timezone, pq.Array(&workDays),
		&cfg.HumanHandoffMessage, &cfg.UnsupportedQueryMessage, &cfg.ConfidenceThreshold,
		&cfg.CreatedAt, &cfg.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			// Return a sensible unconfigured default (opted-out by default)
			return &domain.AIAgentConfig{
				TenantID:                tenantID,
				IsEnabled:               false,
				OperationalMode:         domain.AIOperationalModeAfterHours,
				IdleTimeoutSeconds:      180,
				BusinessName:            "",
				BusinessSummary:         "",
				Tone:                    "professional",
				Language:                "en",
				CustomSystemPrompt:      "",
				BusinessHoursStart:      "09:00:00",
				BusinessHoursEnd:        "17:00:00",
				Timezone:                "Africa/Lagos",
				WorkDays:                []int{1, 2, 3, 4, 5},
				HumanHandoffMessage:     "I am transferring you to one of our human team members who will assist you shortly.",
				UnsupportedQueryMessage: "I do not have that exact information right now, but I have alerted our team to follow up with you directly.",
				ConfidenceThreshold:     0.70,
				CreatedAt:               time.Now(),
				UpdatedAt:               time.Now(),
			}, nil
		}
		return nil, fmt.Errorf("failed to get AI config: %w", err)
	}

	cfg.OperationalMode = domain.AIOperationalMode(modeStr)
	cfg.WorkDays = make([]int, len(workDays))
	for i, v := range workDays {
		cfg.WorkDays[i] = int(v)
	}

	return &cfg, nil
}

func (r *PostgresAIAgentRepository) SaveConfig(ctx context.Context, cfg *domain.AIAgentConfig) error {
	workDaysInt64 := make([]int64, len(cfg.WorkDays))
	for i, v := range cfg.WorkDays {
		workDaysInt64[i] = int64(v)
	}

	query := `
		INSERT INTO ai_agent_configs (
			tenant_id, is_enabled, operational_mode, idle_timeout_seconds,
			business_name, business_summary, tone, language, custom_system_prompt,
			business_hours_start, business_hours_end, timezone, work_days,
			human_handoff_message, unsupported_query_message, confidence_threshold,
			updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9,
			$10::time, $11::time, $12, $13,
			$14, $15, $16,
			NOW()
		)
		ON CONFLICT (tenant_id) DO UPDATE SET
			is_enabled = EXCLUDED.is_enabled,
			operational_mode = EXCLUDED.operational_mode,
			idle_timeout_seconds = EXCLUDED.idle_timeout_seconds,
			business_name = EXCLUDED.business_name,
			business_summary = EXCLUDED.business_summary,
			tone = EXCLUDED.tone,
			language = EXCLUDED.language,
			custom_system_prompt = EXCLUDED.custom_system_prompt,
			business_hours_start = EXCLUDED.business_hours_start,
			business_hours_end = EXCLUDED.business_hours_end,
			timezone = EXCLUDED.timezone,
			work_days = EXCLUDED.work_days,
			human_handoff_message = EXCLUDED.human_handoff_message,
			unsupported_query_message = EXCLUDED.unsupported_query_message,
			confidence_threshold = EXCLUDED.confidence_threshold,
			updated_at = NOW()
	`

	_, err := r.db.ExecContext(
		ctx, query,
		cfg.TenantID, cfg.IsEnabled, string(cfg.OperationalMode), cfg.IdleTimeoutSeconds,
		cfg.BusinessName, cfg.BusinessSummary, cfg.Tone, cfg.Language, cfg.CustomSystemPrompt,
		cfg.BusinessHoursStart, cfg.BusinessHoursEnd, cfg.Timezone, pq.Array(workDaysInt64),
		cfg.HumanHandoffMessage, cfg.UnsupportedQueryMessage, cfg.ConfidenceThreshold,
	)
	if err != nil {
		return fmt.Errorf("failed to save AI agent config: %w", err)
	}
	return nil
}

func (r *PostgresAIAgentRepository) ListKnowledgeItems(ctx context.Context, tenantID string) ([]domain.AIKnowledgeItem, error) {
	query := `
		SELECT id, tenant_id, category, question_or_topic, answer_or_content, is_active, created_at, updated_at
		FROM ai_knowledge_items
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list knowledge items: %w", err)
	}
	defer rows.Close()

	var items []domain.AIKnowledgeItem
	for rows.Next() {
		var item domain.AIKnowledgeItem
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.Category,
			&item.QuestionOrTopic, &item.AnswerOrContent,
			&item.IsActive, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *PostgresAIAgentRepository) CreateKnowledgeItem(ctx context.Context, item *domain.AIKnowledgeItem) error {
	query := `
		INSERT INTO ai_knowledge_items (tenant_id, category, question_or_topic, answer_or_content, is_active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		item.TenantID, item.Category, item.QuestionOrTopic, item.AnswerOrContent, item.IsActive,
	).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create knowledge item: %w", err)
	}
	return nil
}

func (r *PostgresAIAgentRepository) BulkCreateKnowledgeItems(ctx context.Context, tenantID string, items []domain.AIKnowledgeItem) error {
	if len(items) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO ai_knowledge_items (tenant_id, category, question_or_topic, answer_or_content, is_active)
		VALUES ($1, $2, $3, $4, $5)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare bulk insert statement: %w", err)
	}
	defer stmt.Close()

	for _, it := range items {
		cat := it.Category
		if cat == "" {
			cat = "faq"
		}
		if _, err := stmt.ExecContext(ctx, tenantID, cat, it.QuestionOrTopic, it.AnswerOrContent, true); err != nil {
			return fmt.Errorf("failed to insert knowledge item: %w", err)
		}
	}

	return tx.Commit()
}

func (r *PostgresAIAgentRepository) CreateKnowledgeIngestionJob(ctx context.Context, tenantID, filename, contentType string, sourceData []byte) (*domain.KnowledgeIngestionJob, error) {
	job := &domain.KnowledgeIngestionJob{
		TenantID:    tenantID,
		Filename:    filename,
		ContentType: contentType,
		Status:      domain.KnowledgeIngestionQueued,
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO ai_knowledge_ingestion_jobs
			(tenant_id, filename, content_type, source_data, status)
		VALUES ($1, $2, $3, $4, 'queued')
		RETURNING id, created_at
	`, tenantID, filename, contentType, sourceData).Scan(&job.ID, &job.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create knowledge ingestion job: %w", err)
	}
	return job, nil
}

func (r *PostgresAIAgentRepository) GetKnowledgeIngestionJob(ctx context.Context, tenantID, jobID string) (*domain.KnowledgeIngestionJob, error) {
	job := &domain.KnowledgeIngestionJob{}
	var status string
	var itemsJSON []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, filename, content_type, status, extracted_items,
		       COALESCE(error_message, ''), created_at, started_at, completed_at
		FROM ai_knowledge_ingestion_jobs
		WHERE id = $1 AND tenant_id = $2
	`, jobID, tenantID).Scan(
		&job.ID, &job.TenantID, &job.Filename, &job.ContentType, &status, &itemsJSON,
		&job.ErrorMessage, &job.CreatedAt, &job.StartedAt, &job.CompletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("knowledge ingestion job not found")
		}
		return nil, fmt.Errorf("failed to get knowledge ingestion job: %w", err)
	}
	job.Status = domain.KnowledgeIngestionStatus(status)
	if len(itemsJSON) > 0 {
		if err := json.Unmarshal(itemsJSON, &job.Items); err != nil {
			return nil, fmt.Errorf("failed to decode knowledge ingestion results: %w", err)
		}
	}
	return job, nil
}

func (r *PostgresAIAgentRepository) ClaimKnowledgeIngestionJob(ctx context.Context, jobID string) (*domain.KnowledgeIngestionJob, error) {
	job := &domain.KnowledgeIngestionJob{}
	var status string
	err := r.db.QueryRowContext(ctx, `
		UPDATE ai_knowledge_ingestion_jobs
		SET status = 'processing', started_at = NOW(), attempts = attempts + 1
		WHERE id = $1
		  AND attempts < 5
		  AND (status = 'queued' OR (status = 'processing' AND started_at < NOW() - INTERVAL '10 minutes'))
		RETURNING id, tenant_id, filename, content_type, source_data, created_at, started_at
	`, jobID).Scan(
		&job.ID, &job.TenantID, &job.Filename, &job.ContentType, &job.SourceData,
		&job.CreatedAt, &job.StartedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to claim knowledge ingestion job: %w", err)
	}
	status = string(domain.KnowledgeIngestionProcessing)
	job.Status = domain.KnowledgeIngestionStatus(status)
	return job, nil
}

func (r *PostgresAIAgentRepository) CompleteKnowledgeIngestionJob(ctx context.Context, jobID string, items []domain.AIKnowledgeItem) error {
	itemsJSON, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("failed to encode knowledge ingestion results: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE ai_knowledge_ingestion_jobs
		SET status = 'ready', extracted_items = $2, source_data = NULL,
		    error_message = NULL, completed_at = NOW()
		WHERE id = $1
	`, jobID, itemsJSON)
	if err != nil {
		return fmt.Errorf("failed to complete knowledge ingestion job: %w", err)
	}
	return nil
}

func (r *PostgresAIAgentRepository) FailKnowledgeIngestionJob(ctx context.Context, jobID, message string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ai_knowledge_ingestion_jobs
		SET status = 'failed', error_message = $2, source_data = NULL, completed_at = NOW()
		WHERE id = $1
	`, jobID, message)
	if err != nil {
		return fmt.Errorf("failed to mark knowledge ingestion job as failed: %w", err)
	}
	return nil
}

func (r *PostgresAIAgentRepository) UpdateKnowledgeItem(ctx context.Context, item *domain.AIKnowledgeItem) error {
	query := `
		UPDATE ai_knowledge_items
		SET category = $1, question_or_topic = $2, answer_or_content = $3, is_active = $4, updated_at = NOW()
		WHERE id = $5 AND tenant_id = $6
		RETURNING updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		item.Category, item.QuestionOrTopic, item.AnswerOrContent, item.IsActive, item.ID, item.TenantID,
	).Scan(&item.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to update knowledge item: %w", err)
	}
	return nil
}

func (r *PostgresAIAgentRepository) DeleteKnowledgeItem(ctx context.Context, tenantID, itemID string) error {
	query := `DELETE FROM ai_knowledge_items WHERE id = $1 AND tenant_id = $2`
	res, err := r.db.ExecContext(ctx, query, itemID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete knowledge item: %w", err)
	}
	rowsAff, _ := res.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("knowledge item not found")
	}
	return nil
}

func (r *PostgresAIAgentRepository) SetConversationAIState(ctx context.Context, tenantID, conversationID string, state string, pausedUntil *time.Time, needsHuman bool) error {
	query := `
		UPDATE conversations
		SET ai_state = $1, ai_paused_until = $2, needs_human_attention = $3, updated_at = NOW()
		WHERE id = $4 AND tenant_id = $5
	`
	_, err := r.db.ExecContext(ctx, query, state, pausedUntil, needsHuman, conversationID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to update conversation AI state: %w", err)
	}
	return nil
}

func (r *PostgresAIAgentRepository) GetConversationAIState(ctx context.Context, tenantID, conversationID string) (state string, pausedUntil *time.Time, needsHuman bool, err error) {
	query := `
		SELECT COALESCE(ai_state, 'active'), ai_paused_until, COALESCE(needs_human_attention, false)
		FROM conversations
		WHERE id = $1 AND tenant_id = $2
	`
	err = r.db.QueryRowContext(ctx, query, conversationID, tenantID).Scan(&state, &pausedUntil, &needsHuman)
	if err != nil {
		if err == sql.ErrNoRows {
			return "active", nil, false, nil
		}
		return "", nil, false, fmt.Errorf("failed to get conversation AI state: %w", err)
	}
	return state, pausedUntil, needsHuman, nil
}
