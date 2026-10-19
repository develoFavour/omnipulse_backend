package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/service"
	"omnipulse/apps/api-gateway/internal/utils"
)

type AIAgentHandler struct {
	repo                domain.AIAgentRepository
	aiEngine            *service.AIEngineService
	knowledgeDispatcher service.KnowledgeIngestionDispatcher
	inboxHub            *service.InboxHub
}

func NewAIAgentHandler(repo domain.AIAgentRepository) *AIAgentHandler {
	return &AIAgentHandler{repo: repo}
}

func (h *AIAgentHandler) SetAIEngine(aiEngine *service.AIEngineService) {
	h.aiEngine = aiEngine
}

func (h *AIAgentHandler) SetKnowledgeIngestionDispatcher(dispatcher service.KnowledgeIngestionDispatcher) {
	h.knowledgeDispatcher = dispatcher
}

func (h *AIAgentHandler) SetInboxHub(hub *service.InboxHub) {
	h.inboxHub = hub
}

// GetConfig handles GET /api/v1/ai-agent/config
func (h *AIAgentHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	cfg, err := h.repo.GetConfig(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.ProviderAvailable = h.aiEngine != nil && h.aiEngine.HasActiveProvider()

	utils.WriteJSON(w, http.StatusOK, cfg)
}

// ToggleGlobalAI handles POST /api/v1/ai-agent/toggle.
// This workspace-level consent switch is separate from per-conversation
// pause/resume controls used by human agents.
func (h *AIAgentHandler) ToggleGlobalAI(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Choose whether AI assistance should be enabled")
		return
	}
	if req.Enabled == nil {
		utils.WriteError(w, http.StatusBadRequest, "Choose whether AI assistance should be enabled")
		return
	}
	enabled := *req.Enabled
	if enabled && (h.aiEngine == nil || !h.aiEngine.HasActiveProvider()) {
		utils.WriteError(w, http.StatusServiceUnavailable, "AI assistance is not ready yet. Add a Gemini or Groq provider key before enabling it.")
		return
	}

	if err := h.repo.SetEnabled(r.Context(), tenantID, enabled); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "The AI assistance setting could not be updated. Please try again.")
		return
	}

	cfg, err := h.repo.GetConfig(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "The AI assistance setting was saved, but its current status could not be loaded.")
		return
	}
	cfg.ProviderAvailable = h.aiEngine != nil && h.aiEngine.HasActiveProvider()
	message := "AI assistance is now off for this workspace."
	if enabled {
		message = "AI assistance is now on for this workspace."
	}
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{"message": message, "config": cfg})
}

// SaveConfig handles PUT /api/v1/ai-agent/config
func (h *AIAgentHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req domain.AIAgentConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	req.TenantID = tenantID
	if req.OperationalMode == "" {
		req.OperationalMode = domain.AIOperationalModeAfterHours
	}
	switch req.OperationalMode {
	case domain.AIOperationalModeAlwaysOn, domain.AIOperationalModeAfterHours,
		domain.AIOperationalModeIdleFallback, domain.AIOperationalModeCopilotOnly:
	default:
		utils.WriteError(w, http.StatusBadRequest, "Choose a valid AI operating mode")
		return
	}
	if req.ConfidenceThreshold < 0 || req.ConfidenceThreshold > 1 {
		utils.WriteError(w, http.StatusBadRequest, "Confidence threshold must be between 0 and 1")
		return
	}
	if req.IdleTimeoutSeconds < 0 || req.IdleTimeoutSeconds > 86400 {
		utils.WriteError(w, http.StatusBadRequest, "Idle timeout must be between 0 and 24 hours")
		return
	}
	for _, day := range req.WorkDays {
		if day < 1 || day > 7 {
			utils.WriteError(w, http.StatusBadRequest, "Workdays must use values from 1 (Monday) to 7 (Sunday)")
			return
		}
	}
	if req.BusinessHoursStart == "" {
		req.BusinessHoursStart = "09:00:00"
	}
	if req.BusinessHoursEnd == "" {
		req.BusinessHoursEnd = "17:00:00"
	}
	if req.Timezone == "" {
		req.Timezone = "Africa/Lagos"
	}
	if len(req.WorkDays) == 0 {
		req.WorkDays = []int{1, 2, 3, 4, 5}
	}
	if req.ConfidenceThreshold <= 0 {
		req.ConfidenceThreshold = 0.70
	}

	if err := h.repo.SaveConfig(r.Context(), &req); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.ProviderAvailable = h.aiEngine != nil && h.aiEngine.HasActiveProvider()

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "AI assistant configuration saved successfully",
		"config":  req,
	})
}

// ListKnowledgeItems handles GET /api/v1/ai-agent/knowledge
func (h *AIAgentHandler) ListKnowledgeItems(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	limit, offset := paginationParams(r)
	page, err := h.repo.ListKnowledgeItemsPage(r.Context(), tenantID, strings.TrimSpace(r.URL.Query().Get("search")), strings.TrimSpace(r.URL.Query().Get("category")), limit, offset)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, page)
}

