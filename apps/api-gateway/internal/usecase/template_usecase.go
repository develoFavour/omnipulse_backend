package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
)

type StarterTemplate struct {
	Title     string
	Category  string
	Body      string
	Variables []string
}

var DefaultStarterTemplates = []StarterTemplate{
	{
		Title:    "⚡ Flash 24H Exclusive Deal",
		Category: "promotions",
		Body:     "Hey {{first_name}}! ⚡ For the next 24 hours only, unlock 30% off our premier service bundle with code FLASH30.\n\nClaim your spot here before seats fill up: https://omnipulse.link/flash\n\nReply STOP to opt out.",
		Variables: []string{"first_name"},
	},
	{
		Title:    "⭐ VIP Early Access Invitation",
		Category: "promotions",
		Body:     "Hello {{first_name}}, as one of our top-tier partners, you get exclusive private access to our upcoming release 48 hours before the public.\n\nExplore the catalog here: https://omnipulse.link/vip-early\n\nNeed assistance? Reply directly to this chat!",
		Variables: []string{"first_name"},
	},
	{
		Title:    "👋 Welcome to the Community",
		Category: "onboarding",
		Body:     "Hi {{first_name}}! Welcome to our inner circle. 🎉\n\nHere is everything you need to get the most value right away:\n1. Community Guidelines & FAQ\n2. Schedule your 1-on-1 strategy briefing\n\nStay tuned for weekly updates right here on Telegram & WhatsApp!",
		Variables: []string{"first_name"},
	},
	{
		Title:    "💔 We Miss You — Special Comeback Offer",
		Category: "re_engagement",
		Body:     "Hey {{first_name}}, we noticed it's been a while! We've made huge improvements to our platform and want to welcome you back.\n\nUse voucher WELCOMEBACK for a free credit on your next campaign: https://omnipulse.link/return\n\nLet us know if you need anything!",
		Variables: []string{"first_name"},
	},
	{
		Title:    "⏰ Event Starts in 1 Hour",
		Category: "reminders",
		Body:     "Quick reminder {{first_name}}: Our live masterclass starts in exactly 60 minutes! 🎙️\n\nHave your questions ready and join the live stream using your secure link:\nhttps://omnipulse.link/room\n\nSee you inside!",
		Variables: []string{"first_name"},
	},
	{
		Title:    "📢 Important Service Notice",
		Category: "urgent",
		Body:     "Hello {{first_name}}, please note that our service will undergo scheduled maintenance tonight between 2:00 AM and 4:00 AM UTC. No action is required on your part. Thank you for your continued partnership.",
		Variables: []string{"first_name"},
	},
}

type templateUseCase struct {
	repo domain.TemplateRepository
}

func NewTemplateUseCase(repo domain.TemplateRepository) domain.TemplateUseCase {
	return &templateUseCase{repo: repo}
}

var varRegex = regexp.MustCompile(`\{\{([a-zA-Z0-9_]+)\}\}`)

func extractVariables(body string, existing []string) []string {
	varMap := make(map[string]bool)
	for _, v := range existing {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			varMap[trimmed] = true
		}
	}
	matches := varRegex.FindAllStringSubmatch(body, -1)
	for _, m := range matches {
		if len(m) > 1 {
			varMap[m[1]] = true
		}
	}
	result := make([]string, 0, len(varMap))
	for k := range varMap {
		result = append(result, k)
	}
	return result
}

func (u *templateUseCase) SeedDefaultTemplates(ctx context.Context, tenantID string) error {
	for _, st := range DefaultStarterTemplates {
		tmpl := &domain.Template{
			TenantID:  tenantID,
			Title:     st.Title,
			Category:  st.Category,
			Body:      st.Body,
			Variables: st.Variables,
		}
		if err := u.repo.Create(ctx, tmpl); err != nil {
			log.Printf("[TEMPLATE] Warning: failed to seed template %q for tenant %s: %v", st.Title, tenantID, err)
		}
	}
	return nil
}

func (u *templateUseCase) CreateTemplate(
	ctx context.Context,
	tenantID, title, category, body string,
	mediaURL *string,
	variables []string,
) (*domain.Template, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("template title is required")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("template body is required")
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "general"
	}

	vars := extractVariables(body, variables)

	tmpl := &domain.Template{
		TenantID:  tenantID,
		Title:     title,
		Category:  category,
		Body:      body,
		MediaURL:  mediaURL,
		Variables: vars,
	}

	if err := u.repo.Create(ctx, tmpl); err != nil {
		return nil, fmt.Errorf("failed to create template: %w", err)
	}

	return tmpl, nil
}

func (u *templateUseCase) ListTemplates(ctx context.Context, tenantID, category string) ([]*domain.Template, error) {
	templates, err := u.repo.ListByTenant(ctx, tenantID, category)
	if err != nil {
		return nil, err
	}

	// Auto-seed: If the tenant has no templates at all, seed starter templates automatically
	if len(templates) == 0 && category == "" {
		_ = u.SeedDefaultTemplates(ctx, tenantID)
		return u.repo.ListByTenant(ctx, tenantID, category)
	}

	return templates, nil
}

func (u *templateUseCase) GetTemplate(ctx context.Context, tenantID, id string) (*domain.Template, error) {
	return u.repo.GetByID(ctx, tenantID, id)
}

func (u *templateUseCase) UpdateTemplate(
	ctx context.Context,
	tenantID, id, title, category, body string,
	mediaURL *string,
	variables []string,
) (*domain.Template, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("template title is required")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("template body is required")
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "general"
	}

	existing, err := u.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, fmt.Errorf("template not found: %w", err)
	}

	vars := extractVariables(body, variables)

	existing.Title = title
	existing.Category = category
	existing.Body = body
	existing.MediaURL = mediaURL
	existing.Variables = vars

	if err := u.repo.Update(ctx, existing); err != nil {
		return nil, fmt.Errorf("failed to update template: %w", err)
	}

	return existing, nil
}

func (u *templateUseCase) DeleteTemplate(ctx context.Context, tenantID, id string) error {
	return u.repo.Delete(ctx, tenantID, id)
}
