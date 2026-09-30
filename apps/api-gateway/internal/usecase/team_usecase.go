package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/service"
)

type TeamUseCase struct {
	repo         domain.IdentityRepository
	emailService *service.EmailService
	appBaseURL   string
	logger       *log.Logger
}

func NewTeamUseCase(
	repo domain.IdentityRepository,
	emailService *service.EmailService,
	appBaseURL string,
	logger *log.Logger,
) *TeamUseCase {
	if appBaseURL == "" {
		appBaseURL = "https://omnipulseng.vercel.app"
	}
	appBaseURL = strings.TrimRight(appBaseURL, "/")
	return &TeamUseCase{
		repo:         repo,
		emailService: emailService,
		appBaseURL:   appBaseURL,
		logger:       logger,
	}
}

type TeamOverview struct {
	Members     []domain.TenantMember   `json:"members"`
	Invitations []domain.TeamInvitation `json:"invitations"`
}

type InvitationPreview struct {
	WorkspaceName string    `json:"workspace_name"`
	InviterEmail  string    `json:"inviter_email"`
	InvitedEmail  string    `json:"invited_email"`
	Role          string    `json:"role"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// ListTeam returns all workspace members and pending invites
func (u *TeamUseCase) ListTeam(ctx context.Context, tenantID string) (*TeamOverview, error) {
	members, err := u.repo.ListMembers(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list team members: %w", err)
	}

	invitations, err := u.repo.ListInvitations(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending invitations: %w", err)
	}

	return &TeamOverview{
		Members:     members,
		Invitations: invitations,
	}, nil
}

// InviteMember verifies permissions, creates an invitation, and dispatches a Brevo email
func (u *TeamUseCase) InviteMember(
	ctx context.Context,
	tenantID, callerID, callerRole, targetEmail, targetRole string,
) (*domain.TeamInvitation, error) {
	// 1. RBAC Guard: Only owner or admin can invite
	if !strings.EqualFold(callerRole, "owner") && !strings.EqualFold(callerRole, "admin") {
		return nil, errors.New("insufficient permissions: only workspace owners and admins can invite members")
	}

	// Admin can only invite members
	targetRole = strings.ToLower(strings.TrimSpace(targetRole))
	if strings.EqualFold(callerRole, "admin") && targetRole != "member" {
		return nil, errors.New("admins can only invite teammates with the 'member' role")
	}

	if targetRole != "admin" && targetRole != "member" {
		return nil, errors.New("invalid role assignment: role must be 'admin' or 'member'")
	}

	// 2. Validate email format
	targetEmail = strings.ToLower(strings.TrimSpace(targetEmail))
	if _, err := mail.ParseAddress(targetEmail); err != nil {
		return nil, fmt.Errorf("invalid email address format: %w", err)
	}

	// 3. Prevent duplicate active memberships
	existingMembers, err := u.repo.ListMembers(ctx, tenantID)
	if err == nil {
		for _, m := range existingMembers {
			if strings.EqualFold(m.Email, targetEmail) {
				return nil, errors.New("a user with this email is already a member of this workspace")
			}
		}
	}

	// 4. Prevent duplicate pending invitations
	pendingInvites, err := u.repo.ListInvitations(ctx, tenantID)
	if err == nil {
		for _, inv := range pendingInvites {
			if strings.EqualFold(inv.Email, targetEmail) {
				return nil, errors.New("an active invitation is already pending for this email address")
			}
		}
	}

	// 5. Generate cryptographically secure token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate secure invitation token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	// 6. Record invitation in PostgreSQL
	inv := &domain.TeamInvitation{
		TenantID:  tenantID,
		Email:     targetEmail,
		Role:      targetRole,
		Token:     token,
		InvitedBy: callerID,
		Status:    "pending",
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days expiry
	}

	if err := u.repo.CreateInvitation(ctx, inv); err != nil {
		return nil, fmt.Errorf("failed to record invitation: %w", err)
	}

	// 7. Dispatch branded Brevo email
	tenant, _ := u.repo.FindTenantByID(ctx, tenantID)
	workspaceName := "Your Workspace"
	if tenant != nil && tenant.CompanyName != "" {
		workspaceName = tenant.CompanyName
	}

	inviterUser, _ := u.repo.FindUserByClerkID(ctx, callerID)
	inviterName := "A teammate"
	if inviterUser != nil && inviterUser.Email != "" {
		inviterName = inviterUser.Email
	}

	inviteURL := fmt.Sprintf("%s/invite?token=%s", u.appBaseURL, token)

	go func() {
		// Use a detached background context with timeout for email delivery
		emailCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := u.emailService.SendTeamInvitation(emailCtx, targetEmail, inviterName, workspaceName, targetRole, inviteURL); err != nil {
			u.logger.Printf("[TeamUseCase] Failed to send Brevo invitation email to %s: %v", targetEmail, err)
		}
	}()

	return inv, nil
}

// RevokeInvitation cancels a pending invitation
func (u *TeamUseCase) RevokeInvitation(ctx context.Context, tenantID, callerRole, invitationID string) error {
	if !strings.EqualFold(callerRole, "owner") && !strings.EqualFold(callerRole, "admin") {
		return errors.New("insufficient permissions to revoke invitations")
	}
	return u.repo.RevokeInvitation(ctx, tenantID, invitationID)
}

// RemoveMember ejects a user from the workspace
func (u *TeamUseCase) RemoveMember(ctx context.Context, tenantID, callerID, callerRole, memberID string) error {
	// Only owners can remove members or admins
	if !strings.EqualFold(callerRole, "owner") {
		return errors.New("only workspace owners can remove team members")
	}

	// Safeguard: Ensure we never delete the last owner
	members, err := u.repo.ListMembers(ctx, tenantID)
	if err != nil {
		return err
	}

	var targetMember *domain.TenantMember
	for _, m := range members {
		if m.ID == memberID {
			targetMember = &m
			break
		}
	}

	if targetMember == nil {
		return errors.New("member not found in workspace")
	}

	if strings.EqualFold(targetMember.Role, "owner") {
		ownerCount, err := u.repo.CountOwners(ctx, tenantID)
		if err != nil {
			return err
		}
		if ownerCount <= 1 {
			return errors.New("cannot remove the sole owner of a workspace")
		}
	}

	return u.repo.RemoveMember(ctx, tenantID, memberID)
}

// UpdateMemberRole alters a member's workspace role
func (u *TeamUseCase) UpdateMemberRole(ctx context.Context, tenantID, callerRole, memberID, newRole string) error {
	// Only owners can modify team roles
	if !strings.EqualFold(callerRole, "owner") {
		return errors.New("only workspace owners can alter team member roles")
	}

	newRole = strings.ToLower(strings.TrimSpace(newRole))
	if newRole != "owner" && newRole != "admin" && newRole != "member" {
		return errors.New("invalid role: must be 'owner', 'admin', or 'member'")
	}

	members, err := u.repo.ListMembers(ctx, tenantID)
	if err != nil {
		return err
	}

	var targetMember *domain.TenantMember
	for _, m := range members {
		if m.ID == memberID {
			targetMember = &m
			break
		}
	}

	if targetMember == nil {
		return errors.New("member not found in workspace")
	}

	// If demoting an owner, guarantee at least one other owner remains
	if strings.EqualFold(targetMember.Role, "owner") && newRole != "owner" {
		ownerCount, err := u.repo.CountOwners(ctx, tenantID)
		if err != nil {
			return err
		}
		if ownerCount <= 1 {
			return errors.New("cannot demote the sole owner of the workspace")
		}
	}

	return u.repo.UpdateMemberRole(ctx, tenantID, memberID, newRole)
}

// GetInvitationPreview provides public non-sensitive metadata for the /invite screen
func (u *TeamUseCase) GetInvitationPreview(ctx context.Context, token string) (*InvitationPreview, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("invitation token is required")
	}

	inv, err := u.repo.FindInvitationByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, errors.New("invitation not found or invalid")
	}

	if inv.Status != "pending" {
		return nil, fmt.Errorf("this invitation has already been %s", inv.Status)
	}

	if time.Now().After(inv.ExpiresAt) {
		return nil, errors.New("this invitation link has expired")
	}

	tenant, err := u.repo.FindTenantByID(ctx, inv.TenantID)
	if err != nil || tenant == nil {
		return nil, errors.New("associated workspace not found")
	}

	return &InvitationPreview{
		WorkspaceName: tenant.CompanyName,
		InviterEmail:  inv.InviterEmail,
		InvitedEmail:  inv.Email,
		Role:          inv.Role,
		ExpiresAt:     inv.ExpiresAt,
	}, nil
}

// AcceptInvitation processes token acceptance and joins the user to the workspace
func (u *TeamUseCase) AcceptInvitation(ctx context.Context, token, userID string) (*domain.Tenant, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("invitation token is required")
	}
	if userID == "" {
		return nil, errors.New("authenticated user ID is required")
	}

	tenant, err := u.repo.AcceptInvitation(ctx, token, userID)
	if err != nil {
		return nil, err
	}

	u.logger.Printf("[TeamUseCase] User %s successfully accepted invitation and joined workspace %s (%s)", userID, tenant.CompanyName, tenant.ID)
	return tenant, nil
}
