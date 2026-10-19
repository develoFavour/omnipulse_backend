package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"omnipulse/apps/api-gateway/internal/domain"
)

type PostgresInboxRepository struct {
	db *sql.DB
}

func NewPostgresInboxRepository(db *sql.DB) domain.InboxRepository {
	return &PostgresInboxRepository{db: db}
}

func (r *PostgresInboxRepository) ListConversations(ctx context.Context, tenantID string, filter domain.ConversationFilter) ([]domain.Conversation, int, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	conditions = append(conditions, fmt.Sprintf("c.tenant_id = $%d", argIdx))
	args = append(args, tenantID)
	argIdx++

	if filter.Channel != "" && filter.Channel != "all" {
		conditions = append(conditions, fmt.Sprintf("c.channel = $%d", argIdx))
		args = append(args, filter.Channel)
		argIdx++
	}

	if filter.Status != "" && filter.Status != "all" {
		conditions = append(conditions, fmt.Sprintf("c.status = $%d", argIdx))
		args = append(args, filter.Status)
		argIdx++
	}

	if filter.AssignedTo != "" {
		if filter.AssignedTo == "unassigned" {
			conditions = append(conditions, "c.assigned_to IS NULL")
		} else {
			conditions = append(conditions, fmt.Sprintf("c.assigned_to = $%d", argIdx))
			args = append(args, filter.AssignedTo)
			argIdx++
		}
	}

	if filter.NeedsHuman {
		conditions = append(conditions, "COALESCE(c.needs_human_attention, false) = true")
	}

	if filter.Search != "" {
		searchPattern := "%" + strings.ToLower(filter.Search) + "%"
		conditions = append(conditions, fmt.Sprintf(
			"(LOWER(ct.first_name) LIKE $%d OR LOWER(COALESCE(ct.last_name, '')) LIKE $%d OR LOWER(ct.routing_value) LIKE $%d OR LOWER(COALESCE(c.last_message_text, '')) LIKE $%d)",
			argIdx, argIdx, argIdx, argIdx,
		))
		args = append(args, searchPattern)
		argIdx++
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count total matching conversations
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM conversations c
		JOIN contacts ct ON c.contact_id = ct.id
		WHERE %s
	`, whereClause)

	var total int
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count conversations: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 30
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(`
		SELECT 
			c.id, c.tenant_id, c.contact_id, c.channel, c.status, c.assigned_to, 
			c.last_message_text, c.last_message_at, c.unread_count, 
			COALESCE(c.ai_state, 'active'), c.ai_paused_until, COALESCE(c.needs_human_attention, false),
			c.created_at, c.updated_at,
			ct.first_name, ct.last_name, ct.channel, ct.routing_value, ct.status,
			u.id, u.email, u.role
		FROM conversations c
		JOIN contacts ct ON c.contact_id = ct.id
		LEFT JOIN users u ON c.assigned_to = u.id
		WHERE %s
		ORDER BY c.last_message_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)

	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query conversations: %w", err)
	}
	defer rows.Close()

	var conversations []domain.Conversation
	for rows.Next() {
		var conv domain.Conversation
		var contact domain.Contact
		var ctLastName sql.NullString
		var uID, uEmail, uRole sql.NullString

		err := rows.Scan(
			&conv.ID, &conv.TenantID, &conv.ContactID, &conv.Channel, &conv.Status, &conv.AssignedTo,
			&conv.LastMessageText, &conv.LastMessageAt, &conv.UnreadCount,
			&conv.AIState, &conv.AIPausedUntil, &conv.NeedsHuman,
			&conv.CreatedAt, &conv.UpdatedAt,
			&contact.FirstName, &ctLastName, &contact.Channel, &contact.RoutingValue, &contact.Status,
			&uID, &uEmail, &uRole,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan conversation: %w", err)
		}

		contact.ID = conv.ContactID
		contact.TenantID = conv.TenantID
		if ctLastName.Valid {
			contact.LastName = ctLastName.String
		}
		conv.Contact = &contact

		if uID.Valid {
			conv.AssignedUser = &domain.User{
				ID:    uID.String,
				Email: uEmail.String,
				Role:  uRole.String,
			}
		}

		conversations = append(conversations, conv)
	}

	return conversations, total, nil
}

