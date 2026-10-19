package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/lib/pq"
	"omnipulse/apps/api-gateway/internal/utils"
)

// AssignmentNotifier is deliberately separate from the assignment use case:
// notification delivery must never determine whether an assignment succeeds.
type AssignmentNotifier interface {
	NotifyConversationAssignment(ctx context.Context, tenantID, conversationID string, previousAssignee *string)
	NotifyBulkConversationAssignment(ctx context.Context, tenantID string, conversationIDs []string, previousAssignees map[string]*string)
}

type assignmentEmailNotifier struct {
	db       *sql.DB
	mailer   *utils.Mailer
	inboxURL string
	logger   *log.Logger
}

// NewAssignmentEmailNotifier creates the production assignment notifier. A
// missing mailer or database is treated as disabled and is safe for tests.
func NewAssignmentEmailNotifier(db *sql.DB, mailer *utils.Mailer, publicAppBaseURL string, logger *log.Logger) AssignmentNotifier {
	return &assignmentEmailNotifier{
		db:       db,
		mailer:   mailer,
		inboxURL: strings.TrimRight(publicAppBaseURL, "/") + "/inbox",
		logger:   logger,
	}
}

type assignmentEmailRow struct {
	email         string
	recipientName string
	workspaceName string
	contactName   string
	channel       string
}

func (n *assignmentEmailNotifier) NotifyConversationAssignment(ctx context.Context, tenantID, conversationID string, previousAssignee *string) {
	if n == nil || n.db == nil || n.mailer == nil || conversationID == "" {
		return
	}

	var currentAssignee string
	if err := n.db.QueryRowContext(ctx, `SELECT COALESCE(assigned_to, '') FROM conversations WHERE id=$1 AND tenant_id=$2`, conversationID, tenantID).Scan(&currentAssignee); err != nil {
		n.warn("single assignment lookup", err)
		return
	}
	if currentAssignee == "" || (previousAssignee != nil && *previousAssignee == currentAssignee) {
		return
	}

	rows, err := n.fetchRows(ctx, tenantID, []string{conversationID}, currentAssignee)
	if err != nil {
		n.warn("single assignment recipient lookup", err)
		return
	}
	n.sendGrouped(rows)
}

func (n *assignmentEmailNotifier) NotifyBulkConversationAssignment(ctx context.Context, tenantID string, conversationIDs []string, previousAssignees map[string]*string) {
	if n == nil || n.db == nil || n.mailer == nil || len(conversationIDs) == 0 {
		return
	}

	// Only include conversations whose assignee changed to a real teammate.
	changedIDs := make([]string, 0, len(conversationIDs))
	for _, conversationID := range conversationIDs {
		var currentAssignee sql.NullString
		if err := n.db.QueryRowContext(ctx, `SELECT assigned_to FROM conversations WHERE id=$1 AND tenant_id=$2`, conversationID, tenantID).Scan(&currentAssignee); err != nil {
			n.warn("bulk assignment lookup", err)
			continue
		}
		if !currentAssignee.Valid || currentAssignee.String == "" {
			continue
		}
		previous := previousAssignees[conversationID]
		if previous != nil && *previous == currentAssignee.String {
			continue
		}
		changedIDs = append(changedIDs, conversationID)
	}
	if len(changedIDs) == 0 {
		return
	}

	rows, err := n.fetchRows(ctx, tenantID, changedIDs, "")
	if err != nil {
		n.warn("bulk assignment recipient lookup", err)
		return
	}
	n.sendGrouped(rows)
}

func (n *assignmentEmailNotifier) fetchRows(ctx context.Context, tenantID string, conversationIDs []string, assigneeID string) ([]assignmentEmailRow, error) {
	query := `
SELECT u.email,
       TRIM(CONCAT(COALESCE(u.first_name, ''), ' ', COALESCE(u.last_name, ''))),
       t.company_name,
       COALESCE(NULLIF(TRIM(CONCAT(COALESCE(ct.first_name, ''), ' ', COALESCE(ct.last_name, ''))), ''), ct.routing_value),
       c.channel
FROM conversations c
JOIN users u ON u.id = c.assigned_to AND u.tenant_id = c.tenant_id
JOIN tenants t ON t.id = c.tenant_id
JOIN contacts ct ON ct.id = c.contact_id
WHERE c.tenant_id = $1 AND c.id = ANY($2) AND c.assigned_to IS NOT NULL`
	args := []interface{}{tenantID, pq.Array(conversationIDs)}
	if assigneeID != "" {
		query += " AND c.assigned_to = $3"
		args = append(args, assigneeID)
	}

	dbRows, err := n.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query assignment recipients: %w", err)
	}
	defer dbRows.Close()

	var result []assignmentEmailRow
	for dbRows.Next() {
		var row assignmentEmailRow
		if err := dbRows.Scan(&row.email, &row.recipientName, &row.workspaceName, &row.contactName, &row.channel); err != nil {
			return nil, fmt.Errorf("scan assignment recipient: %w", err)
		}
		result = append(result, row)
	}
	if err := dbRows.Err(); err != nil {
		return nil, fmt.Errorf("read assignment recipients: %w", err)
	}
	return result, nil
}

func (n *assignmentEmailNotifier) sendGrouped(rows []assignmentEmailRow) {
	grouped := make(map[string]struct {
		name        string
		workspace   string
		assignments []utils.ConversationAssignment
	})
	for _, row := range rows {
		if strings.TrimSpace(row.email) == "" {
			continue
		}
		group := grouped[row.email]
		group.name = row.recipientName
		group.workspace = row.workspaceName
		group.assignments = append(group.assignments, utils.ConversationAssignment{ContactName: row.contactName, Channel: row.channel})
		grouped[row.email] = group
	}
	for email, group := range grouped {
		n.mailer.SendConversationAssignments(email, group.name, group.workspace, n.inboxURL, group.assignments)
	}
}

func (n *assignmentEmailNotifier) warn(operation string, err error) {
	if n.logger != nil {
		n.logger.Printf("[INBOX-ASSIGNMENT] Email notification %s skipped: %v", operation, err)
	}
}
