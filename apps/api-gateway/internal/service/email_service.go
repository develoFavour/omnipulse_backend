package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type EmailService struct {
	apiKey      string
	senderEmail string
	senderName  string
	httpClient  *http.Client
	logger      *log.Logger
}

func NewEmailService(apiKey, senderEmail, senderName string, logger *log.Logger) *EmailService {
	if senderEmail == "" {
		senderEmail = "opiafavourjr@gmail.com"
	}
	if senderName == "" {
		senderName = "Omnipulseng"
	}
	return &EmailService{
		apiKey:      apiKey,
		senderEmail: senderEmail,
		senderName:  senderName,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		logger:      logger,
	}
}

type brevoRecipient struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type brevoSender struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type brevoEmailRequest struct {
	Sender      brevoSender      `json:"sender"`
	To          []brevoRecipient `json:"to"`
	Subject     string           `json:"subject"`
	HTMLContent string           `json:"htmlContent"`
}

// SendTeamInvitation dispatches a branded invitation email via the Brevo transactional SMTP API
func (s *EmailService) SendTeamInvitation(ctx context.Context, toEmail, inviterName, workspaceName, role, inviteURL string) error {
	if s.apiKey == "" {
		s.logger.Printf("[EmailService] WARN: BREVO_API_KEY is not set. Skipping invitation email dispatch to %s (URL: %s)", toEmail, inviteURL)
		return nil
	}

	roleDisplay := strings.ToUpper(role[:1]) + strings.ToLower(role[1:])
	subject := fmt.Sprintf("You've been invited to join %s on Omnipulse", workspaceName)

	escapedInviter := html.EscapeString(inviterName)
	escapedWorkspace := html.EscapeString(workspaceName)
	escapedRole := html.EscapeString(roleDisplay)
	escapedURL := html.EscapeString(inviteURL)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Invitation to join %[2]s</title>
  <style>
    body { margin: 0; padding: 0; background-color: #0b0f19; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; color: #f3f4f6; }
    .container { max-width: 580px; margin: 40px auto; background-color: #111827; border: 1px solid #1f2937; border-radius: 16px; overflow: hidden; box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.5); }
    .header { padding: 32px 40px; text-align: center; background: linear-gradient(135deg, #1e1b4b 0%%, #312e81 50%%, #4338ca 100%%); border-bottom: 1px solid #3730a3; }
    .logo { font-size: 26px; font-weight: 800; letter-spacing: -0.5px; color: #ffffff; text-decoration: none; }
    .logo-badge { background-color: #6366f1; color: white; padding: 3px 10px; border-radius: 9999px; font-size: 11px; font-weight: 700; text-transform: uppercase; margin-left: 8px; vertical-align: middle; }
    .content { padding: 40px; text-align: center; }
    h1 { font-size: 22px; font-weight: 700; color: #ffffff; margin: 0 0 16px 0; line-height: 1.3; }
    p { font-size: 15px; line-height: 1.6; color: #9ca3af; margin: 0 0 24px 0; }
    .role-badge { display: inline-block; background-color: #1e1b4b; color: #a5b4fc; border: 1px solid #4338ca; padding: 6px 16px; border-radius: 9999px; font-weight: 600; font-size: 13px; margin-bottom: 28px; }
    .cta-btn { display: inline-block; background: linear-gradient(135deg, #4f46e5 0%%, #6366f1 100%%); color: #ffffff !important; text-decoration: none; font-weight: 700; font-size: 16px; padding: 14px 36px; border-radius: 10px; box-shadow: 0 10px 15px -3px rgba(79, 70, 229, 0.4); }
    .notice { font-size: 12px; color: #6b7280; margin-top: 36px; line-height: 1.5; border-top: 1px solid #1f2937; padding-top: 24px; }
    .footer { padding: 24px 40px; text-align: center; background-color: #0b0f19; font-size: 12px; color: #4b5563; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <div class="logo">⚡ Omnipulse <span class="logo-badge">Team</span></div>
    </div>
    <div class="content">
      <h1>Collaborate on %[2]s</h1>
      <p><strong>%[1]s</strong> has invited you to join their workspace on Omnipulse as an official team member.</p>
      <div class="role-badge">Assigned Role: %[3]s</div>
      <div>
        <a href="%[4]s" class="cta-btn" target="_blank">Accept Invitation</a>
      </div>
      <div class="notice">
        This link is secure and will expire in <strong>7 days</strong>.<br>
        If you weren't expecting this invitation, you can safely ignore this email.
      </div>
    </div>
    <div class="footer">
      Omnipulse &bull; Intelligent Cross-Platform Messaging &amp; Broadcast Engine
    </div>
  </div>
</body>
</html>`, escapedInviter, escapedWorkspace, escapedRole, escapedURL)

	reqPayload := brevoEmailRequest{
		Sender: brevoSender{
			Name:  s.senderName,
			Email: s.senderEmail,
		},
		To: []brevoRecipient{
			{Email: toEmail},
		},
		Subject:     subject,
		HTMLContent: htmlBody,
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return fmt.Errorf("failed to encode email payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.brevo.com/v3/smtp/email", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to construct Brevo API request: %w", err)
	}

	req.Header.Set("api-key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("brevo API network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("brevo API rejected request with status %d: %s", resp.StatusCode, string(respBody))
	}

	s.logger.Printf("[EmailService] Successfully dispatched team invitation email to %s (role: %s, workspace: %s)", toEmail, role, workspaceName)
	return nil
}
