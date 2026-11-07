package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/service"
)

type CommerceUseCase interface {
	CreatePaymentLink(ctx context.Context, tenantID, userID, conversationID string, req domain.CreatePaymentLinkRequest) (*domain.PaymentTransaction, error)
	ListPaymentLinks(ctx context.Context, tenantID, conversationID string) ([]domain.PaymentTransaction, error)
	HandlePaymentWebhook(ctx context.Context, reference string, amountKobo int64, paidAt time.Time) error
	SetBroadcaster(b domain.InboxBroadcaster)
}

type commerceUseCase struct {
	paymentRepo domain.PaymentRepository
	inboxRepo   domain.InboxRepository
	inboxUC     InboxUseCase
	contactRepo domain.ContactRepository
	tagRepo     domain.TagRepository
	paystackSvc *service.PaystackService
	broadcaster domain.InboxBroadcaster
	db          *sql.DB
	appBaseURL  string
}

func NewCommerceUseCase(
	paymentRepo domain.PaymentRepository,
	inboxRepo domain.InboxRepository,
	inboxUC InboxUseCase,
	contactRepo domain.ContactRepository,
	tagRepo domain.TagRepository,
	paystackSvc *service.PaystackService,
	db *sql.DB,
	appBaseURL string,
) CommerceUseCase {
	return &commerceUseCase{
		paymentRepo: paymentRepo,
		inboxRepo:   inboxRepo,
		inboxUC:     inboxUC,
		contactRepo: contactRepo,
		tagRepo:     tagRepo,
		paystackSvc: paystackSvc,
		db:          db,
		appBaseURL:  appBaseURL,
	}
}

func (u *commerceUseCase) SetBroadcaster(b domain.InboxBroadcaster) {
	u.broadcaster = b
}

func formatNairaKobo(kobo int64) string {
	naira := float64(kobo) / 100.0
	// Format with commas: 25000 -> 25,000.00 or integer if .00
	intPart := int64(naira)
	fracPart := int64(naira*100) % 100

	strInt := fmt.Sprintf("%d", intPart)
	var formatted strings.Builder
	l := len(strInt)
	for i, r := range strInt {
		if i > 0 && (l-i)%3 == 0 {
			formatted.WriteString(",")
		}
		formatted.WriteRune(r)
	}

	if fracPart > 0 {
		return fmt.Sprintf("%s.%02d", formatted.String(), fracPart)
	}
	return formatted.String()
}

func (u *commerceUseCase) CreatePaymentLink(
	ctx context.Context,
	tenantID, userID, conversationID string,
	req domain.CreatePaymentLinkRequest,
) (*domain.PaymentTransaction, error) {
	if req.AmountKobo <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" {
		return nil, errors.New("description cannot be empty")
	}

	// 1. Fetch conversation
	conv, err := u.inboxRepo.GetConversation(ctx, tenantID, conversationID)
	if err != nil || conv == nil {
		return nil, fmt.Errorf("conversation not found: %w", err)
	}

	// 2. Fetch contact
	contact, err := u.contactRepo.GetByID(ctx, tenantID, conv.ContactID)
	if err != nil || contact == nil {
		return nil, fmt.Errorf("contact for conversation not found: %w", err)
	}

	// 3. Campaign attribution: if not supplied, resolve the latest campaign delivered to this contact
	campaignID := req.CampaignID
	if (campaignID == nil || *campaignID == "") && u.db != nil {
		var latestCampID string
		attrQuery := `
			SELECT campaign_id 
			FROM campaign_deliveries 
			WHERE contact_id = $1 AND status IN ('delivered', 'sent', 'read') 
			ORDER BY created_at DESC 
			LIMIT 1;
		`
		if err := u.db.QueryRowContext(ctx, attrQuery, contact.ID).Scan(&latestCampID); err == nil && latestCampID != "" {
			campaignID = &latestCampID
		}
	}

	// 4. Initialize transaction on Paystack
	callbackURL := fmt.Sprintf("%s/inbox?conversation=%s&paid=true", strings.TrimRight(u.appBaseURL, "/"), conversationID)
	checkout, err := u.paystackSvc.InitializePaymentLink(
		tenantID,
		contact.ID,
		conversationID,
		campaignID,
		req.CustomerEmail,
		contact.RoutingValue,
		req.AmountKobo,
		req.Description,
		callbackURL,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Paystack payment link: %w", err)
	}

	// 5. Store record in database
	tx := &domain.PaymentTransaction{
		TenantID:       tenantID,
		ConversationID: &conversationID,
		ContactID:      contact.ID,
		CampaignID:     campaignID,
		Amount:         req.AmountKobo,
		Currency:       "NGN",
		Description:    req.Description,
		Reference:      checkout.Reference,
		PaystackURL:    checkout.CheckoutURL,
		Status:         domain.PaymentStatusPending,
	}

	if err := u.paymentRepo.CreateTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("failed to save payment transaction: %w", err)
	}

	// 6. Automatically transmit payment request into the chat
	amountDisplay := formatNairaKobo(req.AmountKobo)
	chatMessage := fmt.Sprintf(
		"💳 *Payment Request: ₦%s*\n%s\n\nPay securely via Card, Transfer, or USSD:\n%s",
		amountDisplay,
		req.Description,
		checkout.CheckoutURL,
	)

	_, sendErr := u.inboxUC.SendReply(ctx, tenantID, userID, conversationID, domain.SendReplyRequest{
		Body: chatMessage,
	})
	if sendErr != nil {
		log.Printf("[COMMERCE] Warning: payment link created but failed to auto-send reply: %v", sendErr)
	}

	return tx, nil
}

