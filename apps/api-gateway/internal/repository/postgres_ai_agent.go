package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"omnipulse/apps/api-gateway/internal/domain"
)

type EmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
}

type PostgresAIAgentRepository struct {
	db                *sql.DB
	embeddingProvider EmbeddingProvider
}

func NewPostgresAIAgentRepository(db *sql.DB) *PostgresAIAgentRepository {
	return &PostgresAIAgentRepository{db: db}
}

func (r *PostgresAIAgentRepository) SetEmbeddingProvider(provider EmbeddingProvider) {
	r.embeddingProvider = provider
}

func formatVector(vec []float32) string {
	if len(vec) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
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

func (r *PostgresAIAgentRepository) SetEnabled(ctx context.Context, tenantID string, enabled bool) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO ai_agent_configs (tenant_id, is_enabled, operational_mode)
		VALUES ($1, $2, CASE WHEN $2 THEN 'always_on' ELSE 'after_hours' END)
		ON CONFLICT (tenant_id) DO UPDATE
		SET is_enabled = EXCLUDED.is_enabled, updated_at = NOW()
	`, tenantID, enabled)
	if err != nil {
		return fmt.Errorf("failed to update AI assistant consent: %w", err)
	}
	return nil
}

func (r *PostgresAIAgentRepository) ListKnowledgeItems(ctx context.Context, tenantID string) ([]domain.AIKnowledgeItem, error) {
	query := `
		SELECT id, tenant_id, category, question_or_topic, answer_or_content, source_document, source_section, source_excerpt, is_active, created_at, updated_at
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
			&item.SourceDocument, &item.SourceSection, &item.SourceExcerpt,
			&item.IsActive, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
		}
		items = append(items, item)
	}
	return items, nil
}

