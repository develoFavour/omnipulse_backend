package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

type WhatsAppManager struct {
	db            *sql.DB
	container     *sqlstore.Container
	encryptionKey string
	inboxRepo     domain.InboxRepository
	inboxHub      *InboxHub
	clients       sync.Map // map[string]*tenantSession (key: tenantID)
	mu            sync.Mutex
}

type tenantSession struct {
	client     *whatsmeow.Client
	qrCode     string
	status     string // "waiting_scan", "connected", "error"
	phone      string
	name       string
	lastQRTime time.Time
	mu         sync.RWMutex
}

func NewWhatsAppManager(db *sql.DB, encryptionKey string) (*WhatsAppManager, error) {
	// Set official companion OS properties for WhatsApp Web protocol
	store.SetOSInfo("Chrome (Windows)", [3]uint32{128, 0, 0})

	logger := waLog.Stdout("WA-Database", "WARN", true)
	container := sqlstore.NewWithDB(db, "postgres", logger)
	err := container.Upgrade(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to upgrade whatsmeow sqlstore: %w", err)
	}

	mgr := &WhatsAppManager{
		db:            db,
		container:     container,
		encryptionKey: encryptionKey,
	}

	// Restore all existing connected devices on startup
	go mgr.restoreExistingSessions()

	return mgr, nil
}

func (m *WhatsAppManager) SetInboxRepo(repo domain.InboxRepository) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inboxRepo = repo
}

func (m *WhatsAppManager) SetInboxHub(hub *InboxHub) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inboxHub = hub
}

func (m *WhatsAppManager) restoreExistingSessions() {
	devices, err := m.container.GetAllDevices(context.Background())
	if err != nil {
		log.Printf("[WhatsAppManager] Warning: failed to fetch devices: %v\n", err)
		return
	}

	rows, err := m.db.Query(
		`SELECT tenant_id, sender_identity, encrypted_credentials, status FROM tenant_channels WHERE platform_name = 'whatsapp' AND status = 'active'`,
	)
	if err != nil {
		log.Printf("[WhatsAppManager] Warning: failed to query tenant_channels: %v\n", err)
		return
	}
	defer rows.Close()

	type chRecord struct {
		tenantID       string
		senderIdentity string
		encCreds       []byte
		status         string
	}
	var channels []chRecord
	for rows.Next() {
		var ch chRecord
		if err := rows.Scan(&ch.tenantID, &ch.senderIdentity, &ch.encCreds, &ch.status); err == nil {
			channels = append(channels, ch)
		}
	}

	for _, dev := range devices {
		if dev.ID == nil {
			continue
		}

		var matchedTenantID, matchedIdentity string
		phone := dev.ID.User

		for _, ch := range channels {
			if strings.Contains(ch.senderIdentity, phone) {
				matchedTenantID = ch.tenantID
				matchedIdentity = ch.senderIdentity
				break
			}
			if len(ch.encCreds) > 0 {
				plain, decErr := utils.DecryptBytes(ch.encCreds, m.encryptionKey)
				if decErr == nil {
					var creds map[string]interface{}
					if json.Unmarshal(plain, &creds) == nil {
						if jidStr, _ := creds["jid"].(string); jidStr == dev.ID.String() {
							matchedTenantID = ch.tenantID
							matchedIdentity = ch.senderIdentity
							break
						}
						if credPhone, _ := creds["phone"].(string); credPhone == phone {
							matchedTenantID = ch.tenantID
							matchedIdentity = ch.senderIdentity
							break
						}
					}
				}
			}
		}

		if matchedTenantID == "" {
			log.Printf("[WhatsAppManager] No matching tenant found for device JID %s\n", dev.ID.String())
			continue
		}

		clientLog := waLog.Stdout(fmt.Sprintf("WA-%s", matchedTenantID[:8]), "WARN", true)
		client := whatsmeow.NewClient(dev, clientLog)
		sess := &tenantSession{
			client: client,
			status: "connected",
			phone:  phone,
			name:   matchedIdentity,
		}

		m.setupEventHandler(sess, matchedTenantID)
		if err := client.Connect(); err != nil {
			log.Printf("[WhatsAppManager] Failed to auto-connect session for tenant %s: %v\n", matchedTenantID, err)
		} else {
			log.Printf("[WhatsAppManager] Restored WhatsApp session for tenant %s (JID: %s)\n", matchedTenantID, dev.ID.String())
			m.clients.Store(matchedTenantID, sess)
		}
	}
}

