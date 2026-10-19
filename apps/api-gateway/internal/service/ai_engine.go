package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type AIEngineService struct {
	geminiKey string
	groqKey   string
	client    *http.Client
}

const (
	knowledgeChunkMaxChars = 12000
	knowledgeChunkOverlap  = 600
	maxKnowledgeChunks     = 100
	knowledgeOutputTokens  = 1600
)

func NewAIEngineService(geminiKey, groqKey string) *AIEngineService {
	return &AIEngineService{
		geminiKey: strings.TrimSpace(geminiKey),
		groqKey:   strings.TrimSpace(groqKey),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// HasActiveProvider returns true if at least one free AI provider key is configured
func (s *AIEngineService) HasActiveProvider() bool {
	return s.geminiKey != "" || s.groqKey != ""
}

// ShouldRespond checks operational mode and business hours
func (s *AIEngineService) ShouldRespond(cfg *domain.AIAgentConfig) bool {
	if cfg == nil || !cfg.IsEnabled {
		return false
	}

	switch cfg.OperationalMode {
	case domain.AIOperationalModeAlwaysOn, domain.AIOperationalModeIdleFallback:
		return true

	case domain.AIOperationalModeCopilotOnly:
		// Copilot only assists human agents with draft suggestions, does not auto-reply
		return false

	case domain.AIOperationalModeAfterHours:
		// Check current time against configured business hours
		tz := cfg.Timezone
		if tz == "" {
			tz = "Africa/Lagos"
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc = time.UTC
		}
		now := time.Now().In(loc)

		// Use the configuration contract: 1 = Monday, ..., 7 = Sunday.
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		isWorkDay := false
		for _, d := range cfg.WorkDays {
			if d == weekday {
				isWorkDay = true
				break
			}
		}

		if !isWorkDay {
			// Outside work days = after hours -> AI should respond
			return true
		}

		// Parse business hours (format "HH:MM:SS" or "HH:MM")
		currentMinutes := now.Hour()*60 + now.Minute()
		startMinutes := parseTimeToMinutes(cfg.BusinessHoursStart, 9*60)
		endMinutes := parseTimeToMinutes(cfg.BusinessHoursEnd, 17*60)

		outsideHours := false
		if startMinutes <= endMinutes {
			outsideHours = currentMinutes < startMinutes || currentMinutes >= endMinutes
		} else {
			// Support businesses whose shift crosses midnight, e.g. 22:00–06:00.
			outsideHours = currentMinutes >= endMinutes && currentMinutes < startMinutes
		}
		if outsideHours {
			// Outside business hours -> AI should respond
			return true
		}

		// Inside business hours -> human agents handle chat
		return false

	default:
		return true
	}
}

func parseTimeToMinutes(tStr string, fallback int) int {
	parts := strings.Split(tStr, ":")
	if len(parts) >= 2 {
		var h, m int
		fmt.Sscanf(parts[0], "%d", &h)
		fmt.Sscanf(parts[1], "%d", &m)
		return h*60 + m
	}
	return fallback
}

// GenerateReply orchestrates the prompt assembly and provider call with dual-engine fallback
func (s *AIEngineService) GenerateReply(
	ctx context.Context,
	cfg *domain.AIAgentConfig,
	knowledge []domain.AIKnowledgeItem,
	recentMessages []domain.Message,
	incomingText string,
) (reply string, needsHuman bool, lead *domain.ExtractedLeadInfo, err error) {
	if cfg == nil {
		return "", false, nil, fmt.Errorf("AI assistant configuration is missing")
	}
	if !s.HasActiveProvider() {
		return "", false, nil, fmt.Errorf("no AI provider configured (set GEMINI_API_KEY or GROQ_API_KEY)")
	}

	knowledge = rankKnowledgeItems(knowledge, incomingText, 8)
	activeKnowledge := 0
	for _, item := range knowledge {
		if item.IsActive {
			activeKnowledge++
		}
	}
	log.Printf("[AIEngine] Reply context prepared: knowledge_selected=%d active=%d operational_mode=%q confidence_threshold=%.2f", len(knowledge), activeKnowledge, cfg.OperationalMode, cfg.ConfidenceThreshold)
	systemPrompt := s.buildSystemPrompt(cfg, knowledge)

	// Build conversation history transcript (last 6 messages max)
	var historyBuilder strings.Builder
	msgCount := len(recentMessages)
	startIdx := 0
	if msgCount > 6 {
		startIdx = msgCount - 6
	}
	for i := startIdx; i < msgCount; i++ {
		m := recentMessages[i]
		role := "Customer"
		if m.Direction == domain.MessageDirectionOutbound {
			role = "Business Assistant"
		}
		historyBuilder.WriteString(fmt.Sprintf("%s: %s\n", role, strings.TrimSpace(m.Body)))
	}
	transcript := historyBuilder.String()

	// 1. Try Gemini REST API if configured
	if s.geminiKey != "" {
		reply, err = s.callGemini(ctx, systemPrompt, transcript, incomingText)
		if err == nil && reply != "" {
			processed, handoff, extractedLead, processErr := s.postProcessReply(reply)
			if processErr == nil {
				confidence, handoffReason, structured := responseMetadata(reply)
				log.Printf("[AIEngine] Gemini response processed: structured=%v confidence=%.2f handoff=%v reason=%q knowledge_selected=%d", structured, confidence, handoff, handoffReason, len(knowledge))
				if confidence := responseConfidence(reply); cfg.ConfidenceThreshold > 0 && confidence < cfg.ConfidenceThreshold {
					fallback := cfg.UnsupportedQueryMessage
					if fallback == "" {
						fallback = "I do not have that information right now, but a team member will follow up with you."
					}
					return fallback, true, extractedLead, nil
				}
				return processed, handoff, extractedLead, nil
			}
			err = processErr
		}
		log.Printf("[AIEngine] ⚠️ Gemini call failed: %v. Attempting Groq fallback...\n", err)
	}

	// 2. Fallback to Groq if configured
	if s.groqKey != "" {
		reply, err = s.callGroq(ctx, systemPrompt, transcript, incomingText)
		if err == nil && reply != "" {
			processed, handoff, extractedLead, processErr := s.postProcessReply(reply)
			if processErr == nil {
				confidence, handoffReason, structured := responseMetadata(reply)
				log.Printf("[AIEngine] Groq response processed: structured=%v confidence=%.2f handoff=%v reason=%q knowledge_selected=%d", structured, confidence, handoff, handoffReason, len(knowledge))
				if confidence := responseConfidence(reply); cfg.ConfidenceThreshold > 0 && confidence < cfg.ConfidenceThreshold {
					fallback := cfg.UnsupportedQueryMessage
					if fallback == "" {
						fallback = "I do not have that information right now, but a team member will follow up with you."
					}
					return fallback, true, extractedLead, nil
				}
				return processed, handoff, extractedLead, nil
			}
			err = processErr
		}
		log.Printf("[AIEngine] ⚠️ Groq fallback failed: %v\n", err)
	}

	if reply == "" {
		return "", false, nil, fmt.Errorf("all configured AI providers failed to generate a response")
	}

	return s.postProcessReply(reply)
}

// rankKnowledgeItems keeps the prompt tenant-scoped and bounded. It uses a
// deterministic lexical match for the current MVP; a pgvector-backed retriever
// can replace this function later without changing the AI orchestration API.
func rankKnowledgeItems(items []domain.AIKnowledgeItem, query string, limit int) []domain.AIKnowledgeItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	queryTokens := knowledgeTokens(query)
	type scoredItem struct {
		item  domain.AIKnowledgeItem
		score int
	}
	scored := make([]scoredItem, 0, len(items))
	for _, item := range items {
		if !item.IsActive {
			continue
		}
		textTokens := knowledgeTokens(item.Category + " " + item.QuestionOrTopic + " " + item.AnswerOrContent)
		score := 0
		for token := range queryTokens {
			if _, found := textTokens[token]; found {
				score++
			}
		}
		if score > 0 {
			scored = append(scored, scoredItem{item: item, score: score})
		}
	}
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	result := make([]domain.AIKnowledgeItem, 0, len(scored))
	for _, entry := range scored {
		result = append(result, entry.item)
	}
	return result
}

func knowledgeTokens(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if len(token) >= 3 {
			result[token] = struct{}{}
		}
	}
	return result
}

