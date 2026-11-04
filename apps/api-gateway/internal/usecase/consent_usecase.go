package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"omnipulse/apps/api-gateway/internal/domain"
)

// Opt-out keywords follow the WhatsApp Business opt-out convention. Matching is
// on the whole normalized message so "stop by tomorrow" is not a revocation.
var optOutKeywords = map[string]bool{
	"stop":        true,
	"stop all":    true,
	"unsubscribe": true,
	"cancel":      true,
	"end":         true,
	"quit":        true,
	"opt out":     true,
	"optout":      true,
}

var optInKeywords = map[string]bool{
	"start":  true,
	"unstop": true,
	"resume": true,
}

type ConsentUseCase struct {
	repo        domain.ConsentRepository
	contactRepo domain.ContactRepository
}

func NewConsentUseCase(repo domain.ConsentRepository, contactRepo domain.ContactRepository) *ConsentUseCase {
	return &ConsentUseCase{repo: repo, contactRepo: contactRepo}
}

// ClassifyConsentKeyword returns the consent decision expressed by a message,
// if any. It is intentionally strict: only an exact keyword triggers a change.
func ClassifyConsentKeyword(text string) (domain.ConsentEventType, bool) {
	normalized := normalizeKeyword(text)
	if optOutKeywords[normalized] {
		return domain.ConsentRevoked, true
	}
	if optInKeywords[normalized] {
		return domain.ConsentGranted, true
	}
	return "", false
}

func normalizeKeyword(text string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		case unicode.IsSpace(r) || r == '-' || r == '_':
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// RecordKeyword turns an inbound message into a consent event. Returns the
// decision and whether it was newly recorded; false means "not a keyword".
func (u *ConsentUseCase) RecordKeyword(ctx context.Context, tenantID, channel, routingValue, messageID, text string) (domain.ConsentEventType, bool, error) {
	eventType, ok := ClassifyConsentKeyword(text)
	if !ok {
		return "", false, nil
	}

	contact, err := u.contactRepo.GetByRoutingValue(ctx, tenantID, channel, routingValue)
	if err != nil {
		return eventType, false, err
	}
	if contact == nil {
		return eventType, false, domain.ErrConsentContactNotFound
	}

	evidence, _ := json.Marshal(map[string]string{
		"keyword":    normalizeKeyword(text),
		"message_id": messageID,
		"channel":    channel,
		"text":       truncate(text, 280),
	})

	var idempotencyKey string
	if messageID != "" {
		idempotencyKey = fmt.Sprintf("inbound:%s:%s", channel, messageID)
	}

	applied, err := u.repo.ApplyEvent(ctx, &domain.ConsentEvent{
		TenantID:       tenantID,
		ContactID:      contact.ID,
		EventType:      eventType,
		Source:         domain.ConsentSourceInboundKeyword,
		Channel:        channel,
		Evidence:       string(evidence),
		IdempotencyKey: idempotencyKey,
	})
	return eventType, applied, err
}

// RecordDecision records an operator or API-driven consent change.
func (u *ConsentUseCase) RecordDecision(ctx context.Context, tenantID, contactID string, eventType domain.ConsentEventType, source domain.ConsentSource, evidence string) (bool, error) {
	if eventType != domain.ConsentGranted && eventType != domain.ConsentRevoked {
		return false, fmt.Errorf("invalid consent event type %q", eventType)
	}
	if evidence == "" {
		evidence = "{}"
	}
	return u.repo.ApplyEvent(ctx, &domain.ConsentEvent{
		TenantID:  tenantID,
		ContactID: contactID,
		EventType: eventType,
		Source:    source,
		Evidence:  evidence,
	})
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