func (r *PostgresInboxRepository) GetConversation(ctx context.Context, tenantID, conversationID string) (*domain.Conversation, error) {
	query := `
		SELECT 
			c.id, c.tenant_id, c.contact_id, c.channel, c.status, c.assigned_to, 
			c.last_message_text, c.last_message_at, c.unread_count,
			COALESCE(c.ai_state, 'active'), c.ai_paused_until, COALESCE(c.needs_human_attention, false),
			c.created_at, c.updated_at,
			ct.first_name, ct.last_name, ct.channel, ct.routing_value, ct.status,
			u.id, u.email, u.role
		FROM conversations c
		JOIN contacts ct ON c.contact_id = ct.id
		LEFT JOIN users u ON c.assigned_to = u.id
		WHERE c.id = $1 AND c.tenant_id = $2
	`

	var conv domain.Conversation
	var contact domain.Contact
	var ctLastName sql.NullString
	var uID, uEmail, uRole sql.NullString

	err := r.db.QueryRowContext(ctx, query, conversationID, tenantID).Scan(
		&conv.ID, &conv.TenantID, &conv.ContactID, &conv.Channel, &conv.Status, &conv.AssignedTo,
		&conv.LastMessageText, &conv.LastMessageAt, &conv.UnreadCount,
		&conv.AIState, &conv.AIPausedUntil, &conv.NeedsHuman,
		&conv.CreatedAt, &conv.UpdatedAt,
		&contact.FirstName, &ctLastName, &contact.Channel, &contact.RoutingValue, &contact.Status,
		&uID, &uEmail, &uRole,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("conversation not found")
		}
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	contact.ID = conv.ContactID
	contact.TenantID = conv.TenantID
	if ctLastName.Valid {
		contact.LastName = ctLastName.String
	}
	conv.Contact = &contact

	if uID.Valid {
		conv.AssignedUser = &domain.User{
			ID:    uID.String,
			Email: uEmail.String,
			Role:  uRole.String,
		}
	}

	return &conv, nil
}

func (r *PostgresInboxRepository) GetOrCreateConversation(ctx context.Context, tenantID, contactID, channel string) (*domain.Conversation, error) {
	query := `
		INSERT INTO conversations (tenant_id, contact_id, channel, status, last_message_at)
		VALUES ($1, $2, $3, 'open', NOW())
		ON CONFLICT (tenant_id, contact_id, channel) DO UPDATE
		SET updated_at = NOW()
		RETURNING id, tenant_id, contact_id, channel, status, assigned_to, last_message_text, last_message_at, unread_count, 
		          COALESCE(ai_state, 'active'), ai_paused_until, COALESCE(needs_human_attention, false), created_at, updated_at;
	`

	var conv domain.Conversation
	err := r.db.QueryRowContext(ctx, query, tenantID, contactID, channel).Scan(
		&conv.ID, &conv.TenantID, &conv.ContactID, &conv.Channel, &conv.Status, &conv.AssignedTo,
		&conv.LastMessageText, &conv.LastMessageAt, &conv.UnreadCount,
		&conv.AIState, &conv.AIPausedUntil, &conv.NeedsHuman,
		&conv.CreatedAt, &conv.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create conversation: %w", err)
	}

	return &conv, nil
}

func (r *PostgresInboxRepository) UpdateConversationStatus(ctx context.Context, tenantID, conversationID string, status domain.ConversationStatus) error {
	query := `
		UPDATE conversations
		SET status = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3
	`
	res, err := r.db.ExecContext(ctx, query, status, conversationID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to update conversation status: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("conversation not found")
	}
	return nil
}

func (r *PostgresInboxRepository) AssignConversation(ctx context.Context, tenantID, conversationID string, userID *string) error {
	query := `
		UPDATE conversations
		SET assigned_to = $1, updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3
		  AND ($1::varchar IS NULL OR EXISTS (
			SELECT 1 FROM tenant_members tm
			WHERE tm.tenant_id = $3::uuid AND tm.user_id = $1
		  ) OR EXISTS (
			SELECT 1 FROM users u
			WHERE u.id = $1 AND u.tenant_id = $3::uuid
		  ))
	`
	res, err := r.db.ExecContext(ctx, query, userID, conversationID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to assign conversation: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("conversation not found or assignee is not a workspace member")
	}
	return nil
}

func (r *PostgresInboxRepository) BulkAssignConversations(ctx context.Context, tenantID string, conversationIDs []string, userID *string) error {
	if len(conversationIDs) == 0 {
		return fmt.Errorf("at least one conversation is required")
	}

	query := `
		UPDATE conversations
		SET assigned_to = $1, updated_at = NOW()
		WHERE tenant_id = $2::uuid
		  AND id = ANY($3::uuid[])
		  AND ($1::varchar IS NULL OR EXISTS (
			SELECT 1 FROM tenant_members tm
			WHERE tm.tenant_id = $2::uuid AND tm.user_id = $1
		  ) OR EXISTS (
			SELECT 1 FROM users u
			WHERE u.id = $1 AND u.tenant_id = $2::uuid
		  ))
	`
	result, err := r.db.ExecContext(ctx, query, userID, tenantID, pq.Array(conversationIDs))
	if err != nil {
		return fmt.Errorf("failed to assign conversations: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify conversation assignment: %w", err)
	}
	if rows != int64(len(conversationIDs)) {
		return fmt.Errorf("some conversations were not found in this workspace or the assignee is not a workspace member")
	}
	return nil
}

