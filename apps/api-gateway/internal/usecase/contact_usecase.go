package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
)

const importErrorLimit = 50

// ContactUseCase implements domain.ContactUseCase and orchestrates the business rules
type ContactUseCase struct {
	repo        domain.ContactRepository
	tagRepo     domain.TagRepository
	subRepo     domain.SubscriptionRepository
	consentRepo domain.ConsentRepository
}

// NewContactUseCase injects our driven database port interface, tag repository, subscription repository, and consent ledger
func NewContactUseCase(repo domain.ContactRepository, tagRepo domain.TagRepository, subRepo domain.SubscriptionRepository, consentRepo domain.ConsentRepository) domain.ContactUseCase {
	return &ContactUseCase{
		repo:        repo,
		tagRepo:     tagRepo,
		subRepo:     subRepo,
		consentRepo: consentRepo,
	}
}

// FetchContact orchestrates the retrieval of an audience profile
func (u *ContactUseCase) FetchContact(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: contact ID cannot be blank", domain.ErrInvalidContact)
	}

	contact, err := u.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	if u.tagRepo != nil && contact != nil {
		tagMap, err := u.tagRepo.GetTagsByContactIDs(ctx, tenantID, []string{contact.ID})
		if err == nil {
			if tags, ok := tagMap[contact.ID]; ok {
				contact.Tags = tags
			} else {
				contact.Tags = []*domain.Tag{}
			}
		}
	}

	return contact, nil
}

// GetContactByRoutingValue retrieves a contact by tenant, channel, and destination routing value
func (u *ContactUseCase) GetContactByRoutingValue(ctx context.Context, tenantID, channel, routingValue string) (*domain.Contact, error) {
	return u.repo.GetByRoutingValue(ctx, tenantID, channel, routingValue)
}

// RegisterContact enforces data validation invariants and plan capacity limits before saving a user profile
func (u *ContactUseCase) RegisterContact(ctx context.Context, c *domain.Contact) error {
	c.FirstName = strings.TrimSpace(c.FirstName)
	if c.FirstName == "" {
		return fmt.Errorf("%w: first name is a mandatory field", domain.ErrInvalidContact)
	}

	c.Channel = strings.TrimSpace(strings.ToLower(c.Channel))
	if c.Channel != "whatsapp" && c.Channel != "telegram" && c.Channel != "instagram" && c.Channel != "x" {
		return fmt.Errorf("%w: channel must be whatsapp, telegram, instagram, or x", domain.ErrInvalidContact)
	}

	c.RoutingValue = strings.TrimSpace(c.RoutingValue)
	if c.RoutingValue == "" {
		return fmt.Errorf("%w: routing value cannot be empty", domain.ErrInvalidContact)
	}

	// Check if contact already exists before enforcing new contact capacity limit
	existing, _ := u.repo.GetByRoutingValue(ctx, c.TenantID, c.Channel, c.RoutingValue)
	if existing != nil {
		c.ID = existing.ID
	} else if u.subRepo != nil && c.TenantID != "" {
		// Enforce Plan Contact Limit only for brand new contacts
		sub, err := u.subRepo.GetByTenantID(ctx, c.TenantID)
		if err == nil && sub != nil && sub.ContactsLimit != -1 {
			_, total, err := u.repo.ListWithFilter(ctx, c.TenantID, domain.ContactFilter{Page: 1, PageSize: 1, Limit: 1})
			if err == nil && total >= sub.ContactsLimit {
				return fmt.Errorf("%w: contact capacity limit of %d reached for %s plan. Upgrade to increase capacity.", domain.ErrQuotaExceeded, sub.ContactsLimit, sub.PlanTier)
			}
		}
	}

	if c.Source == "" {
		c.Source = "manual"
	}
	c.Status = "active"

	// Commit pure sanitized domain model downstream to the database layer
	return u.repo.Create(ctx, c)
}