// ListKnowledgeIngestionJobs handles GET /api/v1/ai-agent/knowledge/ingest.
// It provides a paginated document library without returning every extracted item.
func (h *AIAgentHandler) ListKnowledgeIngestionJobs(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	limit, offset := paginationParams(r)
	page, err := h.repo.ListKnowledgeIngestionJobs(r.Context(), tenantID, strings.TrimSpace(r.URL.Query().Get("status")), limit, offset)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "The document library could not be loaded. Please try again.")
		return
	}
	utils.WriteJSON(w, http.StatusOK, page)
}

func paginationParams(r *http.Request) (int, int) {
	limit := 25
	offset := 0
	if value := r.URL.Query().Get("limit"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			limit = parsed
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			offset = parsed
		}
	}
	return limit, offset
}

// CreateKnowledgeItem handles POST /api/v1/ai-agent/knowledge
func (h *AIAgentHandler) CreateKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req domain.AIKnowledgeItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if strings.TrimSpace(req.QuestionOrTopic) == "" || strings.TrimSpace(req.AnswerOrContent) == "" {
		utils.WriteError(w, http.StatusBadRequest, "Question/topic and answer/content cannot be empty")
		return
	}

	if req.Category == "" {
		req.Category = "faq"
	}
	req.TenantID = tenantID
	req.IsActive = true

	if err := h.repo.CreateKnowledgeItem(r.Context(), &req); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, req)
}

// UpdateKnowledgeItem handles PUT /api/v1/ai-agent/knowledge/{id}
func (h *AIAgentHandler) UpdateKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	// Extract ID from URL path (e.g. /api/v1/ai-agent/knowledge/{id})
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		utils.WriteError(w, http.StatusBadRequest, "Missing knowledge item ID")
		return
	}
	itemID := pathParts[len(pathParts)-1]

	var req domain.AIKnowledgeItem
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	req.ID = itemID
	req.TenantID = tenantID

	if err := h.repo.UpdateKnowledgeItem(r.Context(), &req); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, req)
}

// DeleteKnowledgeItem handles DELETE /api/v1/ai-agent/knowledge/{id}
func (h *AIAgentHandler) DeleteKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		utils.WriteError(w, http.StatusBadRequest, "Missing knowledge item ID")
		return
	}
	itemID := pathParts[len(pathParts)-1]

	if err := h.repo.DeleteKnowledgeItem(r.Context(), tenantID, itemID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "Knowledge item deleted successfully"})
}

type ToggleConversationAIRequest struct {
	AIState         string `json:"ai_state"` // "active", "paused_by_human"
	ClearNeedsHuman bool   `json:"clear_needs_human"`
}

// ToggleConversationAI handles POST /api/v1/ai-agent/conversations/{id}/toggle
func (h *AIAgentHandler) ToggleConversationAI(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 5 {
		utils.WriteError(w, http.StatusBadRequest, "Missing conversation ID")
		return
	}
	conversationID := pathParts[len(pathParts)-2]

	var req ToggleConversationAIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	var pausedUntil *time.Time
	if req.AIState == "paused_by_human" {
		t := time.Now().Add(30 * time.Minute)
		pausedUntil = &t
	}

	needsHuman := false
	if !req.ClearNeedsHuman {
		// keep existing status or set false
		needsHuman = false
	}

	if err := h.repo.SetConversationAIState(r.Context(), tenantID, conversationID, req.AIState, pausedUntil, needsHuman); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.inboxHub != nil {
		h.inboxHub.BroadcastConversationAIState(tenantID, conversationID, req.AIState, pausedUntil, needsHuman)
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"conversation_id": conversationID,
		"ai_state":        req.AIState,
		"paused_until":    pausedUntil,
	})
}

// IngestDocument handles POST /api/v1/ai-agent/knowledge/ingest
func (h *AIAgentHandler) IngestDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	if h.aiEngine == nil || !h.aiEngine.HasActiveProvider() {
		utils.WriteError(w, http.StatusServiceUnavailable, "AI parsing engine is not configured (missing GEMINI_API_KEY / GROQ_API_KEY)")
		return
	}
	if h.knowledgeDispatcher == nil {
		utils.WriteError(w, http.StatusServiceUnavailable, "Document processing is temporarily unavailable. Please try again shortly.")
		return
	}

	upload, err := extractKnowledgeUpload(r)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(upload.data) == 0 {
		utils.WriteError(w, http.StatusBadRequest, "Document or text content cannot be empty")
		return
	}

	job, err := h.repo.CreateKnowledgeIngestionJob(r.Context(), tenantID, upload.filename, upload.contentType, upload.data)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "The document could not be queued for processing. Please try again.")
		return
	}

	if err := h.knowledgeDispatcher.EnqueueKnowledgeIngestion(r.Context(), job.ID); err != nil {
		log.Printf("[KNOWLEDGE-QUEUE] Failed to enqueue ingestion job %s: %v", job.ID, err)
		_ = h.repo.FailKnowledgeIngestionJob(r.Context(), job.ID, "Document processing is temporarily unavailable. Please try again.")
		utils.WriteError(w, http.StatusServiceUnavailable, "Document processing is temporarily unavailable. Please try again.")
		return
	}
	log.Printf("[KNOWLEDGE-QUEUE] Enqueued ingestion job %s (tenant=%s, filename=%q, content_type=%q, source_bytes=%d)", job.ID, tenantID, upload.filename, upload.contentType, len(upload.data))

	utils.WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"message": "Your document is being processed. Check the job status to review the extracted knowledge.",
		"job_id":  job.ID,
		"status":  job.Status,
	})
}

