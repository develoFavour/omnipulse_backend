package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"

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
	db        *sql.DB
	container *sqlstore.Container
	clients   sync.Map // map[string]*tenantSession (key: tenantID)
	mu        sync.Mutex
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

func NewWhatsAppManager(db *sql.DB) (*WhatsAppManager, error) {
	// Set official companion OS properties for WhatsApp Web protocol
	store.SetOSInfo("Chrome (Windows)", [3]uint32{128, 0, 0})

	logger := waLog.Stdout("WA-Database", "WARN", true)
	container := sqlstore.NewWithDB(db, "postgres", logger)
	err := container.Upgrade(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to upgrade whatsmeow sqlstore: %w", err)
	}

	mgr := &WhatsAppManager{
		db:        db,
		container: container,
	}

	// Restore all existing connected devices on startup
	go mgr.restoreExistingSessions()

	return mgr, nil
}

func (m *WhatsAppManager) restoreExistingSessions() {
	devices, err := m.container.GetAllDevices(context.Background())
	if err != nil {
		log.Printf("[WhatsAppManager] Warning: failed to fetch devices: %v\n", err)
		return
	}

	for _, dev := range devices {
		if dev.ID == nil {
			continue
		}
		// Look up tenant for this device JID
		var tenantID, senderIdentity, status string
		err := m.db.QueryRow(
			`SELECT tenant_id, sender_identity, status FROM tenant_channels WHERE platform_name = 'whatsapp' AND encrypted_credentials->>'jid' = $1`,
			dev.ID.String(),
		).Scan(&tenantID, &senderIdentity, &status)

		if err != nil || tenantID == "" {
			continue
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
			log.Printf("[WhatsAppManager] Failed to auto-connect session for tenant %s: %v\n", tenantID, err)
		} else {
			log.Printf("[WhatsAppManager] Restored WhatsApp session for tenant %s (JID: %s)\n", tenantID, dev.ID.String())
			m.clients.Store(tenantID, sess)
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
		return "", "timeout", "", "", fmt.Errorf("timeout waiting for initial QR code")
	}

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
			senderJID := evt.Info.Sender
			if !evt.Info.IsFromMe && senderJID.Server == types.DefaultUserServer && senderJID.User != "" {
				m.saveContact(tenantID, senderJID.User, evt.Info.PushName, "whatsapp_inbound")
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

func (m *WhatsAppManager) saveContact(tenantID, rawPhone, name, source string) {
	phone := strings.TrimPrefix(strings.TrimSpace(rawPhone), "+")
	if len(phone) < 7 {
		return
	}

	// Skip saving the tenant's own phone number as an audience contact
	if val, ok := m.clients.Load(tenantID); ok {
		sess := val.(*tenantSession)
		sess.mu.RLock()
		selfPhone := strings.TrimPrefix(strings.TrimSpace(sess.phone), "+")
		sess.mu.RUnlock()
		if selfPhone != "" && selfPhone == phone {
			return
		}
	}

	routingValue := "+" + phone

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

	_, err := m.db.Exec(`
		INSERT INTO contacts (tenant_id, first_name, last_name, channel, routing_value, source, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'whatsapp', $4, $5, 'active', NOW(), NOW())
		ON CONFLICT (tenant_id, channel, routing_value)
		DO UPDATE SET
			first_name = CASE WHEN contacts.first_name IN ('WhatsApp User', 'WhatsApp Contact') AND EXCLUDED.first_name NOT IN ('WhatsApp User', 'WhatsApp Contact') THEN EXCLUDED.first_name ELSE contacts.first_name END,
			last_name = CASE WHEN contacts.last_name = '' AND EXCLUDED.last_name != '' THEN EXCLUDED.last_name ELSE contacts.last_name END,
			updated_at = NOW()
	`, tenantID, fn, ln, routingValue, source)
	if err != nil {
		log.Printf("[WhatsAppManager] Warning: failed to save contact %s (%s): %v\n", name, routingValue, err)
	}
}

func (m *WhatsAppManager) GetStatus(ctx context.Context, tenantID string) (status string, phone string, name string, err error) {
	if val, ok := m.clients.Load(tenantID); ok {
		sess := val.(*tenantSession)
		sess.mu.RLock()
		defer sess.mu.RUnlock()
		if sess.client.IsConnected() && sess.client.IsLoggedIn() {
			return "connected", sess.phone, sess.name, nil
		}
		return sess.status, sess.phone, sess.name, nil
	}

	// Check DB fallback
	var senderIdentity, chStatus string
	var credsJSON []byte
	err = m.db.QueryRow(
		`SELECT sender_identity, encrypted_credentials, status FROM tenant_channels WHERE tenant_id = $1 AND platform_name = 'whatsapp'`,
		tenantID,
	).Scan(&senderIdentity, &credsJSON, &chStatus)

	if err != nil {
		return "disconnected", "", "", nil
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

	_, err := m.db.Exec(`
		UPDATE tenant_channels SET status = 'inactive', updated_at = NOW()
		WHERE tenant_id = $1 AND platform_name = 'whatsapp'
	`, tenantID)
	return err
}

func (m *WhatsAppManager) SendMessage(ctx context.Context, tenantID string, recipientPhone string, text string) error {
	val, ok := m.clients.Load(tenantID)
	if !ok {
		return fmt.Errorf("no active WhatsApp session for tenant %s", tenantID)
	}
	sess := val.(*tenantSession)
	if !sess.client.IsConnected() || !sess.client.IsLoggedIn() {
		return fmt.Errorf("WhatsApp client is not connected for tenant %s", tenantID)
	}

	recipientJID := types.NewJID(recipientPhone, types.DefaultUserServer)
	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	_, err := sess.client.SendMessage(ctx, recipientJID, msg)
	return err
}

// SyncContacts reads all saved contacts from the linked WhatsApp account, groups, and local store
func (m *WhatsAppManager) SyncContacts(ctx context.Context, tenantID string, contactRepo domain.ContactRepository) (int, error) {
	val, ok := m.clients.Load(tenantID)
	if !ok {
		return 0, fmt.Errorf("no active WhatsApp session for tenant %s. Please connect WhatsApp first", tenantID)
	}
	sess := val.(*tenantSession)
	if !sess.client.IsConnected() || !sess.client.IsLoggedIn() {
		return 0, fmt.Errorf("WhatsApp is not connected for tenant %s", tenantID)
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