// GetAllContacts computes the pagination boundaries and filters for mass reads
func (u *ContactUseCase) GetAllContacts(ctx context.Context, tenantID string, filter domain.ContactFilter) (*domain.PaginatedContacts, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 500 {
		filter.PageSize = 50
	}

	filter.Limit = filter.PageSize
	filter.Offset = (filter.Page - 1) * filter.PageSize

	contacts, total, err := u.repo.ListWithFilter(ctx, tenantID, filter)
	if err != nil {
		return nil, err
	}

	if u.tagRepo != nil && len(contacts) > 0 {
		contactIDs := make([]string, len(contacts))
		for i, c := range contacts {
			contactIDs[i] = c.ID
			c.Tags = []*domain.Tag{} // default empty slice instead of null
		}

		tagMap, err := u.tagRepo.GetTagsByContactIDs(ctx, tenantID, contactIDs)
		if err == nil {
			for _, c := range contacts {
				if tags, ok := tagMap[c.ID]; ok {
					c.Tags = tags
				}
			}
		}
	}

	totalPages := 0
	if total > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(filter.PageSize)))
	}

	return &domain.PaginatedContacts{
		Data:       contacts,
		Total:      total,
		Page:       filter.Page,
		PageSize:   filter.PageSize,
		TotalPages: totalPages,
	}, nil
}

func (u *ContactUseCase) StreamContacts(ctx context.Context, tenantID string, filter domain.ContactFilter, yield func(c *domain.Contact, tagNames string) error) error {
	return u.repo.StreamContacts(ctx, tenantID, filter, yield)
}

// DeleteRecipient erases a recipient. Their PII is blanked immediately, they
// are suppressed from every future send, and the row survives in anonymized
// form until the retention sweep hard-deletes it — delivery history included
// in reports survives even that, because the sweep unlinks rather than
// cascades delivery rows.
func (u *ContactUseCase) DeleteRecipient(ctx context.Context, tenantID, id string) (*domain.Contact, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: contact ID cannot be blank", domain.ErrInvalidContact)
	}

	deleteAfter := time.Now().Add(domain.AnonymizedRetention)
	contact, err := u.repo.Anonymize(ctx, tenantID, id, deleteAfter)
	if err != nil {
		return nil, err
	}

	// The ledger only knows grant/revoke. Erasure lands as a revocation so the
	// suppression is auditable; a failed ledger write must not block the
	// erasure itself.
	if u.consentRepo != nil {
		evidence, _ := json.Marshal(map[string]interface{}{
			"basis":        "erasure_request",
			"anonymized":   true,
			"delete_after": deleteAfter.UTC().Format(time.RFC3339),
		})
		_, _ = u.consentRepo.ApplyEvent(ctx, &domain.ConsentEvent{
			TenantID:  tenantID,
			ContactID: contact.ID,
			EventType: domain.ConsentRevoked,
			Source:    domain.ConsentSourceOperator,
			Channel:   contact.Channel,
			Evidence:  string(evidence),
		})
	}

	return contact, nil
}

// parseImportConsent maps the vocabulary people use in exported lists onto the
// consent ledger. An empty decision means "no evidence supplied", which is
// deliberately distinct from a grant or revocation.
func parseImportConsent(raw string) (domain.ConsentEventType, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "unknown", "not recorded", "unrecorded", "none":
		return "", true
	case "granted", "opt_in", "optin", "opted_in", "optedin", "opt-in", "yes", "true", "1", "subscribed", "active":
		return domain.ConsentGranted, true
	case "revoked", "opt_out", "optout", "opted_out", "optedout", "opt-out", "no", "false", "0", "unsubscribed", "stopped":
		return domain.ConsentRevoked, true
	}
	return "", false
}

func normalizeImportChannel(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "whatsapp", "wa", "whatsapp_business", "whatsapp business":
		return "whatsapp", true
	case "telegram", "tg":
		return "telegram", true
	case "instagram", "ig":
		return "instagram", true
	case "x", "twitter":
		return "x", true
	}
	return "", false
}

