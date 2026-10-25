package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

func TestPostProcessReplyValidatesStructuredJSON(t *testing.T) {
	engine := &AIEngineService{}
	reply, handoff, lead, err := engine.postProcessReply(`{"response":"We deliver nationwide.","should_handoff":false,"handoff_reason":"none","confidence":0.94,"lead_info":{"name":"Ada","email":"ada@example.com"}}`)
	if err != nil {
		t.Fatalf("postProcessReply returned error: %v", err)
	}
	if reply != "We deliver nationwide." || handoff || lead == nil || lead.Name != "Ada" {
		t.Fatalf("unexpected parsed response: reply=%q handoff=%v lead=%+v", reply, handoff, lead)
	}
}

func TestPostProcessReplyStripsInternalHandoffMarker(t *testing.T) {
	engine := &AIEngineService{}
	reply, handoff, _, err := engine.postProcessReply(`{"response":"I am transferring you now. [HANDOFF_TO_HUMAN]","should_handoff":true,"handoff_reason":"requested_agent","confidence":0.91}`)
	if err != nil {
		t.Fatalf("postProcessReply returned error: %v", err)
	}
	if reply != "I am transferring you now." || !handoff {
		t.Fatalf("unexpected sanitized handoff: reply=%q handoff=%v", reply, handoff)
	}
}

func TestPostProcessReplyRejectsUnstructuredText(t *testing.T) {
	engine := &AIEngineService{}
	if _, _, _, err := engine.postProcessReply("I think we deliver nationwide."); err == nil {
		t.Fatal("expected unstructured provider output to be rejected")
	}
}

func TestRankKnowledgeItemsReturnsRelevantBoundedItems(t *testing.T) {
	items := []domain.AIKnowledgeItem{
		{QuestionOrTopic: "Delivery fees", AnswerOrContent: "Lagos delivery costs 2000", IsActive: true},
		{QuestionOrTopic: "Opening hours", AnswerOrContent: "We open at 9am", IsActive: true},
	}
	got := rankKnowledgeItems(items, "What is the delivery fee?", 1)
	if len(got) != 1 || got[0].QuestionOrTopic != "Delivery fees" {
		t.Fatalf("unexpected ranked knowledge items: %+v", got)
	}
}

func TestRankKnowledgeItemsMatchesCategory(t *testing.T) {
	items := []domain.AIKnowledgeItem{
		{Category: "product", QuestionOrTopic: "Biteselah", AnswerOrContent: "A fintech platform.", IsActive: true},
		{Category: "policy", QuestionOrTopic: "Refunds", AnswerOrContent: "Refunds are reviewed by the team.", IsActive: true},
	}
	got := rankKnowledgeItems(items, "Tell me about your product", 1)
	if len(got) != 1 || got[0].Category != "product" {
		t.Fatalf("expected product category to be matched, got %+v", got)
	}
}

func TestRankKnowledgeItemsBackfillsWhenQueryHasNoTokens(t *testing.T) {
	items := []domain.AIKnowledgeItem{
		{ID: "1", Category: "faq", QuestionOrTopic: "Company Overview", AnswerOrContent: "MessageRail is an omnichannel platform.", IsActive: true},
		{ID: "2", Category: "pricing", QuestionOrTopic: "Starter Plan", AnswerOrContent: "Costs $29/mo.", IsActive: true},
	}
	// Conversational follow-up where query has only stop words / conversational particles
	got := rankKnowledgeItems(items, "Is that all?", 2)
	if len(got) != 2 {
		t.Fatalf("expected backfill to return all active items, got %d", len(got))
	}
}

func TestRankKnowledgeItemsProductIntentBoost(t *testing.T) {
	items := []domain.AIKnowledgeItem{
		{ID: "1", Category: "faq", QuestionOrTopic: "Company Overview", AnswerOrContent: "MessageRail is an omnichannel platform.", IsActive: true},
		{ID: "2", Category: "hours", QuestionOrTopic: "Support Hours", AnswerOrContent: "8am to 6pm.", IsActive: true},
	}
	got := rankKnowledgeItems(items, "Tell me about your product?", 1)
	if len(got) != 1 || got[0].QuestionOrTopic != "Company Overview" {
		t.Fatalf("expected Company Overview to be boosted, got %+v", got)
	}
}