type structuredAIResponse struct {
	Response      string                    `json:"response"`
	ShouldHandoff bool                      `json:"should_handoff"`
	HandoffReason string                    `json:"handoff_reason"`
	Confidence    float64                   `json:"confidence"`
	LeadInfo      *domain.ExtractedLeadInfo `json:"lead_info,omitempty"`
}

func (s *AIEngineService) postProcessReply(rawReply string) (string, bool, *domain.ExtractedLeadInfo, error) {
	needsHuman := false
	cleaned := rawReply
	legacyMarkers := strings.Contains(rawReply, "[HANDOFF_TO_HUMAN]") || strings.Contains(rawReply, "[LEAD_NAME:") || strings.Contains(rawReply, "[LEAD_EMAIL:")

	// Providers are instructed to return JSON. Validate it before sending anything
	// to a customer. Marker parsing remains as a backwards-compatible fallback for
	// already-configured providers, but is treated as low-confidence output.
	jsonCandidate := strings.TrimSpace(rawReply)
	if start := strings.Index(jsonCandidate, "{"); start >= 0 {
		if end := strings.LastIndex(jsonCandidate, "}"); end > start {
			var structured structuredAIResponse
			if err := json.Unmarshal([]byte(jsonCandidate[start:end+1]), &structured); err == nil {
				markerFound := strings.Contains(structured.Response, "[HANDOFF_TO_HUMAN]")
				cleanedResponse := stripInternalMarkers(structured.Response)
				if cleanedResponse == "" {
					return "", false, nil, fmt.Errorf("AI provider returned an empty response")
				}
				return cleanedResponse, structured.ShouldHandoff || markerFound, structured.LeadInfo, nil
			}
		}
	}

	if strings.Contains(cleaned, "[HANDOFF_TO_HUMAN]") {
		needsHuman = true
		cleaned = strings.ReplaceAll(cleaned, "[HANDOFF_TO_HUMAN]", "")
	}

	var lead *domain.ExtractedLeadInfo
	if strings.Contains(cleaned, "[LEAD_NAME:") || strings.Contains(cleaned, "[LEAD_EMAIL:") {
		lead = &domain.ExtractedLeadInfo{}
		// Extract LEAD_NAME
		if idx := strings.Index(cleaned, "[LEAD_NAME:"); idx != -1 {
			end := strings.Index(cleaned[idx:], "]")
			if end != -1 {
				namePart := cleaned[idx+11 : idx+end]
				lead.Name = strings.TrimSpace(namePart)
				cleaned = cleaned[:idx] + cleaned[idx+end+1:]
			}
		}
		// Extract LEAD_EMAIL
		if idx := strings.Index(cleaned, "[LEAD_EMAIL:"); idx != -1 {
			end := strings.Index(cleaned[idx:], "]")
			if end != -1 {
				emailPart := cleaned[idx+12 : idx+end]
				lead.Email = strings.TrimSpace(emailPart)
				cleaned = cleaned[:idx] + cleaned[idx+end+1:]
			}
		}
	}

	cleaned = stripInternalMarkers(cleaned)
	if cleaned == "" {
		return "", false, nil, fmt.Errorf("AI provider returned an empty response")
	}
	if !legacyMarkers {
		return "", false, nil, fmt.Errorf("AI provider returned an invalid response format")
	}
	return cleaned, needsHuman, lead, nil
}