func (m *WhatsAppManager) GetQR(ctx context.Context, tenantID string) (qrCode string, status string, phone string, name string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if active session exists
	if val, ok := m.clients.Load(tenantID); ok {
		sess := val.(*tenantSession)
		sess.mu.RLock()
		if sess.client.IsConnected() && sess.client.IsLoggedIn() {
			sess.mu.RUnlock()
			return "", "connected", sess.phone, sess.name, nil
		}
		// If client is already connected and actively waiting for scan, return current QR
		if sess.client.IsConnected() && sess.qrCode != "" {
			qr := sess.qrCode
			sess.mu.RUnlock()
			return qr, "waiting_scan", "", "", nil
		}
		sess.mu.RUnlock()

		// Disconnect previous stale client before creating a new one
		sess.client.Disconnect()
		m.clients.Delete(tenantID)
	}

	// Create a new device store for pairing
	deviceStore := m.container.NewDevice()
	clientLog := waLog.Stdout(fmt.Sprintf("WA-%s", tenantID[:8]), "INFO", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)

	sess := &tenantSession{
		client: client,
		status: "waiting_scan",
	}

	m.setupEventHandler(sess, tenantID)

	// Use persistent context so pairing channel and socket stay alive after the HTTP request finishes
	qrChan, err := client.GetQRChannel(context.Background())
	if err != nil {
		return "", "error", "", "", fmt.Errorf("failed to get QR channel: %w", err)
	}

	if err := client.Connect(); err != nil {
		return "", "error", "", "", fmt.Errorf("failed to connect client: %w", err)
	}

	m.clients.Store(tenantID, sess)

	// Wait for the first QR event from channel
	select {
	case evt, ok := <-qrChan:
		if !ok {
			client.Disconnect()
			m.clients.Delete(tenantID)
			return "", "error", "", "", fmt.Errorf("QR channel closed unexpectedly")
		}
		if evt.Event == "code" {
			sess.mu.Lock()
			sess.qrCode = evt.Code
			sess.lastQRTime = time.Now()
			sess.mu.Unlock()

			// Start background listener for subsequent QR updates or pairing
			go m.listenQRChannel(qrChan, sess, tenantID)

			return evt.Code, "waiting_scan", "", "", nil
		} else if evt.Event == "success" {
			sess.mu.Lock()
			sess.status = "connected"
			sess.mu.Unlock()
			return "", "connected", sess.phone, sess.name, nil
		}
	case <-time.After(15 * time.Second):
		client.Disconnect()
		m.clients.Delete(tenantID)
		return "", "timeout", "", "", fmt.Errorf("timeout waiting for initial QR code")
	}

	client.Disconnect()
	m.clients.Delete(tenantID)
	return "", "error", "", "", fmt.Errorf("no QR code generated")
}

func (m *WhatsAppManager) listenQRChannel(qrChan <-chan whatsmeow.QRChannelItem, sess *tenantSession, tenantID string) {
	for evt := range qrChan {
		sess.mu.Lock()
		switch evt.Event {
		case "code":
			sess.qrCode = evt.Code
			sess.lastQRTime = time.Now()
			sess.status = "waiting_scan"
		case "success":
			sess.qrCode = ""
			sess.status = "connected"
		case "timeout":
			sess.qrCode = ""
			sess.status = "timeout"
		}
		sess.mu.Unlock()
	}
}