// SearchKnowledgeItems performs Reciprocal Rank Fusion (RRF) Hybrid Search combining:
// 1. pgvector semantic cosine similarity (<=>) for synonyms, dialects, and intent.
// 2. PostgreSQL tsvector GIN full-text search (ts_rank) for exact keywords, codes, and SKUs.
// If the embedding provider is throttled or fails, it gracefully falls back to tsvector full-text search.
func (r *PostgresAIAgentRepository) SearchKnowledgeItems(ctx context.Context, tenantID, query string, limit int) ([]domain.AIKnowledgeItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []domain.AIKnowledgeItem{}, nil
	}

	// 1. Attempt semantic embedding generation
	var queryEmbedding []float32
	if r.embeddingProvider != nil {
		emb, err := r.embeddingProvider.GenerateEmbedding(ctx, query)
		if err == nil && len(emb) > 0 {
			queryEmbedding = emb
		} else {
			log.Printf("[KnowledgeSearch] Embedding unavailable for query (falling back to lexical full-text): %v\n", err)
		}
	}

	// 2. If embedding is available, run Hybrid Search with Reciprocal Rank Fusion (RRF)
	if len(queryEmbedding) > 0 {
		vecStr := formatVector(queryEmbedding)
		hybridQuery := `
			WITH semantic_search AS (
				SELECT id, 
				       ROW_NUMBER() OVER (ORDER BY embedding <=> $2::vector) as rank_semantic
				FROM ai_knowledge_items
				WHERE tenant_id = $1 AND is_active = TRUE AND embedding IS NOT NULL
				LIMIT 16
			),
			lexical_search AS (
				SELECT id,
				       ROW_NUMBER() OVER (
					       ORDER BY ts_rank(
						       to_tsvector('simple', COALESCE(category, '') || ' ' || question_or_topic || ' ' || answer_or_content),
						       plainto_tsquery('simple', $3)
					       ) DESC, updated_at DESC
				       ) as rank_lexical
				FROM ai_knowledge_items
				WHERE tenant_id = $1 AND is_active = TRUE
				  AND to_tsvector('simple', COALESCE(category, '') || ' ' || question_or_topic || ' ' || answer_or_content)
				      @@ plainto_tsquery('simple', $3)
				LIMIT 16
			)
			SELECT k.id, k.tenant_id, k.category, k.question_or_topic, k.answer_or_content, 
			       k.source_document, k.source_section, k.source_excerpt, k.is_active, k.created_at, k.updated_at
			FROM ai_knowledge_items k
			LEFT JOIN semantic_search s ON k.id = s.id
			LEFT JOIN lexical_search l ON k.id = l.id
			WHERE s.id IS NOT NULL OR l.id IS NOT NULL
			ORDER BY (COALESCE(1.0 / (60 + s.rank_semantic), 0.0) + COALESCE(1.0 / (60 + l.rank_lexical), 0.0)) DESC
			LIMIT $4
		`
		rows, err := r.db.QueryContext(ctx, hybridQuery, tenantID, vecStr, query, limit)
		if err == nil {
			defer rows.Close()
			items := make([]domain.AIKnowledgeItem, 0, limit)
			for rows.Next() {
				var item domain.AIKnowledgeItem
				if err := rows.Scan(&item.ID, &item.TenantID, &item.Category, &item.QuestionOrTopic, &item.AnswerOrContent, &item.SourceDocument, &item.SourceSection, &item.SourceExcerpt, &item.IsActive, &item.CreatedAt, &item.UpdatedAt); err != nil {
					return nil, fmt.Errorf("failed to scan hybrid knowledge item: %w", err)
				}
				items = append(items, item)
			}
			if err := rows.Err(); err == nil && len(items) > 0 {
				log.Printf("[KnowledgeSearch] Hybrid search returned %d items (semantic + lexical)\n", len(items))
				r.backfillKnowledgeItems(ctx, tenantID, &items, limit)
				return items, nil
			}
		} else {
			log.Printf("[KnowledgeSearch] Hybrid query encountered error (%v), falling back to pure full-text\n", err)
		}
	}

	// 3. Fallback: Standard PostgreSQL tsvector full-text search
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, category, question_or_topic, answer_or_content, source_document, source_section, source_excerpt, is_active, created_at, updated_at
		FROM ai_knowledge_items
		WHERE tenant_id = $1
		  AND is_active = TRUE
		  AND to_tsvector('simple', COALESCE(category, '') || ' ' || question_or_topic || ' ' || answer_or_content)
		      @@ plainto_tsquery('simple', $2)
		ORDER BY ts_rank(
			to_tsvector('simple', COALESCE(category, '') || ' ' || question_or_topic || ' ' || answer_or_content),
			plainto_tsquery('simple', $2)
		) DESC, updated_at DESC
		LIMIT $3
	`, tenantID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search knowledge items: %w", err)
	}
	defer rows.Close()

	items := make([]domain.AIKnowledgeItem, 0, limit)
	for rows.Next() {
		var item domain.AIKnowledgeItem
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Category, &item.QuestionOrTopic, &item.AnswerOrContent, &item.SourceDocument, &item.SourceSection, &item.SourceExcerpt, &item.IsActive, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan searched knowledge item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read searched knowledge items: %w", err)
	}
	r.backfillKnowledgeItems(ctx, tenantID, &items, limit)
	return items, nil
}

func (r *PostgresAIAgentRepository) backfillKnowledgeItems(ctx context.Context, tenantID string, items *[]domain.AIKnowledgeItem, limit int) {
	if len(*items) >= limit {
		return
	}
	existingIDs := make(map[string]bool, len(*items))
	for _, it := range *items {
		existingIDs[it.ID] = true
	}
	backfillRows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, category, question_or_topic, answer_or_content, source_document, source_section, source_excerpt, is_active, created_at, updated_at
		FROM ai_knowledge_items
		WHERE tenant_id = $1 AND is_active = TRUE
		ORDER BY 
			CASE 
				WHEN category = 'product' THEN 1 
				WHEN category = 'faq' THEN 2 
				WHEN category = 'pricing' THEN 3
				ELSE 4 
			END,
			created_at ASC
		LIMIT $2
	`, tenantID, limit*2)
	if err != nil {
		return
	}
	defer backfillRows.Close()
	for backfillRows.Next() {
		var it domain.AIKnowledgeItem
		if err := backfillRows.Scan(&it.ID, &it.TenantID, &it.Category, &it.QuestionOrTopic, &it.AnswerOrContent, &it.SourceDocument, &it.SourceSection, &it.SourceExcerpt, &it.IsActive, &it.CreatedAt, &it.UpdatedAt); err == nil {
			if !existingIDs[it.ID] {
				existingIDs[it.ID] = true
				*items = append(*items, it)
				if len(*items) >= limit {
					break
				}
			}
		}
	}
}

