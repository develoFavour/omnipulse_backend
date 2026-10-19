package service

import (
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