func (m *WhatsAppManager) setupEventHandler(sess *tenantSession, tenantID string) {
	sess.client.AddEventHandler(func(rawEvt interface{}) {
		switch evt := rawEvt.(type) {
		case *events.Connected:
			log.Printf("[WhatsAppManager] Tenant %s connected to WhatsApp servers\n", tenantID)
		case *events.PairSuccess:
			jid := evt.ID
			phone := jid.User
			name := phone

			// Try fetching push name / contact info
			if contact, err := sess.client.Store.Contacts.GetContact(context.Background(), jid); err == nil && contact.PushName != "" {
				name = contact.PushName
			}

			senderIdentity := fmt.Sprintf("%s (+%s)", name, phone)

			sess.mu.Lock()
			sess.phone = phone
			sess.name = name
			sess.status = "connected"
			sess.qrCode = ""
			sess.mu.Unlock()

			log.Printf("[WhatsAppManager] ✅ Pair Success for tenant %s: %s (JID: %s)\n", tenantID, senderIdentity, jid.String())

			// Save/Update in PostgreSQL
			creds, _ := json.Marshal(map[string]interface{}{
				"jid":        jid.String(),
				"phone":      phone,
				"name":       name,
				"paired_at":  time.Now().UTC().Format(time.RFC3339),
				"connection": "multi_device_qr",
			})

			// Encrypt credentials at rest
			if encCreds, encErr := utils.EncryptBytes(creds, m.encryptionKey); encErr == nil {
				creds = encCreds
			}

			_, err := m.db.Exec(`
				INSERT INTO tenant_channels (tenant_id, platform_name, sender_identity, encrypted_credentials, status, updated_at)
				VALUES ($1, 'whatsapp', $2, $3, 'active', NOW())
				ON CONFLICT (tenant_id, platform_name)
				DO UPDATE SET
					sender_identity = EXCLUDED.sender_identity,
					encrypted_credentials = EXCLUDED.encrypted_credentials,
					status = 'active',
					updated_at = NOW()
			`, tenantID, senderIdentity, creds)
			if err != nil {
				log.Printf("[WhatsAppManager] Error persisting paired channel to DB: %v\n", err)
			}

		case *events.HistorySync:
			log.Printf("[WhatsAppManager] 🔄 Received HistorySync (%v) for tenant %s\n", evt.Data.GetSyncType(), tenantID)
			count := 0
			// 1. Process Conversations
			for _, conv := range evt.Data.GetConversations() {
				chatID := conv.GetID()
				if chatID == "" {
					continue
				}
				jid, err := types.ParseJID(chatID)
				if err != nil || jid.Server != types.DefaultUserServer || jid.User == "" {
					continue
				}
				name := conv.GetName()
				m.saveContact(tenantID, jid.User, name, "whatsapp_sync")
				count++
			}
			// 2. Process Pushnames
			for _, pn := range evt.Data.GetPushnames() {
				jidStr := pn.GetID()
				if jidStr == "" {
					continue
				}
				jid, err := types.ParseJID(jidStr)
				if err != nil || jid.Server != types.DefaultUserServer || jid.User == "" {
					continue
				}
				m.saveContact(tenantID, jid.User, pn.GetPushname(), "whatsapp_sync")
				count++
			}
			log.Printf("[WhatsAppManager] 📥 Auto-imported %d contacts from HistorySync for tenant %s\n", count, tenantID)

		case *events.PushName:
			if evt.JID.Server == types.DefaultUserServer && evt.JID.User != "" && evt.NewPushName != "" {
				m.saveContact(tenantID, evt.JID.User, evt.NewPushName, "whatsapp_sync")
			}

		case *events.Contact:
			if evt.JID.Server == types.DefaultUserServer && evt.JID.User != "" && evt.Action != nil {
				name := evt.Action.GetFullName()
				if name == "" {
					name = evt.Action.GetFirstName()
				}
				m.saveContact(tenantID, evt.JID.User, name, "whatsapp_sync")
			}

		case *events.Message:
			log.Printf("[WhatsAppManager] 📩 Received events.Message for tenant %s: IsFromMe=%v, Chat=%s, Sender=%s, PushName=%q, MediaType=%s\n",
				tenantID, evt.Info.IsFromMe, evt.Info.Chat.String(), evt.Info.Sender.String(), evt.Info.PushName, evt.Info.MediaType)

			var contactPhone string
			var pushName string
			direction := domain.MessageDirectionInbound
			senderType := domain.MessageSenderTypeContact

			if evt.Info.IsFromMe {
				// Message was sent from this connected WhatsApp account (phone or web)
				if evt.Info.Chat.Server == types.DefaultUserServer && evt.Info.Chat.User != "" {
					contactPhone = evt.Info.Chat.User
					direction = domain.MessageDirectionOutbound
					senderType = domain.MessageSenderTypeAgent
				} else if evt.Info.Chat.Server == types.HiddenUserServer && evt.Info.Chat.User != "" {
					contactPhone = evt.Info.Chat.User
					direction = domain.MessageDirectionOutbound
					senderType = domain.MessageSenderTypeAgent
				}
			} else {
				// Inbound message received from contact (1-on-1 chats)
				if !evt.Info.IsGroup {
					pushName = evt.Info.PushName
					// 1. Check for standard phone number on s.whatsapp.net
					if evt.Info.Chat.Server == types.DefaultUserServer && evt.Info.Chat.User != "" {
						contactPhone = evt.Info.Chat.User
					} else if evt.Info.Sender.Server == types.DefaultUserServer && evt.Info.Sender.User != "" {
						contactPhone = evt.Info.Sender.User
					} else if sess.client != nil && sess.client.Store != nil {
						// 2. Try resolving phone number from LID store
						queryCtx, qCancel := context.WithTimeout(context.Background(), 2*time.Second)
						if pn, err := sess.client.Store.GetAltJID(queryCtx, evt.Info.Chat); err == nil && pn.Server == types.DefaultUserServer && pn.User != "" {
							contactPhone = pn.User
							log.Printf("[WhatsAppManager] 🔀 Resolved LID %s to phone %s via AltJID\n", evt.Info.Chat.String(), pn.User)
						} else if sess.client.Store.LIDs != nil {
							if pn, err := sess.client.Store.LIDs.GetPNForLID(queryCtx, evt.Info.Chat); err == nil && pn.Server == types.DefaultUserServer && pn.User != "" {
								contactPhone = pn.User
								log.Printf("[WhatsAppManager] 🔀 Resolved LID %s to phone %s via LIDStore\n", evt.Info.Chat.String(), pn.User)
							}
						}
						qCancel()
					}

					// 3. If phone number could not be resolved from LID store, use the LID identifier directly
					if contactPhone == "" {
						if evt.Info.Chat.User != "" {
							contactPhone = evt.Info.Chat.User
						} else if evt.Info.Sender.User != "" {
							contactPhone = evt.Info.Sender.User
						}
						log.Printf("[WhatsAppManager] ℹ️ Using LID user as contact identifier: %s (name: %q)\n", contactPhone, pushName)
					}
				}
			}

			if contactPhone != "" {
				contactID := m.saveContact(tenantID, contactPhone, pushName, "whatsapp_sync")
				text := extractMessageText(evt.Message)
				if text != "" && contactID != "" && m.inboxRepo != nil {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					conv, cErr := m.inboxRepo.GetOrCreateConversation(ctx, tenantID, contactID, "whatsapp")
					if cErr == nil && conv != nil {
						// For outbound messages (echoed back when sent from this device/session):
						// Check if Omnipulse already recorded this exact message in the last 10 seconds (avoids doubling messages sent from dashboard)
						if direction == domain.MessageDirectionOutbound {
							var existingCount int
							_ = m.db.QueryRowContext(ctx, `
								SELECT COUNT(*) FROM messages 
								WHERE tenant_id = $1 AND conversation_id = $2 AND direction = 'outbound' AND body = $3 
								  AND created_at >= NOW() - INTERVAL '10 seconds'
							`, tenantID, conv.ID, text).Scan(&existingCount)
							if existingCount > 0 {
								log.Printf("[WhatsAppManager] ⏩ Skipping duplicate outbound message echo for tenant %s (already recorded in dashboard)\n", tenantID)
								return
							}
						}

						msgID := evt.Info.ID
						initialStatus := "sent"
						if direction == domain.MessageDirectionInbound {
							initialStatus = "delivered" // inbound messages are implicitly delivered to us
						}
						newMsg := &domain.Message{
							TenantID:          tenantID,
							ConversationID:    conv.ID,
							Direction:         direction,
							SenderType:        senderType,
							Body:              text,
							PlatformMessageID: &msgID,
							Status:            initialStatus,
						}
						_ = m.inboxRepo.CreateMessage(ctx, newMsg)
						if m.inboxHub != nil {
							m.inboxHub.BroadcastMessage(tenantID, conv, newMsg)
						}
						log.Printf("[WhatsAppManager] 💬 Synced %s message for tenant %s with %s (conv %s): %q\n", direction, tenantID, contactPhone, conv.ID, text)
					} else {
						log.Printf("[WhatsAppManager] ⚠️ Failed to create/find conversation for contact %s: %v\n", contactID, cErr)
					}
				} else {
					log.Printf("[WhatsAppManager] ⚠️ Message skipped: text_empty=%v (len=%d), contactID_empty=%v, inboxRepo_nil=%v (phone=%s)\n",
						text == "", len(text), contactID == "", m.inboxRepo == nil, contactPhone)
				}
			} else {
				log.Printf("[WhatsAppManager] ℹ️ Message ignored (no 1-on-1 contact phone resolved: Chat=%s, Sender=%s, IsGroup=%v)\n",
					evt.Info.Chat.String(), evt.Info.Sender.String(), evt.Info.IsGroup)
			}

		case *events.Receipt:
			// WhatsApp receipt event: fired when messages are delivered or read.
			// ReceiptType "" (empty string) = delivered (grey double-tick)
			// ReceiptType "read" / "read-self" / "played" = read (blue/sky double-tick)
			var newStatus string
			switch evt.Type {
			case types.ReceiptTypeRead, types.ReceiptTypeReadSelf, types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
				newStatus = "read"
			case types.ReceiptTypeDelivered, types.ReceiptTypeSender:
				newStatus = "delivered"
			default:
				log.Printf("[WhatsAppManager] ℹ️ Ignoring non-delivery receipt type: %v (tenant: %s)\n", evt.Type, tenantID)
				return
			}

			if m.inboxRepo == nil || len(evt.MessageIDs) == 0 {
				break
			}

			ctx, rCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer rCancel()

			for _, msgID := range evt.MessageIDs {
				updated, err := m.inboxRepo.UpdateMessageStatusByPlatformID(ctx, tenantID, msgID, newStatus)
				if err != nil {
					// Message may not be in our DB (e.g. sent from phone before integration)
					log.Printf("[WhatsAppManager] ℹ️ Receipt for unknown msg %s (tenant %s): %v\n", msgID, tenantID, err)
					continue
				}
				log.Printf("[WhatsAppManager] ✅ Receipt: msg %s → %s (conv %s, tenant %s)\n", msgID, newStatus, updated.ConversationID, tenantID)
				if m.inboxHub != nil {
					m.inboxHub.BroadcastStatusUpdate(tenantID, updated.ConversationID, updated.ID, newStatus)
				}
			}

		case *events.LoggedOut:

			log.Printf("[WhatsAppManager] ⚠️ Tenant %s was logged out: %v\n", tenantID, evt.Reason)
			sess.mu.Lock()
			sess.status = "disconnected"
			sess.qrCode = ""
			sess.phone = ""
			sess.mu.Unlock()

			_, _ = m.db.Exec(`
				UPDATE tenant_channels SET status = 'inactive', updated_at = NOW()
				WHERE tenant_id = $1 AND platform_name = 'whatsapp'
			`, tenantID)
		}
	})
}

