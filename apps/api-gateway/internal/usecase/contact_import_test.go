package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type importContactRepo struct {
	contacts  map[string]*domain.Contact
	created   []*domain.Contact
	total     int
	createErr error
}

func newImportContactRepo(existing ...*domain.Contact) *importContactRepo {
	repo := &importContactRepo{contacts: map[string]*domain.Contact{}, total: len(existing)}
	for _, c := range existing {
		repo.contacts[c.Channel+"|"+c.RoutingValue] = c
	}
	return repo
}

func importKey(channel, routing string) string { return channel + "|" + routing }

func (m *importContactRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	for _, c := range m.contacts {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, domain.ErrContactNotFound
}

func (m *importContactRepo) GetByRoutingValue(ctx context.Context, tenantID, channel, routingValue string) (*domain.Contact, error) {
	if c, ok := m.contacts[importKey(channel, routingValue)]; ok {
		return c, nil
	}
	return nil, domain.ErrContactNotFound
}

func (m *importContactRepo) Create(ctx context.Context, contact *domain.Contact) error {
	if m.createErr != nil {
		return m.createErr
	}
	key := importKey(contact.Channel, contact.RoutingValue)
	if existing, ok := m.contacts[key]; ok {
		contact.ID = existing.ID
		return nil
	}
	contact.ID = "contact-" + contact.RoutingValue
	m.contacts[key] = contact
	m.total++
	m.created = append(m.created, contact)
	return nil
}

func (m *importContactRepo) ListByTenant(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
	return nil, nil
}

func (m *importContactRepo) ListWithFilter(ctx context.Context, tenantID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
	return nil, m.total, nil
}

func (m *importContactRepo) StreamContacts(ctx context.Context, tenantID string, filter domain.ContactFilter, yield func(c *domain.Contact, tagNames string) error) error {
	return nil
}

func (m *importContactRepo) Anonymize(ctx context.Context, tenantID, id string, deleteAfter time.Time) (*domain.Contact, error) {
	return nil, nil
}

func (m *importContactRepo) FindAnonymizedExpired(ctx context.Context, now time.Time, limit int) ([]string, error) {
	return nil, nil
}

func (m *importContactRepo) HardDelete(ctx context.Context, id string) error {
	return nil
}

type importConsentRepo struct {
	events []*domain.ConsentEvent
	err    error
}

func (m *importConsentRepo) ApplyEvent(ctx context.Context, event *domain.ConsentEvent) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	m.events = append(m.events, event)
	return true, nil
}

func (m *importConsentRepo) LatestForContact(ctx context.Context, tenantID, contactID string) (*domain.ConsentEvent, error) {
	return nil, nil
}

func (m *importConsentRepo) ListForContacts(ctx context.Context, tenantID string, contactIDs []string) ([]*domain.ConsentEvent, error) {
	return nil, nil
}

func newImportFixture(repo *importContactRepo, consent *importConsentRepo, sub *mockSubscriptionRepo) *ContactUseCase {
	// A typed nil pointer would satisfy the interface check while panicking on
	// first use, so only assign when the fixture actually wants a subscription.
	var subRepo domain.SubscriptionRepository
	if sub != nil {
		subRepo = sub
	}
	return NewContactUseCase(repo, nil, subRepo, consent).(*ContactUseCase)
}

func TestImportContactsRecordsPerRowConsent(t *testing.T) {
	repo := newImportContactRepo()
	consent := &importConsentRepo{}
	uc := newImportFixture(repo, consent, nil)

	summary, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{
		DefaultChannel: "whatsapp",
		Rows: []domain.ContactImportRow{
			{FirstName: "Ada", RoutingValue: "+234 801-234-5678", Consent: "yes"},
			{FirstName: "Bola", RoutingValue: "2348023456789", Consent: "opted_out"},
			{FirstName: "Chidi", RoutingValue: "2348034567890"},
			{FirstName: "Dup", RoutingValue: "2348034567890"},
			{FirstName: "Broken", RoutingValue: "123"},
		},
	})
	if err != nil {
		t.Fatalf("ImportContacts returned error: %v", err)
	}

	if summary.Total != 5 || summary.Created != 3 || summary.Duplicates != 1 || summary.Invalid != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.ConsentGranted != 1 || summary.ConsentRevoked != 1 || summary.NoConsent != 1 {
		t.Fatalf("unexpected consent counts: %+v", summary)
	}
	if len(summary.Errors) != 1 || summary.Errors[0].Row != 5 {
		t.Fatalf("expected one error on row 5, got %+v", summary.Errors)
	}
	if got := repo.contacts[importKey("whatsapp", "2348012345678")]; got == nil || got.FirstName != "Ada" {
		t.Fatalf("expected Ada to be stored with normalized routing, got %+v", got)
	}
	if len(consent.events) != 2 {
		t.Fatalf("expected two consent events, got %d", len(consent.events))
	}
	for _, event := range consent.events {
		if event.Source != domain.ConsentSourceCSVImport {
			t.Fatalf("expected csv_import source, got %q", event.Source)
		}
		if !strings.Contains(event.Evidence, "csv_import") {
			t.Fatalf("expected evidence to name the basis, got %q", event.Evidence)
		}
	}
}

