package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

type erasureContactRepo struct {
	contact             *domain.Contact
	anonymizeFn         func(ctx context.Context, tenantID, id string, deleteAfter time.Time) (*domain.Contact, error)
	receivedDeleteAfter *time.Time
}

func (m *erasureContactRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	return m.contact, nil
}

func (m *erasureContactRepo) GetByRoutingValue(ctx context.Context, tenantID, channel, routingValue string) (*domain.Contact, error) {
	return nil, domain.ErrContactNotFound
}

func (m *erasureContactRepo) Create(ctx context.Context, contact *domain.Contact) error { return nil }

func (m *erasureContactRepo) ListByTenant(ctx context.Context, tenantID, channelFilter string, limit, offset int) ([]*domain.Contact, error) {
	return nil, nil
}

func (m *erasureContactRepo) ListWithFilter(ctx context.Context, tenantID string, filter domain.ContactFilter) ([]*domain.Contact, int, error) {
	return nil, 0, nil
}

func (m *erasureContactRepo) StreamContacts(ctx context.Context, tenantID string, filter domain.ContactFilter, yield func(c *domain.Contact, tagNames string) error) error {
	return nil
}

func (m *erasureContactRepo) Anonymize(ctx context.Context, tenantID, id string, deleteAfter time.Time) (*domain.Contact, error) {
	m.receivedDeleteAfter = &deleteAfter
	if m.anonymizeFn != nil {
		return m.anonymizeFn(ctx, tenantID, id, deleteAfter)
	}
	return m.contact, nil
}

func (m *erasureContactRepo) FindAnonymizedExpired(ctx context.Context, now time.Time, limit int) ([]string, error) {
	return nil, nil
}

func (m *erasureContactRepo) HardDelete(ctx context.Context, id string) error { return nil }

func TestDeleteRecipientAnonymizesAndSetsDeadline(t *testing.T) {
	repo := &erasureContactRepo{contact: &domain.Contact{ID: "contact-1", TenantID: "tenant-1", Channel: "whatsapp"}}
	consent := &importConsentRepo{}
	uc := NewContactUseCase(repo, nil, nil, consent)

	before := time.Now()
	got, err := uc.DeleteRecipient(context.Background(), "tenant-1", "contact-1")
	if err != nil {
		t.Fatalf("DeleteRecipient returned error: %v", err)
	}
	if got != repo.contact {
		t.Fatalf("expected the anonymized contact to be returned, got %+v", got)
	}

	if len(consent.events) != 1 {
		t.Fatalf("expected one consent ledger event, got %d", len(consent.events))
	}
	event := consent.events[0]
	if event.EventType != domain.ConsentRevoked {
		t.Fatalf("erasure must be recorded as a revocation, got %q", event.EventType)
	}
	if event.Source != domain.ConsentSourceOperator {
		t.Fatalf("erasure event source = %q, want operator", event.Source)
	}
	if event.ContactID != "contact-1" || event.TenantID != "tenant-1" {
		t.Fatalf("erasure event misaddressed: %+v", event)
	}
	if !strings.Contains(event.Evidence, "erasure_request") {
		t.Fatalf("erasure evidence should state its basis, got %s", event.Evidence)
	}

	if repo.receivedDeleteAfter == nil {
		t.Fatal("anonymize was not wired with a delete deadline")
	}
	wantMin := before.Add(domain.AnonymizedRetention)
	wantMax := time.Now().Add(domain.AnonymizedRetention)
	if repo.receivedDeleteAfter.Before(wantMin) || repo.receivedDeleteAfter.After(wantMax) {
		t.Fatalf("delete deadline %v is not ~%v out", *repo.receivedDeleteAfter, domain.AnonymizedRetention)
	}
}

func TestDeleteRecipientRejectsBlankID(t *testing.T) {
	uc := NewContactUseCase(&erasureContactRepo{}, nil, nil, nil)
	if _, err := uc.DeleteRecipient(context.Background(), "tenant-1", "  "); !errors.Is(err, domain.ErrInvalidContact) {
		t.Fatalf("blank ID should fail validation, got %v", err)
	}
}

func TestDeleteRecipientPropagatesNotFound(t *testing.T) {
	repo := &erasureContactRepo{
		anonymizeFn: func(ctx context.Context, tenantID, id string, deleteAfter time.Time) (*domain.Contact, error) {
			return nil, domain.ErrContactNotFound
		},
	}
	uc := NewContactUseCase(repo, nil, nil, nil)
	if _, err := uc.DeleteRecipient(context.Background(), "tenant-1", "missing"); !errors.Is(err, domain.ErrContactNotFound) {
		t.Fatalf("expected ErrContactNotFound, got %v", err)
	}
}
