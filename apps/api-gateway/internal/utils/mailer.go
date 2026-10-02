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
</head>
<body style="margin: 0; padding: 0; background-color: #f9fafb; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; -webkit-font-smoothing: antialiased; color: #111827;">
  <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="background-color: #f9fafb; padding: 48px 16px;">
    <tr>
      <td align="center">
        <!-- Main Card -->
        <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="max-width: 540px; background-color: #ffffff; border: 1px solid #e5e7eb; border-radius: 12px; box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);">
          <!-- Header -->
          <tr>
            <td style="padding: 32px 40px 24px 40px; border-bottom: 1px solid #f3f4f6;">
              <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0">
                <tr>
                  <td align="left">
                    <span style="display: inline-block; background-color: #4f46e5; color: #ffffff; font-weight: 700; font-size: 14px; padding: 6px 12px; border-radius: 8px; letter-spacing: -0.2px;">⚡ Omnipulse</span>
                  </td>
                  <td align="right">
                    <span style="font-size: 12px; font-weight: 600; color: #6b7280; background-color: #f3f4f6; border: 1px solid #e5e7eb; padding: 4px 10px; border-radius: 9999px;">Team Invite</span>
                  </td>
                </tr>
              </table>
            </td>
          </tr>

          <!-- Content Body -->
          <tr>
            <td style="padding: 36px 40px 40px 40px;">
              <h1 style="margin: 0 0 12px 0; font-size: 22px; font-weight: 700; color: #111827; letter-spacing: -0.4px; line-height: 1.3;">
                Join %[2]s
              </h1>
              <p style="margin: 0 0 24px 0; font-size: 15px; line-height: 24px; color: #4b5563;">
                <strong>%[1]s</strong> has invited you to collaborate on the <strong>%[2]s</strong> workspace on Omnipulse.
              </p>

              <!-- Workspace Details Box -->
              <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="background-color: #f9fafb; border: 1px solid #e5e7eb; border-radius: 8px; margin-bottom: 28px;">
                <tr>
                  <td style="padding: 14px 18px; border-bottom: 1px solid #e5e7eb;">
                    <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0">
                      <tr>
                        <td style="font-size: 13px; color: #6b7280;">Assigned Role</td>
                        <td align="right">
                          <span style="display: inline-block; background-color: #eef2ff; color: #4338ca; border: 1px solid #c7d2fe; font-size: 12px; font-weight: 600; padding: 2px 8px; border-radius: 6px;">%[3]s</span>
                        </td>
                      </tr>
                    </table>
                  </td>
                </tr>
                <tr>
                  <td style="padding: 14px 18px;">
                    <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0">
                      <tr>
                        <td style="font-size: 13px; color: #6b7280;">Invited By</td>
                        <td align="right" style="font-size: 13px; font-weight: 600; color: #111827;">%[1]s</td>
                      </tr>
                    </table>
                  </td>
                </tr>
              </table>

              <!-- Call to Action Button -->
              <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0">
                <tr>
                  <td align="center" style="padding: 4px 0 24px 0;">
                    <a href="%[4]s" target="_blank" style="display: inline-block; background-color: #4f46e5; color: #ffffff !important; text-decoration: none; font-weight: 600; font-size: 15px; padding: 13px 36px; border-radius: 8px; text-align: center;">
                      Accept Invitation &rarr;
                    </a>
                  </td>
                </tr>
              </table>

              <!-- Security Notice -->
              <p style="margin: 0; font-size: 13px; line-height: 20px; color: #6b7280; border-top: 1px solid #f3f4f6; padding-top: 20px;">
                This invitation link is secure and will expire in <strong>7 days</strong>.<br>
                If you weren't expecting this invitation, you can safely ignore this email.
              </p>

              <!-- Fallback Link -->
              <p style="margin: 16px 0 0 0; font-size: 11px; line-height: 18px; color: #9ca3af; word-break: break-all;">
                Button not working? Copy and paste this URL into your browser:<br>
                <a href="%[4]s" target="_blank" style="color: #4f46e5; text-decoration: underline;">%[4]s</a>
              </p>
            </td>
          </tr>
        </table>

        <!-- Footer -->
        <table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="max-width: 540px; margin-top: 24px;">
          <tr>
            <td align="center" style="font-size: 12px; color: #9ca3af; line-height: 18px;">
              Omnipulse &bull; Intelligent Cross-Platform Messaging &amp; Broadcast Engine
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, escapedInviter, escapedWorkspace, escapedRole, escapedURL)

	m.SendEmail(toEmail, subject, htmlBody)
}
