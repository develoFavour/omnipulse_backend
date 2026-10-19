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

		// Check day of week (0 = Sunday, 1 = Monday, ..., 6 = Saturday)
		weekday := int(now.Weekday())
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

		if currentMinutes < startMinutes || currentMinutes >= endMinutes {
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
) (reply string, needsHuman bool, err error) {
	if !s.HasActiveProvider() {
		return "", false, fmt.Errorf("no AI provider configured (set GEMINI_API_KEY or GROQ_API_KEY)")
	}

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

	// 1. Try Gemini 1.5 Flash if configured
	if s.geminiKey != "" {
		reply, err = s.callGemini(ctx, systemPrompt, transcript, incomingText)
		if err == nil && reply != "" {
			return s.postProcessReply(reply)
		}
		log.Printf("[AIEngine] ⚠️ Gemini call failed: %v. Attempting Groq fallback...\n", err)
	}

	// 2. Fallback to Groq (Llama 3.3 70B) if configured
	if s.groqKey != "" {
		reply, err = s.callGroq(ctx, systemPrompt, transcript, incomingText)
		if err == nil && reply != "" {
			return s.postProcessReply(reply)
		}
		log.Printf("[AIEngine] ⚠️ Groq fallback failed: %v\n", err)
	}

	if reply == "" {
		return "", false, fmt.Errorf("all configured AI providers failed to generate a response")
	}

	return s.postProcessReply(reply)
}

func (s *AIEngineService) postProcessReply(rawReply string) (string, bool, error) {
	needsHuman := false
	cleaned := rawReply

	if strings.Contains(cleaned, "[HANDOFF_TO_HUMAN]") {
		needsHuman = true
		cleaned = strings.ReplaceAll(cleaned, "[HANDOFF_TO_HUMAN]", "")
	}

	cleaned = strings.TrimSpace(cleaned)
	return cleaned, needsHuman, nil
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
	b.WriteString(fmt.Sprintf("2. If the user asks something not covered in your knowledge base, or asks to speak with a human/agent/representative, politely respond with: %q and append [HANDOFF_TO_HUMAN] at the end.\n", handoffMsg))
	b.WriteString("3. Keep responses concise and formatted cleanly for WhatsApp/Telegram (under 3-4 sentences when possible). Use bullet points for lists.\n")

	return b.String()
}

// callGemini executes a prompt against Google Gemini REST API
func (s *AIEngineService) callGemini(ctx context.Context, systemPrompt, transcript, incomingText string) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent?key=%s", s.geminiKey)

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
			"temperature":     0.3,
			"maxOutputTokens": 600,
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
		"model":       "openai/gpt-oss-120b",
		"messages":    messages,
		"temperature": 0.3,
		"max_tokens":  600,
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