// GetKnowledgeIngestionJob returns the status and, when ready, proposed items
// for an ingestion job. Results remain tenant-scoped.
func (h *AIAgentHandler) GetKnowledgeIngestionJob(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	jobID := r.PathValue("id")
	if jobID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing ingestion job ID")
		return
	}

	job, err := h.repo.GetKnowledgeIngestionJob(r.Context(), tenantID, jobID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			utils.WriteError(w, http.StatusNotFound, "Document processing job not found")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "The document processing status could not be loaded")
		return
	}
	utils.WriteJSON(w, http.StatusOK, job)
}

// ApproveKnowledgeItems persists a reviewed set of extracted knowledge items.
func (h *AIAgentHandler) ApproveKnowledgeItems(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var req struct {
		Items []domain.AIKnowledgeItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if len(req.Items) == 0 {
		utils.WriteError(w, http.StatusBadRequest, "At least one knowledge item is required")
		return
	}

	for i := range req.Items {
		if strings.TrimSpace(req.Items[i].QuestionOrTopic) == "" || strings.TrimSpace(req.Items[i].AnswerOrContent) == "" {
			utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("Knowledge item %d is missing its topic or content", i+1))
			return
		}
		req.Items[i].TenantID = tenantID
		req.Items[i].IsActive = true
	}

	if err := h.repo.BulkCreateKnowledgeItems(r.Context(), tenantID, req.Items); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "The reviewed knowledge could not be saved. Please try again.")
		return
	}

	utils.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "Knowledge items added successfully",
		"count":   len(req.Items),
	})
}

type knowledgeUpload struct {
	filename    string
	contentType string
	data        []byte
}

func extractKnowledgeUpload(r *http.Request) (*knowledgeUpload, error) {
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		if err := r.ParseMultipartForm(service.MaxKnowledgeDocumentBytes + (1 << 20)); err != nil {
			return nil, fmt.Errorf("could not read the uploaded document")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return nil, fmt.Errorf("please choose a document to upload")
		}
		defer file.Close()

		limited := io.LimitReader(file, service.MaxKnowledgeDocumentBytes+1)
		data, err := io.ReadAll(limited)
		if err != nil {
			return nil, fmt.Errorf("could not read the uploaded document")
		}
		if int64(len(data)) > service.MaxKnowledgeDocumentBytes {
			return nil, fmt.Errorf("document exceeds the %d MB limit", service.MaxKnowledgeDocumentBytes/(1<<20))
		}
		filename := filepath.Base(header.Filename)
		if filename == "." || filename == "" {
			filename = "uploaded-document"
		}
		contentType := header.Header.Get("Content-Type")
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		return &knowledgeUpload{filename: filename, contentType: contentType, data: data}, nil
	}

	var req domain.IngestDocumentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, service.MaxKnowledgeDocumentBytes+1)).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid request payload")
	}
	if int64(len(req.Content)) > service.MaxKnowledgeDocumentBytes {
		return nil, fmt.Errorf("document exceeds the %d MB limit", service.MaxKnowledgeDocumentBytes/(1<<20))
	}
	filename := strings.TrimSpace(req.Title)
	if filename == "" {
		filename = "knowledge.txt"
	}
	if filepath.Ext(filename) == "" {
		filename += ".txt"
	}
	return &knowledgeUpload{filename: filepath.Base(filename), contentType: "text/plain", data: []byte(req.Content)}, nil
}

type TestSimulateRequest struct {
	Query string `json:"query"`
}

// TestSimulate handles POST /api/v1/ai-agent/simulate
func (h *AIAgentHandler) TestSimulate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok || tenantID == "" {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	if h.aiEngine == nil || !h.aiEngine.HasActiveProvider() {
		utils.WriteError(w, http.StatusServiceUnavailable, "AI engine is not configured (missing GEMINI_API_KEY / GROQ_API_KEY)")
		return
	}

	var req TestSimulateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if strings.TrimSpace(req.Query) == "" {
		utils.WriteError(w, http.StatusBadRequest, "Query cannot be empty")
		return
	}

	cfg, err := h.repo.GetConfig(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	knowledge, _ := h.repo.ListKnowledgeItems(r.Context(), tenantID)

	reply, needsHuman, leadInfo, err := h.aiEngine.GenerateReply(r.Context(), cfg, knowledge, nil, req.Query)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("AI simulation failed: %v", err))
		return
	}

	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"query":       req.Query,
		"reply":       reply,
		"needs_human": needsHuman,
		"lead_info":   leadInfo,
	})
}
