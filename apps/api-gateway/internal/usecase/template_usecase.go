package usecase

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
)

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
	return u.repo.ListByTenant(ctx, tenantID, category)
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
