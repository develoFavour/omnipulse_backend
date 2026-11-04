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
// a broadcast for approval. Delivery failures are logged, never returned:
// notifying approvers must not undo a successful submission.
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