func stripInternalMarkers(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "[HANDOFF_TO_HUMAN]", ""))
}

func responseMetadata(rawReply string) (confidence float64, handoffReason string, structured bool) {
	start := strings.Index(rawReply, "{")
	end := strings.LastIndex(rawReply, "}")
	if start < 0 || end <= start {
		return 0, "", false
	}
	var response structuredAIResponse
	if err := json.Unmarshal([]byte(rawReply[start:end+1]), &response); err != nil {
		return 0, "", false
	}
	return response.Confidence, response.HandoffReason, true
}

func responseConfidence(rawReply string) float64 {
	start := strings.Index(rawReply, "{")
	end := strings.LastIndex(rawReply, "}")
	if start < 0 || end <= start {
		return 0
	}
	var response structuredAIResponse
	if err := json.Unmarshal([]byte(rawReply[start:end+1]), &response); err != nil {
		return 0
	}
	return response.Confidence
}

// ExtractKnowledgeItemsFromText parses raw documents, FAQs, or business policies into structured Q&A items
func (s *AIEngineService) ExtractKnowledgeItemsFromText(ctx context.Context, text string) ([]domain.AIKnowledgeItem, error) {
	if !s.HasActiveProvider() {
		return nil, fmt.Errorf("no AI provider configured")
	}
	return s.extractKnowledgeItemsChunked(ctx, text)

	/*
			systemPrompt := `You are an expert AI knowledge base parser for businesses and agencies.
		Analyze the provided text, document, FAQs, product list, or policy, and extract all factual Question & Answer (or Topic & Detail) knowledge items.
		Categories MUST be one of: "faq", "pricing", "product", "policy", "hours".

		Strictly output ONLY a valid JSON array of objects without any markdown formatting or commentary:
		[
		  {
		    "category": "pricing",
		    "question_or_topic": "What are your delivery fees?",
		    "answer_or_content": "Delivery to Lagos is ₦2,000 and ₦4,000 nationwide."
		  }
		]`

			var rawOutput string
			var err error
			log.Printf("[AIEngine] Knowledge extraction started (text_bytes=%d, gemini_configured=%t, groq_configured=%t)", len(text), s.geminiKey != "", s.groqKey != "")

			if s.geminiKey != "" {
				rawOutput, err = s.callGemini(ctx, systemPrompt, "", text)
				if err == nil && rawOutput != "" {
					return parseExtractedKnowledgeJSON(rawOutput)
				}
				log.Printf("[AIEngine] ⚠️ Gemini knowledge extraction failed: %v. Trying Groq...\n", err)
			}

			if s.groqKey != "" {
				rawOutput, err = s.callGroq(ctx, systemPrompt, "", text)
				if err == nil && rawOutput != "" {
					return parseExtractedKnowledgeJSON(rawOutput)
				}
				log.Printf("[AIEngine] ⚠️ Groq knowledge extraction failed: %v\n", err)
			}

			if err != nil {
				log.Printf("[AIEngine] Knowledge extraction failed after all configured providers: %v", err)
				return nil, fmt.Errorf("failed to extract knowledge items: %w", err)
			}
			return nil, fmt.Errorf("all providers failed to extract knowledge items")
	*/
}

