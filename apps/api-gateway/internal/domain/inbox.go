package domain

import (
	"context"
	"time"
)

type ConversationStatus string

const (
	ConversationStatusOpen    ConversationStatus = "open"
	ConversationStatusClosed  ConversationStatus = "closed"
	ConversationStatusWaiting ConversationStatus = "waiting"
)

type MessageDirection string

const (
	MessageDirectionInbound  MessageDirection = "inbound"
	MessageDirectionOutbound MessageDirection = "outbound"
)

type MessageSenderType string

const (
	MessageSenderTypeContact MessageSenderType = "contact"
	MessageSenderTypeAgent   MessageSenderType = "agent"
	MessageSenderTypeBot     MessageSenderType = "bot"
	MessageSenderTypeSystem  MessageSenderType = "system"
)

type Conversation struct {
	ID              string             `json:"id"`
	TenantID        string             `json:"tenant_id"`
	ContactID       string             `json:"contact_id"`
	Channel         string             `json:"channel"`
	Status          ConversationStatus `json:"status"`
	AssignedTo      *string            `json:"assigned_to,omitempty"`
	LastMessageText *string            `json:"last_message_text,omitempty"`
	LastMessageAt   time.Time          `json:"last_message_at"`
	UnreadCount     int                `json:"unread_count"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`

	// Enriched fields for inbox display
	Contact      *Contact `json:"contact,omitempty"`
	AssignedUser *User    `json:"assigned_user,omitempty"`
}

type Message struct {
	ID                string            `json:"id"`
	TenantID          string            `json:"tenant_id"`
	ConversationID    string            `json:"conversation_id"`
	Direction         MessageDirection  `json:"direction"`
	SenderType        MessageSenderType `json:"sender_type"`
	SenderID          *string           `json:"sender_id,omitempty"`
	Body              string            `json:"body"`
	MediaURL          *string           `json:"media_url,omitempty"`
	MediaType         *string           `json:"media_type,omitempty"`
	PlatformMessageID *string           `json:"platform_message_id,omitempty"`
	Status            string            `json:"status"`
	ErrorMessage      *string           `json:"error_message,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
}

type ConversationFilter struct {
	Channel    string // "all", "whatsapp", "telegram"
	Status     string // "open", "closed", "waiting", "all"
	Search     string // search by contact name or message body
	AssignedTo string // filter by assigned user ID
	Limit      int
	Offset     int
}

type InboxRepository interface {
	ListConversations(ctx context.Context, tenantID string, filter ConversationFilter) ([]Conversation, int, error)
	GetConversation(ctx context.Context, tenantID, conversationID string) (*Conversation, error)
	GetOrCreateConversation(ctx context.Context, tenantID, contactID, channel string) (*Conversation, error)
	UpdateConversationStatus(ctx context.Context, tenantID, conversationID string, status ConversationStatus) error
	AssignConversation(ctx context.Context, tenantID, conversationID string, userID *string) error
	MarkConversationAsRead(ctx context.Context, tenantID, conversationID string) error

	ListMessages(ctx context.Context, tenantID, conversationID string, limit, offset int) ([]Message, error)
	CreateMessage(ctx context.Context, msg *Message) error
	UpdateMessageStatusByPlatformID(ctx context.Context, tenantID, platformMessageID, status string) (*Message, error)
}

type SendReplyRequest struct {
	Body     string  `json:"body"`
	MediaURL *string `json:"media_url,omitempty"`
}

type InboxBroadcaster interface {
	BroadcastMessage(tenantID string, conv *Conversation, msg *Message)
	BroadcastStatusUpdate(tenantID, conversationID, messageID, status string)
}