func TestImportContactsNeverOverridesAnOptOut(t *testing.T) {
	optedOut := &domain.Contact{
		ID: "c-opted", TenantID: "tenant-1", FirstName: "Bola",
		Channel: "whatsapp", RoutingValue: "2348023456789", Status: "opted_out",
	}
	repo := newImportContactRepo(optedOut)
	consent := &importConsentRepo{}
	uc := newImportFixture(repo, consent, nil)

	grantAttempt, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{
		DefaultChannel: "whatsapp",
		Rows:           []domain.ContactImportRow{{RoutingValue: "2348023456789", Consent: "granted"}},
	})
	if err != nil {
		t.Fatalf("ImportContacts returned error: %v", err)
	}
	if grantAttempt.Invalid != 1 || grantAttempt.Existing != 0 {
		t.Fatalf("expected the grant to be rejected, got %+v", grantAttempt)
	}
	if len(consent.events) != 0 {
		t.Fatalf("expected no consent event for a rejected grant, got %+v", consent.events)
	}
	if !strings.Contains(grantAttempt.Errors[0].Message, "previously opted out") {
		t.Fatalf("expected an explanatory opt-out error, got %q", grantAttempt.Errors[0].Message)
	}

	revokeAttempt, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{
		DefaultChannel: "whatsapp",
		Rows:           []domain.ContactImportRow{{RoutingValue: "2348023456789", Consent: "revoked"}},
	})
	if err != nil {
		t.Fatalf("ImportContacts returned error: %v", err)
	}
	if revokeAttempt.Existing != 1 || revokeAttempt.Invalid != 0 {
		t.Fatalf("expected the revocation to be applied to the existing contact, got %+v", revokeAttempt)
	}
	if len(consent.events) != 1 || consent.events[0].EventType != domain.ConsentRevoked {
		t.Fatalf("expected one revocation event, got %+v", consent.events)
	}
}

func TestImportContactsQuotaCountsOnlyNewRecipients(t *testing.T) {
	existing := &domain.Contact{ID: "c1", TenantID: "tenant-1", FirstName: "Ada", Channel: "telegram", RoutingValue: "111", Status: "active"}
	sub := &mockSubscriptionRepo{
		getByTenantIDFunc: func(ctx context.Context, tenantID string) (*domain.Subscription, error) {
			return &domain.Subscription{TenantID: tenantID, PlanTier: domain.PlanStarter, ContactsLimit: 2}, nil
		},
	}

	t.Run("rejects the whole import when new rows exceed capacity", func(t *testing.T) {
		repo := newImportContactRepo(existing)
		repo.total = 2
		uc := newImportFixture(repo, &importConsentRepo{}, sub)

		_, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{
			DefaultChannel: "telegram",
			Rows: []domain.ContactImportRow{
				{RoutingValue: "222"},
				{RoutingValue: "333"},
			},
		})
		if !errors.Is(err, domain.ErrQuotaExceeded) {
			t.Fatalf("expected ErrQuotaExceeded, got %v", err)
		}
		if len(repo.created) != 0 {
			t.Fatalf("expected no writes when the quota check fails, got %d", len(repo.created))
		}
	})

	t.Run("allows updating existing recipients at the limit", func(t *testing.T) {
		repo := newImportContactRepo(existing)
		repo.total = 2
		uc := newImportFixture(repo, &importConsentRepo{}, sub)

		summary, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{
			DefaultChannel: "telegram",
			Rows:           []domain.ContactImportRow{{RoutingValue: "111"}},
		})
		if err != nil {
			t.Fatalf("ImportContacts returned error: %v", err)
		}
		if summary.Existing != 1 || summary.Created != 0 {
			t.Fatalf("expected an existing recipient, got %+v", summary)
		}
	})
}

func TestImportContactsDefaults(t *testing.T) {
	repo := newImportContactRepo()
	consent := &importConsentRepo{}
	uc := newImportFixture(repo, consent, nil)

	summary, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{
		DefaultChannel: "WhatsApp",
		DefaultConsent: "granted",
		ConsentNote:    "Signed up via the October landing page",
		Rows:           []domain.ContactImportRow{{RoutingValue: "0044 7700 900123"}},
	})
	if err != nil {
		t.Fatalf("ImportContacts returned error: %v", err)
	}
	if summary.Created != 1 || summary.ConsentGranted != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	stored := repo.contacts[importKey("whatsapp", "447700900123")]
	if stored == nil {
		t.Fatalf("expected the 00 prefix to be stripped and digits kept, got %+v", repo.contacts)
	}
	if stored.FirstName != "447700900123" {
		t.Fatalf("expected the routing value as a fallback name, got %q", stored.FirstName)
	}
	if len(consent.events) != 1 || consent.events[0].Channel != "whatsapp" {
		t.Fatalf("unexpected consent event: %+v", consent.events)
	}
	if !strings.Contains(consent.events[0].Evidence, "Signed up via the October landing page") {
		t.Fatalf("expected the evidence note to be retained, got %q", consent.events[0].Evidence)
	}
}

func TestImportContactsRejectsUnusableRequests(t *testing.T) {
	uc := newImportFixture(newImportContactRepo(), &importConsentRepo{}, nil)

	if _, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{}); !errors.Is(err, domain.ErrInvalidContact) {
		t.Fatalf("expected an error for an empty import, got %v", err)
	}

	rows := make([]domain.ContactImportRow, domain.ContactImportMaxRows+1)
	for i := range rows {
		rows[i] = domain.ContactImportRow{RoutingValue: "2348000000000"}
	}
	if _, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{
		DefaultChannel: "whatsapp",
		Rows:           rows,
	}); !errors.Is(err, domain.ErrInvalidContact) {
		t.Fatalf("expected an error for an oversized import, got %v", err)
	}

	if _, err := uc.ImportContacts(context.Background(), "tenant-1", domain.ContactImportRequest{
		DefaultChannel: "carrier-pigeon",
		Rows:           []domain.ContactImportRow{{RoutingValue: "1"}},
	}); !errors.Is(err, domain.ErrInvalidContact) {
		t.Fatalf("expected an error for an unknown default channel, got %v", err)
	}
}