func (s *AIEngineService) extractKnowledgeItemsChunked(ctx context.Context, text string) ([]domain.AIKnowledgeItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("document contains no readable text")
	}
	chunks, err := chunkKnowledgeText(text, knowledgeChunkMaxChars, knowledgeChunkOverlap)
	if err != nil {
		return nil, err
	}
	if len(chunks) > maxKnowledgeChunks {
		return nil, fmt.Errorf("document is too large to analyze in one job; please split it into smaller documents")
	}

	systemPrompt := `You are an expert AI knowledge base parser for businesses and agencies.
Analyze ONLY the provided document section and extract every distinct factual Question & Answer (or Topic & Detail) knowledge item in that section.
Do not omit prices, names, limits, eligibility rules, locations, dates, contact details, or other concrete facts. Preserve important details in the answer.
Do not invent information and do not refer to facts outside the provided section.
Categories MUST be one of: "faq", "pricing", "product", "policy", "hours".

Strictly output ONLY a valid JSON array of objects without any markdown formatting or commentary:
[
  {
    "category": "pricing",
    "question_or_topic": "What are your delivery fees?",
    "answer_or_content": "Delivery to Lagos is ₦2,000 and ₦4,000 nationwide."
  }
]`

	log.Printf("[AIEngine] Knowledge extraction started (text_bytes=%d, chunks=%d, gemini_configured=%t, groq_configured=%t)", len(text), len(chunks), s.geminiKey != "", s.groqKey != "")
	allItems := make([]domain.AIKnowledgeItem, 0)
	for index, chunk := range chunks {
		var rawOutput string
		var providerErr error
		if s.geminiKey != "" {
			rawOutput, providerErr = s.callGemini(ctx, systemPrompt, "", chunk)
			if providerErr == nil && rawOutput != "" {
				items, parseErr := parseExtractedKnowledgeJSON(rawOutput)
				if parseErr == nil {
					allItems = append(allItems, items...)
					log.Printf("[AIEngine] Knowledge chunk %d/%d completed (chars=%d, items=%d)", index+1, len(chunks), len(chunk), len(items))
					continue
				}
				providerErr = parseErr
			}
			log.Printf("[AIEngine] Gemini knowledge chunk failed: %v. Trying Groq fallback", providerErr)
		}
		if s.groqKey != "" {
			rawOutput, providerErr = s.callGroq(ctx, systemPrompt, "", chunk)
			if providerErr == nil && rawOutput != "" {
				items, parseErr := parseExtractedKnowledgeJSON(rawOutput)
				if parseErr == nil {
					allItems = append(allItems, items...)
					log.Printf("[AIEngine] Knowledge chunk %d/%d completed (chars=%d, items=%d)", index+1, len(chunks), len(chunk), len(items))
					continue
				}
				providerErr = parseErr
			}
			log.Printf("[AIEngine] Groq knowledge chunk failed: %v", providerErr)
		}
		return nil, fmt.Errorf("failed to extract knowledge from document section %d: %w", index+1, providerErr)
	}
	return mergeKnowledgeItems(allItems), nil
}

