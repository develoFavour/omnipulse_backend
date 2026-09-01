package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

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

	qrChan, err := client.GetQRChannel(ctx)
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
