package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

var ErrEmbeddingThrottled = fmt.Errorf("gemini embedding rate limit reached; circuit open")

type AIEngineService struct {
	geminiKey               string
	groqKey                 string
	client                  *http.Client
	embeddingThrottledUntil time.Time
	embeddingMu             sync.RWMutex
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

	// Contextual ranking: include recent messages so follow-ups ("Is that all?", "How much?") retain conversation context
	rankingQuery := incomingText
	for i := len(recentMessages) - 1; i >= 0 && i >= len(recentMessages)-3; i-- {
		rankingQuery += " " + recentMessages[i].Body
	}
	knowledge = rankKnowledgeItems(knowledge, rankingQuery, 8)
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

var commonStopWords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "that": {}, "this": {}, "with": {},
	"your": {}, "you": {}, "are": {}, "was": {}, "have": {}, "from": {},
	"they": {}, "will": {}, "would": {}, "could": {}, "should": {}, "there": {},
	"their": {}, "about": {}, "what": {}, "when": {}, "where": {}, "which": {},
	"tell": {}, "know": {}, "some": {}, "been": {}, "how": {}, "can": {},
	"our": {}, "all": {}, "any": {}, "who": {}, "give": {},
}

// rankKnowledgeItems keeps the prompt tenant-scoped and bounded. It uses
// lexical matching with intent boosts and automatically backfills active items
// so conversational queries and follow-ups never leave the AI context empty.
func rankKnowledgeItems(items []domain.AIKnowledgeItem, query string, limit int) []domain.AIKnowledgeItem {
	if limit <= 0 {
		return nil
	}
	queryLower := strings.ToLower(strings.TrimSpace(query))
	queryTokens := knowledgeTokens(query)

	type scoredItem struct {
		item  domain.AIKnowledgeItem
		score int
	}
	scored := make([]scoredItem, 0, len(items))

	// Detect conversational inquiry intent signals
	wantsProduct := strings.Contains(queryLower, "product") || strings.Contains(queryLower, "service") ||
		strings.Contains(queryLower, "what do you do") || strings.Contains(queryLower, "offer") ||
		strings.Contains(queryLower, "overview") || strings.Contains(queryLower, "features") ||
		strings.Contains(queryLower, "about")
	wantsPricing := strings.Contains(queryLower, "price") || strings.Contains(queryLower, "pricing") ||
		strings.Contains(queryLower, "cost") || strings.Contains(queryLower, "plan") ||
		strings.Contains(queryLower, "subscription") || strings.Contains(queryLower, "fee") ||
		strings.Contains(queryLower, "pay")
	wantsHours := strings.Contains(queryLower, "hour") || strings.Contains(queryLower, "open") ||
		strings.Contains(queryLower, "time") || strings.Contains(queryLower, "contact") ||
		strings.Contains(queryLower, "support")
	wantsPolicy := strings.Contains(queryLower, "refund") || strings.Contains(queryLower, "cancel") ||
		strings.Contains(queryLower, "return") || strings.Contains(queryLower, "guarantee")

	for _, item := range items {
		if !item.IsActive {
			continue
		}
		textTokens := knowledgeTokens(item.Category + " " + item.QuestionOrTopic + " " + item.AnswerOrContent)
		topicLower := strings.ToLower(item.QuestionOrTopic)
		catLower := strings.ToLower(item.Category)
		score := 0

		if queryLower != "" && strings.Contains(topicLower, queryLower) {
			score += 5
		}

		for token := range queryTokens {
			if _, found := textTokens[token]; found {
				score += 2
			}
		}

		// Intent category boosts
		if wantsProduct && (catLower == "product" || strings.Contains(topicLower, "overview") || strings.Contains(topicLower, "product")) {
			score += 4
		}
		if wantsPricing && (catLower == "pricing" || strings.Contains(topicLower, "plan") || strings.Contains(topicLower, "price")) {
			score += 4
		}
		if wantsHours && (catLower == "hours" || strings.Contains(topicLower, "hour") || strings.Contains(topicLower, "support")) {
			score += 4
		}
		if wantsPolicy && (catLower == "policy" || strings.Contains(topicLower, "refund") || strings.Contains(topicLower, "cancel")) {
			score += 4
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

	result := make([]domain.AIKnowledgeItem, 0, limit)
	seenIDs := make(map[string]bool)
	for _, entry := range scored {
		if !seenIDs[entry.item.ID] {
			seenIDs[entry.item.ID] = true
			result = append(result, entry.item)
			if len(result) >= limit {
				break
			}
		}
	}

	// Backfill with other active knowledge items so the system prompt is never starved of context
	if len(result) < limit {
		for _, item := range items {
			if !item.IsActive || seenIDs[item.ID] {
				continue
			}
			seenIDs[item.ID] = true
			result = append(result, item)
			if len(result) >= limit {
				break
			}
		}
	}

	return result
}

func knowledgeTokens(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if len(token) >= 3 {
			if _, isStop := commonStopWords[token]; !isStop {
				result[token] = struct{}{}
			}
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

var inlineBulletRegex = regexp.MustCompile(`([^\n])\s*(•\s+)`)
var inlinePunctBulletRegex = regexp.MustCompile(`([.!?])\s*([•\-\*]\s+)`)

func isPureGreeting(text string) bool {
	cleaned := strings.ToLower(strings.TrimSpace(text))
	cleaned = strings.Trim(cleaned, "!.,? ")
	switch cleaned {
	case "hi", "hello", "hey", "hiya", "howdy", "good morning", "good afternoon", "good evening", "good day", "greetings", "hi there", "hello there", "hey there", "yo", "sup":
		return true
	}
	return false
}

func stripInternalMarkers(value string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(value, "[HANDOFF_TO_HUMAN]", ""))
	cleaned = inlinePunctBulletRegex.ReplaceAllString(cleaned, "$1\n\n$2")
	cleaned = inlineBulletRegex.ReplaceAllString(cleaned, "$1\n\n$2")
	return strings.TrimSpace(cleaned)
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
	return s.ExtractKnowledgeItemsFromDocument(ctx, "", text)
}

// ExtractKnowledgeItemsFromDocument preserves the source document and section
// that produced each suggestion so a reviewer can verify the extracted fact.
func (s *AIEngineService) ExtractKnowledgeItemsFromDocument(ctx context.Context, sourceDocument, text string) ([]domain.AIKnowledgeItem, error) {
	if !s.HasActiveProvider() {
		return nil, fmt.Errorf("no AI provider configured")
	}
	return s.extractKnowledgeItemsChunked(ctx, sourceDocument, text)

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

func (s *AIEngineService) extractKnowledgeItemsChunked(ctx context.Context, sourceDocument, text string) ([]domain.AIKnowledgeItem, error) {
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

Strictly output ONLY a valid JSON object containing an "items" array:
{
  "items": [
    {
      "category": "pricing",
      "question_or_topic": "What are your delivery fees?",
      "answer_or_content": "Delivery to Lagos is ₦2,000 and ₦4,000 nationwide."
    }
  ]
}`

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
					annotateKnowledgeItems(items, sourceDocument, index+1, chunk)
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
					annotateKnowledgeItems(items, sourceDocument, index+1, chunk)
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

func annotateKnowledgeItems(items []domain.AIKnowledgeItem, sourceDocument string, section int, chunk string) {
	if strings.TrimSpace(sourceDocument) == "" {
		return
	}
	excerpt := strings.TrimSpace(chunk)
	if len(excerpt) > 280 {
		excerpt = excerpt[:280] + "…"
	}
	for i := range items {
		items[i].SourceDocument = sourceDocument
		items[i].SourceSection = fmt.Sprintf("Section %d", section)
		items[i].SourceExcerpt = excerpt
	}
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

	// Attempt 1: Direct JSON array [...]
	var directList []domain.AIKnowledgeItem
	if err := json.Unmarshal([]byte(cleaned), &directList); err == nil && len(directList) > 0 {
		return sanitizeKnowledgeItems(directList), nil
	}

	// Attempt 2: Object with "items", "data", "knowledge", or "facts" wrapper (standard Groq json_object format)
	var wrapper struct {
		Items     []domain.AIKnowledgeItem `json:"items"`
		Knowledge []domain.AIKnowledgeItem `json:"knowledge"`
		Data      []domain.AIKnowledgeItem `json:"data"`
		Facts     []domain.AIKnowledgeItem `json:"facts"`
	}
	if err := json.Unmarshal([]byte(cleaned), &wrapper); err == nil {
		if len(wrapper.Items) > 0 {
			return sanitizeKnowledgeItems(wrapper.Items), nil
		}
		if len(wrapper.Knowledge) > 0 {
			return sanitizeKnowledgeItems(wrapper.Knowledge), nil
		}
		if len(wrapper.Data) > 0 {
			return sanitizeKnowledgeItems(wrapper.Data), nil
		}
		if len(wrapper.Facts) > 0 {
			return sanitizeKnowledgeItems(wrapper.Facts), nil
		}
	}

	// Attempt 3: Single JSON object { "category": "...", ... } directly returned by model
	var single domain.AIKnowledgeItem
	if err := json.Unmarshal([]byte(cleaned), &single); err == nil {
		if strings.TrimSpace(single.QuestionOrTopic) != "" || strings.TrimSpace(single.AnswerOrContent) != "" {
			return sanitizeKnowledgeItems([]domain.AIKnowledgeItem{single}), nil
		}
	}

	// Attempt 4: Slice substring between '[' and ']'
	start := strings.Index(cleaned, "[")
	end := strings.LastIndex(cleaned, "]")
	if start != -1 && end != -1 && end > start {
		var subList []domain.AIKnowledgeItem
		if err := json.Unmarshal([]byte(cleaned[start:end+1]), &subList); err == nil && len(subList) > 0 {
			return sanitizeKnowledgeItems(subList), nil
		}
	}

	// Attempt 5: Single object substring between '{' and '}'
	objStart := strings.Index(cleaned, "{")
	objEnd := strings.LastIndex(cleaned, "}")
	if objStart != -1 && objEnd != -1 && objEnd > objStart {
		var subObj domain.AIKnowledgeItem
		if err := json.Unmarshal([]byte(cleaned[objStart:objEnd+1]), &subObj); err == nil {
			if strings.TrimSpace(subObj.QuestionOrTopic) != "" || strings.TrimSpace(subObj.AnswerOrContent) != "" {
				return sanitizeKnowledgeItems([]domain.AIKnowledgeItem{subObj}), nil
			}
		}
	}

	return nil, fmt.Errorf("failed to unmarshal extracted JSON: unexpected structure (output: %s)", cleaned)
}

func sanitizeKnowledgeItems(items []domain.AIKnowledgeItem) []domain.AIKnowledgeItem {
	for i := range items {
		items[i].IsActive = true
		items[i].Category = strings.ToLower(strings.TrimSpace(items[i].Category))
		if items[i].Category == "" {
			items[i].Category = "faq"
		}
		items[i].QuestionOrTopic = strings.TrimSpace(items[i].QuestionOrTopic)
		items[i].AnswerOrContent = strings.TrimSpace(items[i].AnswerOrContent)
	}
	return items
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
	b.WriteString(fmt.Sprintf("2. GREETINGS & CASUAL OPENERS: If the customer sends a simple greeting or casual opener (such as 'Hi', 'Hello', 'Hey', 'Good morning', 'Good afternoon', 'Good evening', or 'How are you?'), reply warmly, briefly, and politely (e.g., 'Hello! Welcome to %s. How can I assist you today?'). NEVER launch into a full company overview, sales pitch, or feature list in response to a simple greeting.\n", businessName))
	b.WriteString("3. Only hand off when the user explicitly asks for a human/agent/representative, or when the issue truly requires human action. Do not hand off merely because a question is broad or ambiguous.\n")
	b.WriteString(fmt.Sprintf("4. When the customer actually ASKS about 'your product', 'your services', 'what do you do', or what the business offers, introduce %s and summarize its core features, value proposition, and benefits using the Business Overview and Knowledge Base. If there is one primary product or platform described, present it directly. Never ask 'which specific product do you mean?' unless the user specifically asks to choose between multiple distinct product names.\n", businessName))
	b.WriteString("5. Maintain conversation context across messages. If the customer asks follow-up questions such as 'Is that all?', 'What else?', 'Tell me more', 'How much does it cost?', or 'What plans do you offer?', refer back to the context already discussed and share additional details, features, subscription plans, pricing, or policies from the knowledge base. Never claim you lack context or details about a product or service already introduced in this conversation.\n")
	b.WriteString("6. If a specific factual detail is missing from the knowledge base and the user did not ask for a human, share what you do know about related offerings, explain what detail is not listed, and offer to check with a team member. Set should_handoff to false.\n")
	b.WriteString(fmt.Sprintf("7. When a human handoff is genuinely required, respond politely with: %q. Do not include internal markers in the response field; set should_handoff to true instead.\n", handoffMsg))
	b.WriteString("8. Formatting for WhatsApp: Keep responses concise, warm, and formatted cleanly for WhatsApp/Telegram (under 3-4 sentences when possible). If using bullet points, always start each bullet point on its own new line with a line break before the list.\n")
	b.WriteString("9. If the customer introduces themselves or shares an email address, include the extracted details in lead_info.\n")
	b.WriteString("10. Return ONLY valid JSON with this exact shape: {\"response\":\"...\",\"should_handoff\":false,\"handoff_reason\":\"none\",\"confidence\":0.95,\"lead_info\":null}. Do not include markdown or extra commentary.\n")

	return b.String()
}

// callGemini executes a prompt against Google Gemini REST API
func (s *AIEngineService) callGemini(ctx context.Context, systemPrompt, transcript, incomingText string) (string, error) {
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-1.5-flash"
	}
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", model)

	userPrompt := incomingText
	if transcript != "" {
		userPrompt = fmt.Sprintf("Recent conversation history:\n%s\nCustomer just sent: %s", transcript, incomingText)
	}
	if isPureGreeting(incomingText) {
		userPrompt += "\n\n[Instruction: The customer sent a simple greeting. Greet them warmly and ask how you can help them today. Do not send a product pitch or feature list yet.]"
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

	userContent := incomingText
	if transcript != "" {
		userContent = fmt.Sprintf("Recent context:\n%s\n\nCustomer message: %s", transcript, incomingText)
	}
	if isPureGreeting(incomingText) {
		userContent += "\n\n[Instruction: The customer sent a simple greeting. Greet them warmly and ask how you can help them today. Do not send a product pitch or feature list yet.]"
	}
	if !strings.Contains(strings.ToLower(userContent), "json") {
		userContent = fmt.Sprintf("%s\n\n(Respond strictly with a valid JSON object matching the requested schema)", userContent)
	}

	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userContent,
	})

	groqModel := os.Getenv("GROQ_MODEL")
	if groqModel == "" {
		groqModel = "openai/gpt-oss-120b"
	}

	payload := map[string]interface{}{
		"model":           groqModel,
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

// GenerateEmbedding calls Google Gemini text-embedding-004 to create a 768-dimensional vector.
// It includes an automatic circuit breaker: if rate-limited (HTTP 429) or quota exhausted,
// it opens the circuit for 60 seconds and returns ErrEmbeddingThrottled, enabling callers
// to seamlessly fall back to tsvector full-text search without blocking customer interactions.
func (s *AIEngineService) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if s == nil || s.geminiKey == "" {
		return nil, fmt.Errorf("gemini provider is not configured for embeddings")
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("cannot embed empty text")
	}
	// Limit text length to prevent exceeding token limits
	if len(text) > 4000 {
		text = text[:4000]
	}

	// 1. Circuit breaker check
	s.embeddingMu.RLock()
	throttledUntil := s.embeddingThrottledUntil
	s.embeddingMu.RUnlock()

	if time.Now().Before(throttledUntil) {
		return nil, ErrEmbeddingThrottled
	}

	// 2. Build Gemini embedding request
	url := "https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:embedContent"
	payload := map[string]interface{}{
		"model": "models/text-embedding-004",
		"content": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": text},
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode embedding payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", s.geminiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)

	// 3. Handle rate limits, quota exhaustion, or unauthenticated/unsupported errors (HTTP 429, 503, 404, 401)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable ||
		resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized ||
		strings.Contains(string(respBytes), "RESOURCE_EXHAUSTED") || strings.Contains(string(respBytes), "quota") ||
		strings.Contains(string(respBytes), "not found") || strings.Contains(string(respBytes), "API key not valid") {
		s.embeddingMu.Lock()
		s.embeddingThrottledUntil = time.Now().Add(5 * time.Minute)
		s.embeddingMu.Unlock()
		log.Printf("[AIEngine] ⚠️ Gemini embedding unavailable (status %d). Circuit opened for 5m (falling back to tsvector search)", resp.StatusCode)
		return nil, ErrEmbeddingThrottled
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini embedding error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var geminiResp struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}

	if err := json.Unmarshal(respBytes, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode gemini embedding response: %w", err)
	}

	if len(geminiResp.Embedding.Values) == 0 {
		return nil, fmt.Errorf("empty embedding vector returned by gemini")
	}

	return geminiResp.Embedding.Values, nil
}

// IsEmbeddingAvailable checks if embeddings can currently be generated
func (s *AIEngineService) IsEmbeddingAvailable() bool {
	if s == nil || s.geminiKey == "" {
		return false
	}
	s.embeddingMu.RLock()
	defer s.embeddingMu.RUnlock()
	return !time.Now().Before(s.embeddingThrottledUntil)
}