// normalizeImportRouting aligns pasted values with the formats the directory
// already stores: WhatsApp contacts sync as bare E.164 digits, while handles
// lose their leading @ (and are lowercased where the platform is).
func normalizeImportRouting(channel, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	switch channel {
	case "whatsapp":
		var digits strings.Builder
		for _, r := range value {
			if r >= '0' && r <= '9' {
				digits.WriteRune(r)
			}
		}
		normalized := strings.TrimPrefix(digits.String(), "00")
		if len(normalized) < 7 || len(normalized) > 15 {
			return "", fmt.Errorf("expected a phone number with 7-15 digits")
		}
		return normalized, nil
	case "telegram", "instagram", "x":
		value = strings.TrimPrefix(value, "@")
		if channel == "instagram" || channel == "x" {
			value = strings.ToLower(value)
		}
		if value == "" {
			return "", fmt.Errorf("recipient handle cannot be empty")
		}
		if len(value) > 255 {
			return "", fmt.Errorf("recipient handle is too long")
		}
		return value, nil
	}
	return "", fmt.Errorf("unsupported channel %q", channel)
}

// ImportContacts validates and stores a pasted/CSV list. Rows are processed
// independently so a malformed line never discards an otherwise valid import.
func (u *ContactUseCase) ImportContacts(ctx context.Context, tenantID string, req domain.ContactImportRequest) (*domain.ContactImportSummary, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: missing tenant context", domain.ErrInvalidContact)
	}
	if len(req.Rows) == 0 {
		return nil, fmt.Errorf("%w: there are no rows to import", domain.ErrInvalidContact)
	}
	if len(req.Rows) > domain.ContactImportMaxRows {
		return nil, fmt.Errorf("%w: a single import is limited to %d rows", domain.ErrInvalidContact, domain.ContactImportMaxRows)
	}

	defaultChannel := ""
	if strings.TrimSpace(req.DefaultChannel) != "" {
		channel, ok := normalizeImportChannel(req.DefaultChannel)
		if !ok {
			return nil, fmt.Errorf("%w: default channel must be whatsapp, telegram, instagram, or x", domain.ErrInvalidContact)
		}
		defaultChannel = channel
	}

	defaultConsent, ok := parseImportConsent(req.DefaultConsent)
	if !ok {
		return nil, fmt.Errorf("%w: default consent must be granted, revoked, or empty", domain.ErrInvalidContact)
	}

	summary := &domain.ContactImportSummary{
		Total:  len(req.Rows),
		Errors: make([]domain.ContactImportError, 0),
	}
	addError := func(row int, routingValue, message string) {
		summary.Invalid++
		if len(summary.Errors) < importErrorLimit {
			summary.Errors = append(summary.Errors, domain.ContactImportError{
				Row:          row,
				RoutingValue: routingValue,
				Message:      message,
			})
		}
	}

	type preparedRow struct {
		number    int
		firstName string
		lastName  string
		channel   string
		routing   string
		consent   domain.ConsentEventType
		note      string
		existing  *domain.Contact
	}

	prepared := make([]preparedRow, 0, len(req.Rows))
	seen := make(map[string]struct{}, len(req.Rows))

	for index, row := range req.Rows {
		number := index + 1

		channel := defaultChannel
		if strings.TrimSpace(row.Channel) != "" {
			normalized, ok := normalizeImportChannel(row.Channel)
			if !ok {
				addError(number, row.RoutingValue, "channel must be whatsapp, telegram, instagram, or x")
				continue
			}
			channel = normalized
		}
		if channel == "" {
			addError(number, row.RoutingValue, "choose a channel for this recipient")
			continue
		}

		routing, err := normalizeImportRouting(channel, row.RoutingValue)
		if err != nil {
			addError(number, row.RoutingValue, err.Error())
			continue
		}

		consent := defaultConsent
		if strings.TrimSpace(row.Consent) != "" {
			parsed, ok := parseImportConsent(row.Consent)
			if !ok {
				addError(number, routing, "consent must be granted, revoked, or left empty")
				continue
			}
			consent = parsed
		}

		key := channel + "|" + routing
		if _, exists := seen[key]; exists {
			summary.Duplicates++
			continue
		}
		seen[key] = struct{}{}

		firstName := strings.TrimSpace(row.FirstName)
		if firstName == "" {
			firstName = routing
		}

		prepared = append(prepared, preparedRow{
			number:    number,
			firstName: firstName,
			lastName:  strings.TrimSpace(row.LastName),
			channel:   channel,
			routing:   routing,
			consent:   consent,
			note:      strings.TrimSpace(row.ConsentNote),
		})
	}

	// Look up every recipient before writing so the plan limit can be enforced
	// without a partial import. Existing recipients never consume new capacity.
	checked := prepared[:0]
	for _, row := range prepared {
		existing, err := u.repo.GetByRoutingValue(ctx, tenantID, row.channel, row.routing)
		if err != nil && !errors.Is(err, domain.ErrContactNotFound) {
			addError(row.number, row.routing, "this recipient could not be checked, so the row was skipped")
			continue
		}
		row.existing = existing
		checked = append(checked, row)
	}
	prepared = checked

	quotaLimit := -1
	currentTotal := 0
	if u.subRepo != nil {
		if sub, err := u.subRepo.GetByTenantID(ctx, tenantID); err == nil && sub != nil {
			quotaLimit = sub.ContactsLimit
		}
	}
	if quotaLimit != -1 {
		if _, total, err := u.repo.ListWithFilter(ctx, tenantID, domain.ContactFilter{Page: 1, PageSize: 1, Limit: 1}); err == nil {
			currentTotal = total
		}
		newRecipients := 0
		for _, row := range prepared {
			if row.existing == nil {
				newRecipients++
			}
		}
		if currentTotal+newRecipients > quotaLimit {
			return nil, fmt.Errorf("%w: this import would exceed your plan's %d-contact capacity (%d in use, %d new). Upgrade, remove rows, or import in smaller batches.", domain.ErrQuotaExceeded, quotaLimit, currentTotal, newRecipients)
		}
	}

	for _, row := range prepared {
		// An opt-out is only reversible by the recipient. Re-importing a list
		// that still contains them must not silently re-grant consent.
		if row.existing != nil && row.existing.Status == "opted_out" && row.consent == domain.ConsentGranted {
			addError(row.number, row.routing, "this recipient previously opted out; an import cannot record consent they did not give")
			continue
		}

		contact := &domain.Contact{
			TenantID:     tenantID,
			FirstName:    row.firstName,
			LastName:     row.lastName,
			Channel:      row.channel,
			RoutingValue: row.routing,
			Source:       "csv_import",
			Status:       "active",
		}
		if err := u.repo.Create(ctx, contact); err != nil {
			addError(row.number, row.routing, "this recipient could not be saved")
			continue
		}

		if row.existing == nil {
			summary.Created++
		} else {
			summary.Existing++
		}

		if row.consent != "" && u.consentRepo != nil {
			note := row.note
			if note == "" {
				note = strings.TrimSpace(req.ConsentNote)
			}
			evidence, _ := json.Marshal(map[string]interface{}{
				"basis": "csv_import",
				"note":  note,
				"row":   row.number,
			})
			_, err := u.consentRepo.ApplyEvent(ctx, &domain.ConsentEvent{
				TenantID:  tenantID,
				ContactID: contact.ID,
				EventType: row.consent,
				Source:    domain.ConsentSourceCSVImport,
				Channel:   row.channel,
				Evidence:  string(evidence),
			})
			if err != nil {
				addError(row.number, row.routing, "this recipient was saved but their consent decision could not be recorded")
				continue
			}
		}

		switch row.consent {
		case domain.ConsentGranted:
			summary.ConsentGranted++
		case domain.ConsentRevoked:
			summary.ConsentRevoked++
		default:
			summary.NoConsent++
		}
	}

	summary.TruncatedErrors = summary.Invalid > len(summary.Errors)
	return summary, nil
}