func extractMessageText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if msg.Conversation != nil && *msg.Conversation != "" {
		return *msg.Conversation
	}
	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil && *msg.ExtendedTextMessage.Text != "" {
		return *msg.ExtendedTextMessage.Text
	}
	if msg.ImageMessage != nil {
		if msg.ImageMessage.Caption != nil && *msg.ImageMessage.Caption != "" {
			return *msg.ImageMessage.Caption
		}
		return "[Photo]"
	}
	if msg.VideoMessage != nil {
		if msg.VideoMessage.Caption != nil && *msg.VideoMessage.Caption != "" {
			return *msg.VideoMessage.Caption
		}
		return "[Video]"
	}
	if msg.AudioMessage != nil {
		return "[Voice message]"
	}
	if msg.DocumentMessage != nil {
		if msg.DocumentMessage.Caption != nil && *msg.DocumentMessage.Caption != "" {
			return *msg.DocumentMessage.Caption
		}
		if msg.DocumentMessage.FileName != nil && *msg.DocumentMessage.FileName != "" {
			return "[" + *msg.DocumentMessage.FileName + "]"
		}
		return "[Document]"
	}
	if msg.StickerMessage != nil {
		return "[Sticker]"
	}
	if msg.ContactMessage != nil {
		return "[Contact card]"
	}
	if msg.LocationMessage != nil {
		return "[Location]"
	}
	return ""
}

