package usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

func TestClassifyConsentKeyword(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   domain.ConsentEventType
		wantOK bool
	}{
		{name: "plain stop", input: "STOP", want: domain.ConsentRevoked, wantOK: true},
		{name: "lowercase with punctuation", input: "stop.", want: domain.ConsentRevoked, wantOK: true},
		{name: "with surrounding spaces", input: "  Unsubscribe  ", want: domain.ConsentRevoked, wantOK: true},
		{name: "internal whitespace", input: "STOP   ALL", want: domain.ConsentRevoked, wantOK: true},
		{name: "opt out with hyphen", input: "Opt-Out", want: domain.ConsentRevoked, wantOK: true},
		{name: "optout as one word", input: "optout", want: domain.ConsentRevoked, wantOK: true},
		{name: "keyword inside a sentence", input: "please stop by tomorrow", want: "", wantOK: false},
		{name: "keyword as prefix", input: "stop sending me promos", want: "", wantOK: false},
		{name: "start is opt-in", input: "START", want: domain.ConsentGranted, wantOK: true},
		{name: "unstop is opt-in", input: "unstop", want: domain.ConsentGranted, wantOK: true},
		{name: "unrelated text", input: "hello there", want: "", wantOK: false},
		{name: "empty", input: "", want: "", wantOK: false},
		{name: "emoji only", input: "👍👍", want: "", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ClassifyConsentKeyword(tc.input)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("ClassifyConsentKeyword(%q) = (%q, %v), want (%q, %v)", tc.input, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

type mockConsentRepo struct {
	applied   bool
	err       error
	lastEvent *domain.ConsentEvent
}

func (m *mockConsentRepo) ApplyEvent(ctx context.Context, event *domain.ConsentEvent) (bool, error) {
	m.lastEvent = event
	return m.applied, m.err
}

func (m *mockConsentRepo) LatestForContact(ctx context.Context, tenantID, contactID string) (*domain.ConsentEvent, error) {
	return nil, nil
}

func (m *mockConsentRepo) ListForContacts(ctx context.Context, tenantID string, contactIDs []string) ([]*domain.ConsentEvent, error) {
	return nil, nil
}

type mockConsentContactRepo struct {
	contact *domain.Contact
	err     error
}

func (m *mockConsentContactRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	return m.contact, m.err
}

func (m *mockConsentContactRepo) GetByRoutingValue(ctx context.Context, tenantID, channel, routingValue string) (*domain.Contact, error) {
	return m.contact, m.err
}

func (m *mockConsentContactRepo) Create(ctx context.Context, contact *domain.Contact) error {
	return nil
}

func (m *mockConsentContactRepo) ListByTenant(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
	return nil, nil
}

func (m *mockConsentContactRepo) ListWithFilter(ctx context.Context, tenantID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
	return nil, 0, nil
}

func (m *mockConsentContactRepo) StreamContacts(ctx context.Context, tenantID string, filter domain.ContactFilter, yield func(c *domain.Contact, tagNames string) error) error {
	return nil
}

func (m *mockConsentContactRepo) Anonymize(ctx context.Context, tenantID, id string, deleteAfter time.Time) (*domain.Contact, error) {
	return nil, nil
}

func (m *mockConsentContactRepo) FindAnonymizedExpired(ctx context.Context, now time.Time, limit int) ([]string, error) {
	return nil, nil
}

func (m *mockConsentContactRepo) HardDelete(ctx context.Context, id string) error {
	return nil
}

func TestRecordKeywordIgnoresNonKeywords(t *testing.T) {
	repo := &mockConsentRepo{applied: true}
	uc := NewConsentUseCase(repo, &mockConsentContactRepo{contact: &domain.Contact{ID: "c1"}})

	_, handled, err := uc.RecordKeyword(context.Background(), "t1", "whatsapp", "2348012345678", "wamid.1", "can you send me the price list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handled {
		t.Fatal("expected a normal message to be ignored")
	}
	if repo.lastEvent != nil {
		t.Fatal("no event should be written for a normal message")
	}
}

func TestRecordKeywordRevokesAndStoresIdempotencyKey(t *testing.T) {
	repo := &mockConsentRepo{applied: true}
	uc := NewConsentUseCase(repo, &mockConsentContactRepo{contact: &domain.Contact{ID: "c1"}})

	eventType, handled, err := uc.RecordKeyword(context.Background(), "t1", "whatsapp", "2348012345678", "wamid.abc", "STOP")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled || eventType != domain.ConsentRevoked {
		t.Fatalf("expected revoked/handled, got %q/%v", eventType, handled)
	}
	if repo.lastEvent == nil {
		t.Fatal("expected an event to be written")
	}
	if repo.lastEvent.IdempotencyKey != "inbound:whatsapp:wamid.abc" {
		t.Fatalf("unexpected idempotency key %q", repo.lastEvent.IdempotencyKey)
	}
	if repo.lastEvent.ContactID != "c1" || repo.lastEvent.Source != domain.ConsentSourceInboundKeyword {
		t.Fatalf("unexpected event target/source: %+v", repo.lastEvent)
	}

	var evidence map[string]string
	if err := json.Unmarshal([]byte(repo.lastEvent.Evidence), &evidence); err != nil {
		t.Fatalf("evidence is not valid JSON: %v", err)
	}
	if evidence["keyword"] != "stop" {
		t.Fatalf("expected normalized keyword in evidence, got %q", evidence["keyword"])
	}
}

func TestRecordKeywordWithoutMessageIDIsNotIdempotent(t *testing.T) {
	repo := &mockConsentRepo{applied: true}
	uc := NewConsentUseCase(repo, &mockConsentContactRepo{contact: &domain.Contact{ID: "c1"}})

	if _, _, err := uc.RecordKeyword(context.Background(), "t1", "telegram", "12345", "", "unsubscribe"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.lastEvent.IdempotencyKey != "" {
		t.Fatalf("expected empty idempotency key, got %q", repo.lastEvent.IdempotencyKey)
	}
}

func TestRecordKeywordFailsWhenContactMissing(t *testing.T) {
	uc := NewConsentUseCase(&mockConsentRepo{}, &mockConsentContactRepo{err: domain.ErrContactNotFound})

	if _, _, err := uc.RecordKeyword(context.Background(), "t1", "whatsapp", "2348000000000", "wamid.x", "stop"); err == nil {
		t.Fatal("expected an error when the contact does not exist")
	}
}

func TestRecordDecisionRejectsUnknownType(t *testing.T) {
	uc := NewConsentUseCase(&mockConsentRepo{}, &mockConsentContactRepo{})
	if _, err := uc.RecordDecision(context.Background(), "t1", "c1", "maybe", domain.ConsentSourceOperator, ""); err == nil {
		t.Fatal("expected invalid event type to be rejected")
	}
}
