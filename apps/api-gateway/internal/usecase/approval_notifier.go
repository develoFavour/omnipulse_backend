package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

// ApprovalNotifier tells Workspace Owners and Admins that a teammate submitted
// a broadcast for approval, and tells the agency when the brand decides.
// Delivery failures are logged, never returned: notifying people must not undo
// a successful submission or decision.
type ApprovalNotifier struct {
	identity   domain.IdentityRepository
	notifs     domain.NotificationRepository
	mailer     *utils.Mailer
	appBaseURL string
}

func NewApprovalNotifier(identity domain.IdentityRepository, notifs domain.NotificationRepository, mailer *utils.Mailer, appBaseURL string) *ApprovalNotifier {
	return &ApprovalNotifier{
		identity:   identity,
		notifs:     notifs,
		mailer:     mailer,
		appBaseURL: cleanAppBaseURL(appBaseURL),
	}
}

func (n *ApprovalNotifier) NotifySubmitted(ctx context.Context, tenantID, campaignID, campaignTitle, submittedBy string) {
	if n == nil {
		return
	}

	members, err := n.identity.ListMembers(ctx, tenantID)
	if err != nil {
		log.Printf("[APPROVAL-NOTIFY] Failed to list members for tenant %s: %v\n", tenantID, err)
	}

	submitter := "A teammate"
	approvers := make([]domain.TenantMember, 0, len(members))
	for _, m := range members {
		if m.UserID == submittedBy && m.Email != "" {
			submitter = m.Email
		}
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if (role == "owner" || role == "admin") && m.Email != "" && m.UserID != submittedBy {
			approvers = append(approvers, m)
		}
	}

	metadata, _ := json.Marshal(map[string]string{
		"campaign_id": campaignID,
		"route":       "/broadcast/scheduled",
	})
	notif := &domain.Notification{
		TenantID: tenantID,
		Type:     domain.NotifCampaignPendingApproval,
		Title:    "Broadcast awaiting approval",
		Body:     fmt.Sprintf("%s submitted \"%s\" for approval. Owners and Admins can review it in the Scheduled Queue.", submitter, campaignTitle),
		Metadata: metadata,
	}
	if err := n.notifs.Create(ctx, notif); err != nil {
		log.Printf("[APPROVAL-NOTIFY] Failed to write notification for campaign %s: %v\n", campaignID, err)
	}

	if n.mailer == nil || len(approvers) == 0 {
		return
	}

	workspaceName := "your workspace"
	if tenant, err := n.identity.FindTenantByID(ctx, tenantID); err == nil && tenant != nil && strings.TrimSpace(tenant.CompanyName) != "" {
		workspaceName = tenant.CompanyName
	}

	queueURL := fmt.Sprintf("%s/broadcast/scheduled?campaign=%s", n.appBaseURL, url.QueryEscape(campaignID))
	for _, approver := range approvers {
		n.mailer.SendCampaignAwaitingApproval(approver.Email, workspaceName, campaignTitle, submitter, queueURL)
	}
}

// NotifyBrandDecision tells the agency that the brand approved the campaign or
// asked for changes. It reaches the submitter and every Owner/Admin, in-app and
// by email, so the next action is never hidden behind the campaign screen.
func (n *ApprovalNotifier) NotifyBrandDecision(ctx context.Context, tenantID, campaignID, campaignTitle, decision, note, reviewerLabel, submittedBy string) {
	if n == nil {
		return
	}

	approved := decision == domain.ReviewDecisionApproved

	reviewer := strings.TrimSpace(reviewerLabel)
	if reviewer == "" {
		reviewer = "The brand"
	}
	note = strings.TrimSpace(note)

	var notifType domain.NotificationType
	var title, body string
	if approved {
		notifType = domain.NotifCampaignBrandApproved
		title = fmt.Sprintf("Brand approved \"%s\"", campaignTitle)
		body = fmt.Sprintf("%s approved this campaign. It can now be scheduled or sent.", reviewer)
	} else {
		notifType = domain.NotifCampaignBrandChanges
		title = fmt.Sprintf("Changes requested on \"%s\"", campaignTitle)
		if note != "" {
			body = fmt.Sprintf("%s requested changes: %s", reviewer, truncateNotificationText(note, 280))
		} else {
			body = fmt.Sprintf("%s requested changes. Update the campaign and get a new approval before sending.", reviewer)
		}
	}

	metadata, _ := json.Marshal(map[string]string{
		"campaign_id": campaignID,
		"route":       "/broadcast/scheduled",
		"decision":    decision,
	})
	notif := &domain.Notification{
		TenantID: tenantID,
		Type:     notifType,
		Title:    title,
		Body:     body,
		Metadata: metadata,
	}
	if err := n.notifs.Create(ctx, notif); err != nil {
		log.Printf("[APPROVAL-NOTIFY] Failed to write brand decision notification for campaign %s: %v\n", campaignID, err)
	}

	if n.mailer == nil {
		return
	}

	members, err := n.identity.ListMembers(ctx, tenantID)
	if err != nil {
		log.Printf("[APPROVAL-NOTIFY] Failed to list members for tenant %s: %v\n", tenantID, err)
		return
	}

	recipients := make(map[string]domain.TenantMember)
	for _, m := range members {
		if strings.TrimSpace(m.Email) == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if m.UserID == submittedBy || role == "owner" || role == "admin" {
			recipients[strings.ToLower(m.Email)] = m
		}
	}
	if len(recipients) == 0 {
		return
	}

	workspaceName := "your workspace"
	if tenant, err := n.identity.FindTenantByID(ctx, tenantID); err == nil && tenant != nil && strings.TrimSpace(tenant.CompanyName) != "" {
		workspaceName = tenant.CompanyName
	}

	campaignURL := fmt.Sprintf("%s/broadcast/scheduled?campaign=%s", n.appBaseURL, url.QueryEscape(campaignID))
	for _, recipient := range recipients {
		n.mailer.SendBrandDecision(recipient.Email, workspaceName, campaignTitle, approved, note, reviewer, campaignURL)
	}
}

func truncateNotificationText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