func (m *WhatsAppManager) saveContact(tenantID, rawPhone, name, source string) string {
	phone := strings.TrimPrefix(strings.TrimSpace(rawPhone), "+")
	if len(phone) < 7 {
		return ""
	}

	routingValue := "+" + phone

	// Skip saving the tenant's own phone number as an audience contact,
	// UNLESS it was already explicitly added to contacts (e.g. for developer/owner testing)
	if val, ok := m.clients.Load(tenantID); ok {
		sess := val.(*tenantSession)
		sess.mu.RLock()
		selfPhone := strings.TrimPrefix(strings.TrimSpace(sess.phone), "+")
		sess.mu.RUnlock()
		if selfPhone != "" && selfPhone == phone {
			var existingID string
			execCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := m.db.QueryRowContext(execCtx, `
				SELECT id FROM contacts WHERE tenant_id = $1 AND channel = 'whatsapp' AND routing_value = $2
			`, tenantID, routingValue).Scan(&existingID)
			cancel()
			if err == nil && existingID != "" {
				return existingID
			}
			return ""
		}
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = "WhatsApp User"
	}
	parts := strings.SplitN(name, " ", 2)
	fn := parts[0]
	ln := ""
	if len(parts) > 1 {
		ln = parts[1]
	}

	execCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var contactID string
	err := m.db.QueryRowContext(execCtx, `
		INSERT INTO contacts (tenant_id, first_name, last_name, channel, routing_value, source, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'whatsapp', $4, $5, 'active', NOW(), NOW())
		ON CONFLICT (tenant_id, channel, routing_value)
		DO UPDATE SET
			first_name = CASE WHEN contacts.first_name IN ('WhatsApp User', 'WhatsApp Contact') AND EXCLUDED.first_name NOT IN ('WhatsApp User', 'WhatsApp Contact') THEN EXCLUDED.first_name ELSE contacts.first_name END,
			last_name = CASE WHEN contacts.last_name = '' AND EXCLUDED.last_name != '' THEN EXCLUDED.last_name ELSE contacts.last_name END,
			updated_at = NOW()
		RETURNING id;
	`, tenantID, fn, ln, routingValue, source).Scan(&contactID)
	if err != nil {
		log.Printf("[WhatsAppManager] Warning: failed to save contact %s (%s): %v\n", name, routingValue, err)
		return ""
	}
	return contactID
}