func chunkKnowledgeText(text string, maxChars, overlap int) ([]string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("document contains no readable text")
	}
	if maxChars <= 0 || overlap < 0 || overlap >= maxChars {
		return nil, fmt.Errorf("invalid knowledge chunk configuration")
	}
	chunks := make([]string, 0, (len(text)/maxChars)+1)
	start := 0
	for start < len(text) {
		end := start + maxChars
		if end >= len(text) {
			chunks = append(chunks, strings.TrimSpace(text[start:]))
			break
		}
		boundary := end
		for boundary > start && !isKnowledgeBoundary(text[boundary]) {
			boundary--
		}
		if boundary == start {
			boundary = end
		}
		chunk := strings.TrimSpace(text[start:boundary])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		next := boundary - overlap
		if next <= start {
			next = boundary
		}
		start = next
	}
	return chunks, nil
}

func isKnowledgeBoundary(value byte) bool {
	return value == ' ' || value == '\n' || value == '\r' || value == '\t'
}

func mergeKnowledgeItems(items []domain.AIKnowledgeItem) []domain.AIKnowledgeItem {
	merged := make([]domain.AIKnowledgeItem, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item.QuestionOrTopic = strings.TrimSpace(item.QuestionOrTopic)
		item.AnswerOrContent = strings.TrimSpace(item.AnswerOrContent)
		if item.QuestionOrTopic == "" || item.AnswerOrContent == "" {
			continue
		}
		if item.Category == "" {
			item.Category = "faq"
		}
		item.IsActive = true
		key := strings.ToLower(strings.Join([]string{item.Category, item.QuestionOrTopic, item.AnswerOrContent}, "\x00"))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, item)
	}
	return merged
}