func TestChunkKnowledgeTextPreservesDocumentContent(t *testing.T) {
	text := strings.Repeat("A factual sentence with important business details. ", 300)
	chunks, err := chunkKnowledgeText(text, 100, 10)
	if err != nil {
		t.Fatalf("chunkKnowledgeText returned error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 100 {
			t.Fatalf("chunk exceeded maximum size: %d", len(chunk))
		}
	}
	joined := strings.Join(chunks, " ")
	if !strings.Contains(joined, "important business details") {
		t.Fatal("chunking lost document content")
	}
}

func TestMergeKnowledgeItemsRemovesOnlyExactDuplicates(t *testing.T) {
	items := mergeKnowledgeItems([]domain.AIKnowledgeItem{
		{Category: "pricing", QuestionOrTopic: "Delivery", AnswerOrContent: "Lagos: N2,000"},
		{Category: "pricing", QuestionOrTopic: "Delivery", AnswerOrContent: "Lagos: N2,000"},
		{Category: "pricing", QuestionOrTopic: "Delivery", AnswerOrContent: "Nationwide: N4,000"},
		{Category: "", QuestionOrTopic: "Hours", AnswerOrContent: "9am to 5pm"},
	})
	if len(items) != 3 {
		t.Fatalf("expected 3 merged items, got %d", len(items))
	}
	if items[2].Category != "faq" {
		t.Fatalf("expected missing category to default to faq, got %q", items[2].Category)
	}
}

func TestEmbeddingCircuitBreakerRejectsWhenThrottled(t *testing.T) {
	engine := &AIEngineService{
		geminiKey:               "test-key",
		embeddingThrottledUntil: time.Now().Add(30 * time.Second),
	}

	if engine.IsEmbeddingAvailable() {
		t.Fatal("expected IsEmbeddingAvailable to be false while circuit is open")
	}

	_, err := engine.GenerateEmbedding(context.Background(), "test question")
	if err != ErrEmbeddingThrottled {
		t.Fatalf("expected ErrEmbeddingThrottled, got: %v", err)
	}
}

func TestEmbeddingRejectsEmptyText(t *testing.T) {
	engine := &AIEngineService{
		geminiKey: "test-key",
	}

	_, err := engine.GenerateEmbedding(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected error when embedding empty text")
	}
}

func TestParseExtractedKnowledgeJSONHandlesSingleObject(t *testing.T) {
	// The exact Groq response shape that previously caused a failure
	raw := `{
    "category": "pricing",
    "question_or_topic": "What is the price and feature limit of the Starter Plan?",
    "answer_or_content": "Starter Plan costs $29 per month, includes up to 1,000 monthly active conversations, 1 connected WhatsApp Business number, 1 Telegram bot, a standard AI knowledge base with up to 50 curated business facts, and 5 team member seats."
  }`
	items, err := parseExtractedKnowledgeJSON(raw)
	if err != nil {
		t.Fatalf("parseExtractedKnowledgeJSON returned error for single object: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Category != "pricing" || !strings.Contains(items[0].AnswerOrContent, "$29") {
		t.Fatalf("unexpected parsed item: %+v", items[0])
	}
}

func TestParseExtractedKnowledgeJSONHandlesWrappedItems(t *testing.T) {
	raw := `{"items": [
		{"category": "hours", "question_or_topic": "What are your business hours?", "answer_or_content": "Mon-Fri 8am-6pm WAT."}
	]}`
	items, err := parseExtractedKnowledgeJSON(raw)
	if err != nil {
		t.Fatalf("parseExtractedKnowledgeJSON returned error for wrapped items: %v", err)
	}
	if len(items) != 1 || items[0].Category != "hours" {
		t.Fatalf("unexpected parsed items: %+v", items)
	}
}

func TestParseExtractedKnowledgeJSONHandlesDirectArray(t *testing.T) {
	raw := `[
		{"category": "policy", "question_or_topic": "What is your refund policy?", "answer_or_content": "14-day money-back guarantee."}
	]`
	items, err := parseExtractedKnowledgeJSON(raw)
	if err != nil {
		t.Fatalf("parseExtractedKnowledgeJSON returned error for direct array: %v", err)
	}
	if len(items) != 1 || items[0].Category != "policy" {
		t.Fatalf("unexpected parsed items: %+v", items)
	}
}