// ensureSession guarantees an active, connected WhatsApp client session for the tenant,
// automatically re-establishing connections from the database store on-demand if disconnected.
func (m *WhatsAppManager) ensureSession(ctx context.Context, tenantID string) (*tenantSession, error) {
	if val, ok := m.clients.Load(tenantID); ok {
		sess := val.(*tenantSession)
		sess.mu.RLock()
		connected := sess.client != nil && sess.client.IsConnected() && sess.client.IsLoggedIn()
		sess.mu.RUnlock()
		if connected {
			return sess, nil
		}
		// If in memory but temporarily disconnected, attempt quick reconnect
		if sess.client != nil && !sess.client.IsConnected() {
			_ = sess.client.Connect()
			if sess.client.WaitForConnection(5 * time.Second) {
				sess.mu.Lock()
				sess.status = "connected"
				sess.mu.Unlock()
				return sess, nil
			}
		}
	}

	// Look up tenant's paired device in PostgreSQL
	var senderIdentity string
	var credsJSON []byte
	err := m.db.QueryRowContext(ctx, `
		SELECT sender_identity, encrypted_credentials
		FROM tenant_channels
		WHERE tenant_id = $1 AND platform_name = 'whatsapp' AND status = 'active'
		LIMIT 1
	`, tenantID).Scan(&senderIdentity, &credsJSON)
	if err != nil || len(credsJSON) == 0 {
		return nil, fmt.Errorf("no active WhatsApp session for tenant %s. Please connect WhatsApp first", tenantID)
	}

	if decrypted, decErr := utils.DecryptBytes(credsJSON, m.encryptionKey); decErr == nil {
		credsJSON = decrypted
	}

	var creds map[string]interface{}
	_ = json.Unmarshal(credsJSON, &creds)
	jidStr, _ := creds["jid"].(string)
	if jidStr == "" {
		return nil, fmt.Errorf("no WhatsApp device JID configured for tenant %s", tenantID)
	}

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid WhatsApp JID format in channel credentials: %w", err)
	}

	dev, devErr := m.container.GetDevice(ctx, jid)
	if devErr != nil || dev == nil {
		return nil, fmt.Errorf("WhatsApp device session for %s not found in store: %w", jidStr, devErr)
	}

	clientLog := waLog.Stdout(fmt.Sprintf("WA-%s", tenantID[:8]), "WARN", true)
	client := whatsmeow.NewClient(dev, clientLog)
	sess := &tenantSession{
		client: client,
		status: "connected",
		phone:  dev.ID.User,
		name:   senderIdentity,
	}
	m.setupEventHandler(sess, tenantID)

	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("failed to initiate WhatsApp connection: %w", err)
	}

	if !client.WaitForConnection(8 * time.Second) {
		return nil, fmt.Errorf("timeout connecting to WhatsApp servers for tenant %s", tenantID)
	}

	m.clients.Store(tenantID, sess)
	log.Printf("[WhatsAppManager] Auto-reconnected WhatsApp session for tenant %s (JID: %s)\n", tenantID, jid.String())
	return sess, nil
}

func (m *WhatsAppManager) GetStatus(ctx context.Context, tenantID string) (status string, phone string, name string, err error) {
	// Try self-healing connection first
	sess, err := m.ensureSession(ctx, tenantID)
	if err == nil && sess != nil {
		sess.mu.RLock()
		defer sess.mu.RUnlock()
		return "connected", sess.phone, sess.name, nil
	}

	// Check DB fallback
	var senderIdentity, chStatus string
	var credsJSON []byte
	err = m.db.QueryRowContext(
		ctx,
		`SELECT sender_identity, encrypted_credentials, status FROM tenant_channels WHERE tenant_id = $1 AND platform_name = 'whatsapp'`,
		tenantID,
	).Scan(&senderIdentity, &credsJSON, &chStatus)

	if err != nil {
		return "disconnected", "", "", nil
	}

	if decrypted, decErr := utils.DecryptBytes(credsJSON, m.encryptionKey); decErr == nil {
		credsJSON = decrypted
	}

	var creds map[string]interface{}
	_ = json.Unmarshal(credsJSON, &creds)
	phoneStr, _ := creds["phone"].(string)

	if chStatus == "active" {
		return "connected", phoneStr, senderIdentity, nil
	}
	return "disconnected", "", "", nil
}