func (u *commerceUseCase) ListPaymentLinks(ctx context.Context, tenantID, conversationID string) ([]domain.PaymentTransaction, error) {
	return u.paymentRepo.ListByConversation(ctx, tenantID, conversationID)
}

func (u *commerceUseCase) HandlePaymentWebhook(ctx context.Context, reference string, amountKobo int64, paidAt time.Time) error {
	log.Printf("[COMMERCE] Processing paid webhook for reference=%s amount=%d\n", reference, amountKobo)

	tx, err := u.paymentRepo.MarkPaid(ctx, reference, paidAt)
	if err != nil {
		return fmt.Errorf("failed to mark transaction as paid: %w", err)
	}

	// Tag contact with "Customer - Paid"
	if u.tagRepo != nil && tx.ContactID != "" {
		tags, _ := u.tagRepo.ListByTenant(ctx, tx.TenantID)
		var paidTagID string
		for _, t := range tags {
			if strings.EqualFold(t.Name, "Customer - Paid") || strings.EqualFold(t.Name, "paid") {
				paidTagID = t.ID
				break
			}
		}
		if paidTagID == "" {
			newTag := &domain.Tag{
				TenantID: tx.TenantID,
				Name:     "Customer - Paid",
				Color:    "#10B981", // Emerald green
			}
			if err := u.tagRepo.Create(ctx, newTag); err == nil {
				paidTagID = newTag.ID
			}
		}
		if paidTagID != "" {
			_ = u.tagRepo.AssignTagToContact(ctx, tx.TenantID, tx.ContactID, paidTagID)
		}
	}

	// If linked to a conversation, drop a system confirmation message into chat
	if tx.ConversationID != nil && *tx.ConversationID != "" && u.inboxRepo != nil {
		amountFormatted := formatNairaKobo(tx.Amount)
		systemMsg := &domain.Message{
			TenantID:       tx.TenantID,
			ConversationID: *tx.ConversationID,
			Direction:      domain.MessageDirectionInbound,
			SenderType:     domain.MessageSenderTypeSystem,
			Body:           fmt.Sprintf("✅ *Payment Received:* ₦%s for %q (Ref: %s)", amountFormatted, tx.Description, tx.Reference),
			Status:         "delivered",
		}
		if err := u.inboxRepo.CreateMessage(ctx, systemMsg); err == nil && u.broadcaster != nil {
			conv, _ := u.inboxRepo.GetConversation(ctx, tx.TenantID, *tx.ConversationID)
			u.broadcaster.BroadcastMessage(tx.TenantID, conv, systemMsg)
		}
	}

	return nil
}
