package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/config"
)

const brevoTransactionalEmailURL = "https://api.brevo.com/v3/smtp/email"

// Mailer sends transactional emails through Brevo's HTTP API.
type Mailer struct {
	cfg    *config.Config
	client *http.Client
}

type brevoEmailRequest struct {
	Sender      brevoEmailAddress   `json:"sender"`
	To          []brevoEmailAddress `json:"to"`
	Subject     string              `json:"subject"`
	HTMLContent string              `json:"htmlContent"`
}

type brevoEmailAddress struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

// NewMailer creates an instance of Mailer.
func NewMailer(cfg *config.Config) *Mailer {
	return &Mailer{
		cfg: cfg,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// SendEmail asynchronously dispatches a transactional email using Brevo's API.
func (m *Mailer) SendEmail(to, subject, htmlBody string) {
	go func() {
		senderEmail := m.cfg.BrevoSenderEmail
		if senderEmail == "" {
			senderEmail = m.cfg.SMTPSender
		}
		if senderEmail == "" {
			senderEmail = "opiafavourjr@gmail.com"
		}

		senderName := m.cfg.BrevoSenderName
		if senderName == "" {
			senderName = m.cfg.SenderName
		}
		if senderName == "" {
			senderName = "Omnipulseng"
		}

		if m.cfg.BrevoAPIKey == "" {
			fmt.Println("Mailer Alert: BREVO_API_KEY is not configured. Email skipped.")
			return
		}

		payload := brevoEmailRequest{
			Sender: brevoEmailAddress{
				Name:  senderName,
				Email: senderEmail,
			},
			To: []brevoEmailAddress{
				{Email: to},
			},
			Subject:     subject,
			HTMLContent: htmlBody,
		}

		body, err := json.Marshal(payload)
		if err != nil {
			fmt.Printf("Mailer JSON Marshal Error to %s: %v\n", to, err)
			return
		}

		req, err := http.NewRequest(http.MethodPost, brevoTransactionalEmailURL, bytes.NewReader(body))
		if err != nil {
			fmt.Printf("Mailer Request Build Error to %s: %v\n", to, err)
			return
		}
		req.Header.Set("accept", "application/json")
		req.Header.Set("api-key", m.cfg.BrevoAPIKey)
		req.Header.Set("content-type", "application/json")

		resp, err := m.client.Do(req)
		if err != nil {
			fmt.Printf("Mailer Brevo Dispatch Error to %s: %v\n", to, err)
			return
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			fmt.Printf("Mailer Brevo API Error to %s: status=%d body=%s\n", to, resp.StatusCode, string(respBody))
			return
		}

		fmt.Printf("Mailer Brevo email sent to %s (Subject: %s)\n", to, subject)
	}()
}

// SendTeamInvitation builds a branded responsive template and dispatches via SendEmail
func (m *Mailer) SendTeamInvitation(toEmail, inviterName, workspaceName, role, inviteURL string) {
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

	m.SendEmail(toEmail, subject, htmlBody)
}