func (m *WhatsAppManager) Disconnect(ctx context.Context, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if val, ok := m.clients.Load(tenantID); ok {
		sess := val.(*tenantSession)
		if sess.client.IsConnected() {
			_ = sess.client.Logout(ctx)
			sess.client.Disconnect()
		}
		m.clients.Delete(tenantID)
	}

	_, err := m.db.ExecContext(ctx, `
		UPDATE tenant_channels SET status = 'inactive', updated_at = NOW()
		WHERE tenant_id = $1 AND platform_name = 'whatsapp'
	`, tenantID)
	return err
}

// Close gracefully terminates all active WhatsApp Multi-Device client connections on shutdown
func (m *WhatsAppManager) Close() {
	m.clients.Range(func(key, val interface{}) bool {
		if sess, ok := val.(*tenantSession); ok {
			sess.mu.Lock()
			if sess.client != nil && sess.client.IsConnected() {
				sess.client.Disconnect()
			}
			sess.mu.Unlock()
		}
		return true
	})
	log.Println("[WhatsAppManager] All WhatsApp sessions cleanly disconnected.")
}

func (m *WhatsAppManager) resolveTargetJID(ctx context.Context, sess *tenantSession, recipientPhone string) types.JID {
	phoneClean := strings.TrimPrefix(strings.TrimSpace(recipientPhone), "+")
	targetJID := types.NewJID(phoneClean, types.DefaultUserServer)

	// If phoneClean was stored from an LID, check if it maps to an alt JID
	if sess.client != nil && sess.client.Store != nil {
		lidJID := types.NewJID(phoneClean, types.HiddenUserServer)
		if alt, err := sess.client.Store.GetAltJID(ctx, lidJID); err == nil && !alt.IsEmpty() {
			targetJID = alt
		}
	}

	foundOnWA := false
	if sess.client != nil {
		if onWaResp, err := sess.client.IsOnWhatsApp(ctx, []string{phoneClean, recipientPhone}); err == nil && len(onWaResp) > 0 {
			for _, r := range onWaResp {
				if r.IsIn {
					targetJID = r.JID
					foundOnWA = true
					break
				}
			}
		}
	}

	// If not found on standard WhatsApp and identifier has 15+ digits (LID format), use LID server
	if !foundOnWA && len(phoneClean) >= 15 && targetJID.Server != types.HiddenUserServer {
		targetJID = types.NewJID(phoneClean, types.HiddenUserServer)
	}

	return targetJID
}

func (m *WhatsAppManager) SendMessage(ctx context.Context, tenantID string, recipientPhone string, text string, mediaURL *string) (string, error) {
	sess, err := m.ensureSession(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("WhatsApp client is not connected for tenant %s: %w", tenantID, err)
	}

	targetJID := m.resolveTargetJID(ctx, sess, recipientPhone)

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	if mediaURL != nil && *mediaURL != "" {
		imgReq, imgReqErr := http.NewRequestWithContext(ctx, "GET", *mediaURL, nil)
		if imgReqErr == nil {
			imgResp, imgErr := http.DefaultClient.Do(imgReq)
			if imgErr == nil && imgResp.StatusCode == http.StatusOK {
				imgBytes, readErr := io.ReadAll(imgResp.Body)
				imgResp.Body.Close()
				if readErr == nil && len(imgBytes) > 0 {
					uploaded, upErr := sess.client.Upload(ctx, imgBytes, whatsmeow.MediaImage)
					if upErr == nil {
						msg = &waE2E.Message{
							ImageMessage: &waE2E.ImageMessage{
								Caption:       proto.String(text),
								Mimetype:      proto.String(http.DetectContentType(imgBytes)),
								URL:           &uploaded.URL,
								DirectPath:    &uploaded.DirectPath,
								MediaKey:      uploaded.MediaKey,
								FileEncSHA256: uploaded.FileEncSHA256,
								FileSHA256:    uploaded.FileSHA256,
								FileLength:    proto.Uint64(uint64(len(imgBytes))),
							},
						}
					} else {
						log.Printf("[WhatsAppManager] Warning: failed to upload image attachment: %v (falling back to text)\n", upErr)
					}
				}
			}
		}
	}

	resp, err := sess.client.SendMessage(ctx, targetJID, msg)
	if err != nil {
		return "", err
	}
	log.Printf("[WhatsAppManager] ✅ Dispatched message to %s (%s) [MsgID: %s]\n", recipientPhone, targetJID.String(), resp.ID)
	return resp.ID, nil
}

