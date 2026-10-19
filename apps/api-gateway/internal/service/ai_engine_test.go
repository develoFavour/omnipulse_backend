package service

import (
	"strings"
	"testing"

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
