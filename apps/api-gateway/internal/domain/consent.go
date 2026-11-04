package domain

import (
	"context"
	"errors"
	"time"
)

var ErrConsentContactNotFound = errors.New("consent target contact not found")

type ConsentEventType string

const (
	ConsentGranted ConsentEventType = "granted"
	ConsentRevoked ConsentEventType = "revoked"
)

type ConsentSource string

const (
	ConsentSourceInboundKeyword ConsentSource = "inbound_keyword"
	ConsentSourceWebForm        ConsentSource = "web_form"
	ConsentSourceCSVImport      ConsentSource = "csv_import"
	ConsentSourceOperator       ConsentSource = "operator"
	ConsentSourceAPI            ConsentSource = "api"
)

// ConsentEvent is an immutable record of a consent decision. It never carries
// current state; contacts.status is the projection of the most recent event.
type ConsentEvent struct {
	ID             string           `json:"id"`
	TenantID       string           `json:"tenant_id"`
	ContactID      string           `json:"contact_id"`
	EventType      ConsentEventType `json:"event_type"`
	Source         ConsentSource    `json:"source"`
	Channel        string           `json:"channel"`
	Evidence       string           `json:"evidence"`
	OccurredAt     time.Time        `json:"occurred_at"`
	RecordedAt     time.Time        `json:"recorded_at"`
	IdempotencyKey string           `json:"idempotency_key,omitempty"`
}

// ConsentRepository stores the append-only ledger and projects it onto contacts.
type ConsentRepository interface {
	// ApplyEvent inserts the event and, when it is the most recent event for the
	// contact, updates contacts.status. Returns false when the event was a
	// duplicate replay and nothing was written.
	ApplyEvent(ctx context.Context, event *ConsentEvent) (bool, error)
	LatestForContact(ctx context.Context, tenantID, contactID string) (*ConsentEvent, error)
	ListForContacts(ctx context.Context, tenantID string, contactIDs []string) ([]*ConsentEvent, error)
}

// ConsentUseCase turns a human signal (keyword, operator action, API call) into
// a ledger entry for the right contact.
type ConsentUseCase interface {
	RecordKeyword(ctx context.Context, tenantID, channel, routingValue, messageID, text string) (ConsentEventType, bool, error)
	RecordDecision(ctx context.Context, tenantID, contactID string, eventType ConsentEventType, source ConsentSource, evidence string) (bool, error)
}