func (r *PostgresInboxRepository) MarkConversationAsRead(ctx context.Context, tenantID, conversationID string) error {
	query := `
		UPDATE conversations
		SET unread_count = 0, updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`
	_, err := r.db.ExecContext(ctx, query, conversationID, tenantID)
	if err != nil {
		return fmt.Errorf("failed to mark conversation as read: %w", err)
	}
	return nil
}

func (r *PostgresInboxRepository) ListMessages(ctx context.Context, tenantID, conversationID string, limit, offset int) ([]domain.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT 
			id, tenant_id, conversation_id, direction, sender_type, sender_id,
			body, media_url, media_type, platform_message_id, status, error_message,
			COALESCE(is_deleted, false), edited_at, created_at
		FROM messages
		WHERE conversation_id = $1 AND tenant_id = $2
		ORDER BY created_at ASC
		LIMIT $3 OFFSET $4
	`

	rows, err := r.db.QueryContext(ctx, query, conversationID, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	var messages []domain.Message
	for rows.Next() {
		var m domain.Message
		err := rows.Scan(
			&m.ID, &m.TenantID, &m.ConversationID, &m.Direction, &m.SenderType, &m.SenderID,
			&m.Body, &m.MediaURL, &m.MediaType, &m.PlatformMessageID, &m.Status, &m.ErrorMessage,
			&m.IsDeleted, &m.EditedAt, &m.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, m)
	}

	return messages, nil
}

func (r *PostgresInboxRepository) CreateMessage(ctx context.Context, msg *domain.Message) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	if msg.PlatformMessageID != nil && *msg.PlatformMessageID != "" {
		var existingID string
		lookupErr := r.db.QueryRowContext(ctx, `
			SELECT id FROM messages
			WHERE tenant_id = $1 AND platform_message_id = $2
			LIMIT 1
		`, msg.TenantID, *msg.PlatformMessageID).Scan(&existingID)
		if lookupErr == nil {
			msg.ID = existingID
			return domain.ErrMessageAlreadyExists
		}
		if lookupErr != sql.ErrNoRows {
			return fmt.Errorf("failed to check for duplicate platform message: %w", lookupErr)
		}
	}

	query := `
		INSERT INTO messages (
			tenant_id, conversation_id, direction, sender_type, sender_id,
			body, media_url, media_type, platform_message_id, status, error_message, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id;
	`

	err := r.db.QueryRowContext(ctx, query,
		msg.TenantID, msg.ConversationID, msg.Direction, msg.SenderType, msg.SenderID,
		msg.Body, msg.MediaURL, msg.MediaType, msg.PlatformMessageID, msg.Status, msg.ErrorMessage, msg.CreatedAt,
	).Scan(&msg.ID)

	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return domain.ErrMessageAlreadyExists
		}
		return fmt.Errorf("failed to insert message: %w", err)
	}

	// Update conversation metadata
	unreadIncrement := 0
	if msg.Direction == domain.MessageDirectionInbound {
		unreadIncrement = 1
	}

	updateConvQuery := `
		UPDATE conversations
		SET last_message_text = $1,
		    last_message_at = $2,
		    unread_count = unread_count + $3,
		    updated_at = NOW()
		WHERE id = $4 AND tenant_id = $5
	`
	_, _ = r.db.ExecContext(ctx, updateConvQuery, msg.Body, msg.CreatedAt, unreadIncrement, msg.ConversationID, msg.TenantID)

	return nil
}

// UpdateMessageStatusByPlatformID updates a message's delivery/read status using
// the platform-assigned message ID (e.g. WhatsApp message ID from a Receipt event).
// Returns the updated message so the caller can broadcast the status change.
func (r *PostgresInboxRepository) UpdateMessageStatusByPlatformID(ctx context.Context, tenantID, platformMessageID, status string) (*domain.Message, error) {
	query := `
		UPDATE messages
		SET status = CASE
			WHEN status = 'read' THEN 'read'
			WHEN $1 = 'read' THEN 'read'
			WHEN $1 = 'delivered' AND status IN ('sent', 'failed') THEN 'delivered'
			ELSE $1
		END
		WHERE tenant_id = $2 AND platform_message_id = $3
		RETURNING id, tenant_id, conversation_id, direction, sender_type, sender_id,
		          body, media_url, media_type, platform_message_id, status, error_message,
		          COALESCE(is_deleted, false), edited_at, created_at;
	`

	var m domain.Message
	err := r.db.QueryRowContext(ctx, query, status, tenantID, platformMessageID).Scan(
		&m.ID, &m.TenantID, &m.ConversationID, &m.Direction, &m.SenderType, &m.SenderID,
		&m.Body, &m.MediaURL, &m.MediaType, &m.PlatformMessageID, &m.Status, &m.ErrorMessage,
		&m.IsDeleted, &m.EditedAt, &m.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message not found for platform_message_id=%s", platformMessageID)
		}
		return nil, fmt.Errorf("failed to update message status: %w", err)
	}

	return &m, nil
}

// EditMessage updates the body of a message and marks it as edited.
// Only outbound (agent-sent) messages may be edited.
func (r *PostgresInboxRepository) EditMessage(ctx context.Context, tenantID, messageID, newBody string) (*domain.Message, error) {
	query := `
		UPDATE messages
		SET body = $1, edited_at = NOW()
		WHERE id = $2 AND tenant_id = $3 AND direction = 'outbound' AND COALESCE(is_deleted, false) = false
		RETURNING id, tenant_id, conversation_id, direction, sender_type, sender_id,
		          body, media_url, media_type, platform_message_id, status, error_message,
		          COALESCE(is_deleted, false), edited_at, created_at;
	`

	var m domain.Message
	err := r.db.QueryRowContext(ctx, query, newBody, messageID, tenantID).Scan(
		&m.ID, &m.TenantID, &m.ConversationID, &m.Direction, &m.SenderType, &m.SenderID,
		&m.Body, &m.MediaURL, &m.MediaType, &m.PlatformMessageID, &m.Status, &m.ErrorMessage,
		&m.IsDeleted, &m.EditedAt, &m.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message not found or cannot be edited (must be outbound and not deleted)")
		}
		return nil, fmt.Errorf("failed to edit message: %w", err)
	}

	// Update conversation's last_message_text if the edited message is the latest message
	updateConvQuery := `
		UPDATE conversations c
		SET last_message_text = COALESCE(
			(SELECT CASE WHEN is_deleted THEN '🚫 This message was deleted' ELSE body END 
			 FROM messages 
			 WHERE conversation_id = c.id AND tenant_id = c.tenant_id 
			 ORDER BY created_at DESC LIMIT 1),
			$1
		),
		updated_at = NOW()
		WHERE c.id = $2 AND c.tenant_id = $3;
	`
	_, _ = r.db.ExecContext(ctx, updateConvQuery, newBody, m.ConversationID, m.TenantID)

	return &m, nil
}

// DeleteMessage soft-deletes a message by setting is_deleted=true and clearing the body.
// Works for outbound messages only; inbound messages are never deleted from the platform side.
func (r *PostgresInboxRepository) DeleteMessage(ctx context.Context, tenantID, messageID string) (*domain.Message, error) {
	query := `
		UPDATE messages
		SET is_deleted = true, body = ''
		WHERE id = $1 AND tenant_id = $2 AND direction = 'outbound' AND COALESCE(is_deleted, false) = false
		RETURNING id, tenant_id, conversation_id, direction, sender_type, sender_id,
		          body, media_url, media_type, platform_message_id, status, error_message,
		          COALESCE(is_deleted, false), edited_at, created_at;
	`

	var m domain.Message
	err := r.db.QueryRowContext(ctx, query, messageID, tenantID).Scan(
		&m.ID, &m.TenantID, &m.ConversationID, &m.Direction, &m.SenderType, &m.SenderID,
		&m.Body, &m.MediaURL, &m.MediaType, &m.PlatformMessageID, &m.Status, &m.ErrorMessage,
		&m.IsDeleted, &m.EditedAt, &m.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message not found or already deleted")
		}
		return nil, fmt.Errorf("failed to delete message: %w", err)
	}

	// Update conversation's last_message_text to match the latest message state
	updateConvQuery := `
		UPDATE conversations c
		SET last_message_text = COALESCE(
			(SELECT CASE WHEN is_deleted THEN '🚫 This message was deleted' ELSE body END 
			 FROM messages 
			 WHERE conversation_id = c.id AND tenant_id = c.tenant_id 
			 ORDER BY created_at DESC LIMIT 1),
			'🚫 This message was deleted'
		),
		updated_at = NOW()
		WHERE c.id = $1 AND c.tenant_id = $2;
	`
	_, _ = r.db.ExecContext(ctx, updateConvQuery, m.ConversationID, m.TenantID)

	return &m, nil
}
