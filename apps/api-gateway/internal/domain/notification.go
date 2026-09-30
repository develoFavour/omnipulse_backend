package domain

import (
	"context"
	"encoding/json"
	"time"
)

// NotificationType categorises event kinds surfaced to agency users
type NotificationType string

const (
	NotifCampaignCompleted     NotificationType = "campaign_completed"
	NotifDeliveryFailure       NotificationType = "delivery_failure"
	NotifNewOptOut             NotificationType = "new_opt_out"
	NotifContactImportFinished NotificationType = "contact_import_finished"
	NotifChannelDisconnected   NotificationType = "channel_disconnected"
	NotifSystemAlert           NotificationType = "system_alert" // worker outage / infrastructure alerts
)

// Notification is a single inbox event scoped to a tenant
type Notification struct {
	ID        string           `json:"id"`
	TenantID  string           `json:"tenant_id"`
	Type      NotificationType `json:"type"`
	Title     string           `json:"title"`
	Body      string           `json:"body"`
	Metadata  json.RawMessage  `json:"metadata"`
	IsRead    bool             `json:"is_read"`
	CreatedAt time.Time        `json:"created_at"`
}

// NotificationRepository is the driven port for notification persistence
type NotificationRepository interface {
	Create(ctx context.Context, n *Notification) error
	ListForTenant(ctx context.Context, tenantID string, limit int) ([]*Notification, error)
	UnreadCount(ctx context.Context, tenantID string) (int, error)
	MarkRead(ctx context.Context, tenantID, id string) error
	MarkAllRead(ctx context.Context, tenantID string) error
}

// NotificationUseCase is the driving port for the notification subsystem
type NotificationUseCase interface {
	Create(ctx context.Context, n *Notification) error
	List(ctx context.Context, tenantID string, limit int) ([]*Notification, error)
	UnreadCount(ctx context.Context, tenantID string) (int, error)
	MarkRead(ctx context.Context, tenantID, id string) error
	MarkAllRead(ctx context.Context, tenantID string) error
}