func (r *PostgresAIAgentRepository) ListKnowledgeItemsPage(ctx context.Context, tenantID, search, category string, limit, offset int) (*domain.KnowledgePage, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	pattern := "%" + search + "%"
	var total int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ai_knowledge_items
		WHERE tenant_id = $1
		  AND ($2 = '' OR question_or_topic ILIKE $3 OR answer_or_content ILIKE $3)
		  AND ($4 = '' OR category = $4)
	`, tenantID, search, pattern, category).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count knowledge items: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, category, question_or_topic, answer_or_content, source_document, source_section, source_excerpt, is_active, created_at, updated_at
		FROM ai_knowledge_items
		WHERE tenant_id = $1
		  AND ($2 = '' OR question_or_topic ILIKE $3 OR answer_or_content ILIKE $3)
		  AND ($4 = '' OR category = $4)
		ORDER BY created_at DESC
		LIMIT $5 OFFSET $6
	`, tenantID, search, pattern, category, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list knowledge items page: %w", err)
	}
	defer rows.Close()
	items := make([]domain.AIKnowledgeItem, 0, limit)
	for rows.Next() {
		var item domain.AIKnowledgeItem
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Category, &item.QuestionOrTopic, &item.AnswerOrContent, &item.SourceDocument, &item.SourceSection, &item.SourceExcerpt, &item.IsActive, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read knowledge item page: %w", err)
	}
	return &domain.KnowledgePage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (r *PostgresAIAgentRepository) CreateKnowledgeItem(ctx context.Context, item *domain.AIKnowledgeItem) error {
	var vecStr string
	if len(item.Embedding) > 0 {
		vecStr = formatVector(item.Embedding)
	} else if r.embeddingProvider != nil {
		textToEmbed := item.QuestionOrTopic + "\n" + item.AnswerOrContent
		if emb, err := r.embeddingProvider.GenerateEmbedding(ctx, textToEmbed); err == nil && len(emb) > 0 {
			item.Embedding = emb
			vecStr = formatVector(emb)
		}
	}

	query := `
		INSERT INTO ai_knowledge_items (tenant_id, category, question_or_topic, answer_or_content, source_document, source_section, source_excerpt, is_active, embedding)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CASE WHEN $9 != '' THEN $9::vector ELSE NULL END)
		RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		item.TenantID, item.Category, item.QuestionOrTopic, item.AnswerOrContent, item.SourceDocument, item.SourceSection, item.SourceExcerpt, item.IsActive, vecStr,
	).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)

	if err != nil {
		// Fallback without vector column in case host has not applied pgvector column
		fallbackQuery := `
			INSERT INTO ai_knowledge_items (tenant_id, category, question_or_topic, answer_or_content, source_document, source_section, source_excerpt, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, created_at, updated_at
		`
		err = r.db.QueryRowContext(
			ctx, fallbackQuery,
			item.TenantID, item.Category, item.QuestionOrTopic, item.AnswerOrContent, item.SourceDocument, item.SourceSection, item.SourceExcerpt, item.IsActive,
		).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to create knowledge item: %w", err)
		}
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
		INSERT INTO ai_knowledge_items (tenant_id, category, question_or_topic, answer_or_content, source_document, source_section, source_excerpt, is_active, embedding)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CASE WHEN $9 != '' THEN $9::vector ELSE NULL END)
	`)
	useFallback := false
	if err != nil {
		useFallback = true
		stmt, err = tx.PrepareContext(ctx, `
			INSERT INTO ai_knowledge_items (tenant_id, category, question_or_topic, answer_or_content, source_document, source_section, source_excerpt, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`)
		if err != nil {
			return fmt.Errorf("failed to prepare bulk insert statement: %w", err)
		}
	}
	defer stmt.Close()

	for _, it := range items {
		cat := it.Category
		if cat == "" {
			cat = "faq"
		}
		if !useFallback {
			var vecStr string
			if len(it.Embedding) > 0 {
				vecStr = formatVector(it.Embedding)
			} else if r.embeddingProvider != nil {
				textToEmbed := it.QuestionOrTopic + "\n" + it.AnswerOrContent
				if emb, embErr := r.embeddingProvider.GenerateEmbedding(ctx, textToEmbed); embErr == nil && len(emb) > 0 {
					vecStr = formatVector(emb)
				}
			}
			if _, err := stmt.ExecContext(ctx, tenantID, cat, it.QuestionOrTopic, it.AnswerOrContent, it.SourceDocument, it.SourceSection, it.SourceExcerpt, true, vecStr); err != nil {
				return fmt.Errorf("failed to insert knowledge item: %w", err)
			}
		} else {
			if _, err := stmt.ExecContext(ctx, tenantID, cat, it.QuestionOrTopic, it.AnswerOrContent, it.SourceDocument, it.SourceSection, it.SourceExcerpt, true); err != nil {
				return fmt.Errorf("failed to insert knowledge item: %w", err)
			}
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
		       jsonb_array_length(COALESCE(extracted_items, '[]'::jsonb)),
		       COALESCE(error_message, ''), created_at, started_at, completed_at
		FROM ai_knowledge_ingestion_jobs
		WHERE id = $1 AND tenant_id = $2
	`, jobID, tenantID).Scan(
		&job.ID, &job.TenantID, &job.Filename, &job.ContentType, &status, &itemsJSON, &job.ItemCount,
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

func (r *PostgresAIAgentRepository) ListKnowledgeIngestionJobs(ctx context.Context, tenantID, status string, limit, offset int) (*domain.KnowledgeIngestionPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ai_knowledge_ingestion_jobs
		WHERE tenant_id = $1 AND ($2 = '' OR status = $2)
	`, tenantID, status).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count ingestion jobs: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, tenant_id, filename, content_type, status,
		       jsonb_array_length(COALESCE(extracted_items, '[]'::jsonb)),
		       COALESCE(error_message, ''), created_at, started_at, completed_at
		FROM ai_knowledge_ingestion_jobs
		WHERE tenant_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`, tenantID, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list ingestion jobs: %w", err)
	}
	defer rows.Close()
	jobs := make([]domain.KnowledgeIngestionJob, 0, limit)
	for rows.Next() {
		var job domain.KnowledgeIngestionJob
		var statusValue string
		if err := rows.Scan(&job.ID, &job.TenantID, &job.Filename, &job.ContentType, &statusValue, &job.ItemCount, &job.ErrorMessage, &job.CreatedAt, &job.StartedAt, &job.CompletedAt); err != nil {
			return nil, fmt.Errorf("failed to scan ingestion job: %w", err)
		}
		job.Status = domain.KnowledgeIngestionStatus(statusValue)
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read ingestion jobs: %w", err)
	}
	return &domain.KnowledgeIngestionPage{Jobs: jobs, Total: total, Limit: limit, Offset: offset}, nil
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
	var vecStr string
	if len(item.Embedding) > 0 {
		vecStr = formatVector(item.Embedding)
	} else if r.embeddingProvider != nil {
		textToEmbed := item.QuestionOrTopic + "\n" + item.AnswerOrContent
		if emb, err := r.embeddingProvider.GenerateEmbedding(ctx, textToEmbed); err == nil && len(emb) > 0 {
			item.Embedding = emb
			vecStr = formatVector(emb)
		}
	}

	query := `
		UPDATE ai_knowledge_items
		SET category = $1, question_or_topic = $2, answer_or_content = $3, source_document = $4, source_section = $5, source_excerpt = $6, is_active = $7,
		    embedding = CASE WHEN $8 != '' THEN $8::vector ELSE embedding END,
		    updated_at = NOW()
		WHERE id = $9 AND tenant_id = $10
		RETURNING updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		item.Category, item.QuestionOrTopic, item.AnswerOrContent, item.SourceDocument, item.SourceSection, item.SourceExcerpt, item.IsActive, vecStr, item.ID, item.TenantID,
	).Scan(&item.UpdatedAt)

	if err != nil {
		fallbackQuery := `
			UPDATE ai_knowledge_items
			SET category = $1, question_or_topic = $2, answer_or_content = $3, source_document = $4, source_section = $5, source_excerpt = $6, is_active = $7, updated_at = NOW()
			WHERE id = $8 AND tenant_id = $9
			RETURNING updated_at
		`
		err = r.db.QueryRowContext(
			ctx, fallbackQuery,
			item.Category, item.QuestionOrTopic, item.AnswerOrContent, item.SourceDocument, item.SourceSection, item.SourceExcerpt, item.IsActive, item.ID, item.TenantID,
		).Scan(&item.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to update knowledge item: %w", err)
		}
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

func (r *PostgresAIAgentRepository) RecordKnowledgeGap(ctx context.Context, gap *domain.AIKnowledgeGap) error {
	if gap == nil || strings.TrimSpace(gap.Query) == "" || strings.TrimSpace(gap.TenantID) == "" {
		return nil
	}
	cleanQuery := strings.TrimSpace(gap.Query)
	channel := gap.Channel
	if channel == "" {
		channel = "whatsapp"
	}

	query := `
		INSERT INTO ai_knowledge_gaps (tenant_id, query, channel, conversation_id, ai_confidence, occurrences, status, last_asked_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 1, 'pending', NOW(), NOW(), NOW())
		ON CONFLICT (tenant_id, lower(trim(query))) WHERE status = 'pending'
		DO UPDATE SET
			occurrences = ai_knowledge_gaps.occurrences + 1,
			last_asked_at = NOW(),
			ai_confidence = EXCLUDED.ai_confidence,
			updated_at = NOW()
		RETURNING id, occurrences, created_at, updated_at
	`
	return r.db.QueryRowContext(
		ctx, query,
		gap.TenantID, cleanQuery, channel, gap.ConversationID, gap.AIConfidence,
	).Scan(&gap.ID, &gap.Occurrences, &gap.CreatedAt, &gap.UpdatedAt)
}

func (r *PostgresAIAgentRepository) ListKnowledgeGaps(ctx context.Context, tenantID, status string, limit, offset int) (*domain.AIKnowledgeGapsPage, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	statusFilter := "pending"
	if status != "" && status != "all" {
		statusFilter = status
	}

	var countQuery string
	var listQuery string
	var countArgs []interface{}
	var listArgs []interface{}

	if status == "all" {
		countQuery = `SELECT COUNT(*) FROM ai_knowledge_gaps WHERE tenant_id = $1`
		countArgs = []interface{}{tenantID}

		listQuery = `
			SELECT id, tenant_id, query, channel, conversation_id, ai_confidence, occurrences, status, resolution_item_id, last_asked_at, created_at, updated_at
			FROM ai_knowledge_gaps
			WHERE tenant_id = $1
			ORDER BY occurrences DESC, last_asked_at DESC
			LIMIT $2 OFFSET $3
		`
		listArgs = []interface{}{tenantID, limit, offset}
	} else {
		countQuery = `SELECT COUNT(*) FROM ai_knowledge_gaps WHERE tenant_id = $1 AND status = $2`
		countArgs = []interface{}{tenantID, statusFilter}

		listQuery = `
			SELECT id, tenant_id, query, channel, conversation_id, ai_confidence, occurrences, status, resolution_item_id, last_asked_at, created_at, updated_at
			FROM ai_knowledge_gaps
			WHERE tenant_id = $1 AND status = $2
			ORDER BY occurrences DESC, last_asked_at DESC
			LIMIT $3 OFFSET $4
		`
		listArgs = []interface{}{tenantID, statusFilter, limit, offset}
	}

	var total int
	if err := r.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count knowledge gaps: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to list knowledge gaps: %w", err)
	}
	defer rows.Close()

	gaps := make([]domain.AIKnowledgeGap, 0)
	for rows.Next() {
		var g domain.AIKnowledgeGap
		var convID, resID sql.NullString
		if err := rows.Scan(
			&g.ID, &g.TenantID, &g.Query, &g.Channel, &convID, &g.AIConfidence,
			&g.Occurrences, &g.Status, &resID, &g.LastAskedAt, &g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan knowledge gap: %w", err)
		}
		if convID.Valid {
			g.ConversationID = &convID.String
		}
		if resID.Valid {
			g.ResolutionItemID = &resID.String
		}
		gaps = append(gaps, g)
	}

	return &domain.AIKnowledgeGapsPage{
		Gaps:   gaps,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

func (r *PostgresAIAgentRepository) GetKnowledgeGap(ctx context.Context, tenantID, gapID string) (*domain.AIKnowledgeGap, error) {
	query := `
		SELECT id, tenant_id, query, channel, conversation_id, ai_confidence, occurrences, status, resolution_item_id, last_asked_at, created_at, updated_at
		FROM ai_knowledge_gaps
		WHERE id = $1 AND tenant_id = $2
	`
	var g domain.AIKnowledgeGap
	var convID, resID sql.NullString
	err := r.db.QueryRowContext(ctx, query, gapID, tenantID).Scan(
		&g.ID, &g.TenantID, &g.Query, &g.Channel, &convID, &g.AIConfidence,
		&g.Occurrences, &g.Status, &resID, &g.LastAskedAt, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("knowledge gap not found")
		}
		return nil, fmt.Errorf("failed to get knowledge gap: %w", err)
	}
	if convID.Valid {
		g.ConversationID = &convID.String
	}
	if resID.Valid {
		g.ResolutionItemID = &resID.String
	}
	return &g, nil
}

func (r *PostgresAIAgentRepository) ResolveKnowledgeGap(ctx context.Context, tenantID, gapID string, item *domain.AIKnowledgeItem) (*domain.AIKnowledgeItem, error) {
	if item == nil {
		return nil, fmt.Errorf("knowledge item is required")
	}
	item.TenantID = tenantID
	item.IsActive = true

	// 1. Create the knowledge item
	if err := r.CreateKnowledgeItem(ctx, item); err != nil {
		return nil, fmt.Errorf("failed to create knowledge item: %w", err)
	}

	// 2. Mark the gap as resolved
	updateQuery := `
		UPDATE ai_knowledge_gaps
		SET status = 'resolved', resolution_item_id = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3
	`
	_, err := r.db.ExecContext(ctx, updateQuery, item.ID, gapID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to mark gap as resolved: %w", err)
	}

	return item, nil
}

func (r *PostgresAIAgentRepository) DismissKnowledgeGap(ctx context.Context, tenantID, gapID string) error {
	query := `
		UPDATE ai_knowledge_gaps
		SET status = 'dismissed', updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`
	res, err := r.db.ExecContext(ctx, query, gapID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to dismiss knowledge gap: %w", err)
	}
	rowsAff, _ := res.RowsAffected()
	if rowsAff == 0 {
		return fmt.Errorf("knowledge gap not found")
	}
	return nil
}