func parseExtractedKnowledgeJSON(raw string) ([]domain.AIKnowledgeItem, error) {
	cleaned := strings.TrimSpace(raw)
	// Strip markdown fences if present
	if strings.HasPrefix(cleaned, "```json") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
	} else if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```")
	}
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	start := strings.Index(cleaned, "[")
	end := strings.LastIndex(cleaned, "]")
	if start != -1 && end != -1 && end > start {
		cleaned = cleaned[start : end+1]
	}

	var items []domain.AIKnowledgeItem
	if err := json.Unmarshal([]byte(cleaned), &items); err != nil {
		return nil, fmt.Errorf("failed to unmarshal extracted JSON: %w (output: %s)", err, cleaned)
	}

	for i := range items {
		items[i].IsActive = true
		if items[i].Category == "" {
			items[i].Category = "faq"
		}
	}
	return items, nil
}

func (s *AIEngineService) buildSystemPrompt(cfg *domain.AIAgentConfig, knowledge []domain.AIKnowledgeItem) string {
	var b strings.Builder

	businessName := cfg.BusinessName
	if businessName == "" {
		businessName = "our business"
	}
	tone := cfg.Tone
	if tone == "" {
		tone = "friendly, helpful, and professional"
	}
	handoffMsg := cfg.HumanHandoffMessage
	if handoffMsg == "" {
		handoffMsg = "I am connecting you with one of our human team members who will assist you shortly."
	}

	b.WriteString(fmt.Sprintf("You are the official autonomous customer support assistant for %s.\n", businessName))
	b.WriteString(fmt.Sprintf("Tone of voice: %s.\n", tone))
	if cfg.Language != "" {
		b.WriteString(fmt.Sprintf("Primary language: %s.\n", cfg.Language))
	}

	if cfg.BusinessSummary != "" {
		b.WriteString("\n=== BUSINESS OVERVIEW ===\n")
		b.WriteString(cfg.BusinessSummary)
		b.WriteString("\n")
	}

	if len(knowledge) > 0 {
		b.WriteString("\n=== VERIFIED KNOWLEDGE BASE & FAQs ===\n")
		for _, item := range knowledge {
			if !item.IsActive {
				continue
			}
			b.WriteString(fmt.Sprintf("• [%s] %s: %s\n", strings.ToUpper(item.Category), item.QuestionOrTopic, item.AnswerOrContent))
		}
	}

	b.WriteString("\n=== OPERATING HOURS & LOCATION ===\n")
	b.WriteString(fmt.Sprintf("Hours: %s to %s (%s)\n", cfg.BusinessHoursStart, cfg.BusinessHoursEnd, cfg.Timezone))

	if cfg.CustomSystemPrompt != "" {
		b.WriteString("\n=== SPECIAL INSTRUCTIONS ===\n")
		b.WriteString(cfg.CustomSystemPrompt)
		b.WriteString("\n")
	}

	b.WriteString("\n=== STRICT RULES ===\n")
	b.WriteString("1. ONLY answer questions using the facts and information provided above. DO NOT invent prices, delivery policies, or products.\n")
	b.WriteString("2. Only hand off when the user explicitly asks for a human/agent/representative, or when the issue truly requires human action. Do not hand off merely because a question is broad or ambiguous.\n")
	b.WriteString("3. If the user asks about a product or service generally and the knowledge base contains relevant facts, answer with those facts. If multiple products could match, ask which one they mean.\n")
	b.WriteString("4. If the knowledge base does not contain enough information and the user did not ask for a human, explain what information is missing and ask a concise clarifying question. Set should_handoff to false.\n")
	b.WriteString(fmt.Sprintf("5. When a human handoff is genuinely required, respond politely with: %q. Do not include internal markers in the response field; set should_handoff to true instead.\n", handoffMsg))
	b.WriteString("6. Keep responses concise and formatted cleanly for WhatsApp/Telegram (under 3-4 sentences when possible). Use bullet points for lists.\n")
	b.WriteString("7. If the customer introduces themselves or shares an email address, include the extracted details in lead_info.\n")
	b.WriteString("8. Return ONLY valid JSON with this exact shape: {\"response\":\"...\",\"should_handoff\":false,\"handoff_reason\":\"none\",\"confidence\":0.95,\"lead_info\":null}. Do not include markdown or extra commentary.\n")

	return b.String()
}

// callGemini executes a prompt against Google Gemini REST API
func (s *AIEngineService) callGemini(ctx context.Context, systemPrompt, transcript, incomingText string) (string, error) {
	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent"

	userPrompt := incomingText
	if transcript != "" {
		userPrompt = fmt.Sprintf("Recent conversation history:\n%s\nCustomer just sent: %s", transcript, incomingText)
	}

	payload := map[string]interface{}{
		"system_instruction": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": systemPrompt},
			},
		},
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": userPrompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":      0.3,
			"maxOutputTokens":  knowledgeOutputTokens,
			"responseMimeType": "application/json",
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", s.geminiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini api error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(respBytes, &geminiResp); err != nil {
		return "", fmt.Errorf("failed to parse gemini response: %w", err)
	}

	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		return geminiResp.Candidates[0].Content.Parts[0].Text, nil
	}

	return "", fmt.Errorf("gemini returned no content candidate")
}

// callGroq executes a prompt against Groq Llama 3.3 70B REST API
func (s *AIEngineService) callGroq(ctx context.Context, systemPrompt, transcript, incomingText string) (string, error) {
	url := "https://api.groq.com/openai/v1/chat/completions"

	messages := []map[string]string{
		{"role": "system", "content": systemPrompt},
	}

	if transcript != "" {
		messages = append(messages, map[string]string{
			"role":    "user",
			"content": fmt.Sprintf("Recent context:\n%s", transcript),
		})
	}

	messages = append(messages, map[string]string{
		"role":    "user",
		"content": incomingText,
	})

	payload := map[string]interface{}{
		"model":           "openai/gpt-oss-120b",
		"messages":        messages,
		"temperature":     0.3,
		"max_tokens":      knowledgeOutputTokens,
		"response_format": map[string]string{"type": "json_object"},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.groqKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("groq http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("groq api error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var groqResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBytes, &groqResp); err != nil {
		return "", fmt.Errorf("failed to parse groq response: %w", err)
	}

	if len(groqResp.Choices) > 0 {
		return groqResp.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("groq returned no choices")
}