// RevokeMessage issues an official WhatsApp revocation ("delete for everyone")
// for a previously sent message. The recipient's WhatsApp client will replace
// the bubble with "This message was deleted".
func (m *WhatsAppManager) RevokeMessage(ctx context.Context, tenantID string, recipientPhone string, platformMsgID string) error {
	sess, err := m.ensureSession(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("WhatsApp client is not connected for tenant %s: %w", tenantID, err)
	}

	targetJID := m.resolveTargetJID(ctx, sess, recipientPhone)
	revokeMsg := sess.client.BuildRevoke(targetJID, types.EmptyJID, types.MessageID(platformMsgID))

	resp, err := sess.client.SendMessage(ctx, targetJID, revokeMsg)
	if err != nil {
		return fmt.Errorf("failed to dispatch WhatsApp revocation for msg %s: %w", platformMsgID, err)
	}

	log.Printf("[WhatsAppManager] 🗑️ Dispatched message revocation to %s (%s) [RevokeMsgID: %s for OriginalMsgID: %s]\n",
		recipientPhone, targetJID.String(), resp.ID, platformMsgID)
	return nil
}

// EditMessage issues an official WhatsApp message edit for a previously sent message.
// The recipient's WhatsApp client will update the message content and display the "Edited" label.
func (m *WhatsAppManager) EditMessage(ctx context.Context, tenantID string, recipientPhone string, platformMsgID string, newText string) error {
	sess, err := m.ensureSession(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("WhatsApp client is not connected for tenant %s: %w", tenantID, err)
	}

	targetJID := m.resolveTargetJID(ctx, sess, recipientPhone)
	editMsg := sess.client.BuildEdit(targetJID, types.MessageID(platformMsgID), &waE2E.Message{
		Conversation: proto.String(newText),
	})

	resp, err := sess.client.SendMessage(ctx, targetJID, editMsg)
	if err != nil {
		return fmt.Errorf("failed to dispatch WhatsApp edit for msg %s: %w", platformMsgID, err)
	}

	log.Printf("[WhatsAppManager] ✏️ Dispatched message edit to %s (%s) [EditMsgID: %s for OriginalMsgID: %s]\n",
		recipientPhone, targetJID.String(), resp.ID, platformMsgID)
	return nil
}

// SyncContacts reads all saved contacts from the linked WhatsApp account, groups, and local store
func (m *WhatsAppManager) SyncContacts(ctx context.Context, tenantID string, contactRepo domain.ContactRepository) (int, error) {
	sess, err := m.ensureSession(ctx, tenantID)
	if err != nil {
		return 0, fmt.Errorf("WhatsApp is not connected for tenant %s: %w", tenantID, err)
	}

	syncedCount := 0

	// 1. Sync from local store
	if contactsMap, err := sess.client.Store.Contacts.GetAllContacts(ctx); err == nil && len(contactsMap) > 0 {
		for jid, info := range contactsMap {
			if jid.Server != types.DefaultUserServer || jid.User == "" {
				continue
			}
			name := info.FirstName
			if name == "" {
				name = info.FullName
			}
			if name == "" {
				name = info.PushName
			}
			if name == "" {
				name = info.BusinessName
			}
			m.saveContact(tenantID, jid.User, name, "whatsapp_sync")
			syncedCount++
		}
	}

	// 2. Sync from joined group participants (if any)
	if groups, err := sess.client.GetJoinedGroups(ctx); err == nil && len(groups) > 0 {
		for _, group := range groups {
			for _, participant := range group.Participants {
				if participant.JID.Server == types.DefaultUserServer && participant.JID.User != "" {
					m.saveContact(tenantID, participant.JID.User, "WhatsApp Contact", "whatsapp_group_sync")
				}
			}
		}
	}

	// 3. Query total count from DB for accurate feedback
	var totalInDB int
	_ = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts WHERE tenant_id = $1 AND channel = 'whatsapp'`, tenantID).Scan(&totalInDB)
	syncedCount = totalInDB

	log.Printf("[WhatsAppManager] 📥 Synced %d WhatsApp contacts for tenant %s\n", syncedCount, tenantID)
	return syncedCount, nil
}
