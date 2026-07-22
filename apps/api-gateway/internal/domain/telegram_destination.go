package domain

import (
	"context"
	"errors"
	"time"
)

var ErrTelegramDestinationNotFound = errors.New("telegram destination not found")

// TelegramDestination is a group, supergroup, or channel discovered for a tenant bot.
type TelegramDestination struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	ChannelID      string    `json:"channel_id"`
	TelegramChatID string    `json:"telegram_chat_id"`
	Title          string    `json:"title"`
	Type           string    `json:"type"`
	Status         string    `json:"status"`
	Source         string    `json:"source"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TelegramDestinationRepository defines persistence for Telegram broadcast targets.
type TelegramDestinationRepository interface {
	Upsert(ctx context.Context, destination *TelegramDestination) error
	ListByTenant(ctx context.Context, tenantID string) ([]TelegramDestination, error)
	ListByIDs(ctx context.Context, tenantID string, ids []string) ([]TelegramDestination, error)
}
