package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"

	"github.com/lib/pq"
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
	aiEngine      *AIEngineService
	aiAgentRepo   domain.AIAgentRepository
	aiDispatcher  AIReplyDispatcher
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

func (m *WhatsAppManager) updateChannelHealth(ctx context.Context, tenantID, healthStatus, lastError string, successful bool) {
	if successful {
		_, err := m.db.ExecContext(ctx, `
			UPDATE tenant_channels
			SET health_status = $2,
				last_heartbeat_at = NOW(),
				last_successful_at = NOW(),
				last_error = NULL,
				last_error_at = NULL,
				updated_at = NOW()
			WHERE tenant_id = $1 AND platform_name = 'whatsapp'`, tenantID, healthStatus)
		if err != nil {
			log.Printf("[WhatsAppManager] Failed to persist healthy state for tenant %s: %v\n", tenantID, err)
		}
		return
	}

	_, err := m.db.ExecContext(ctx, `
		UPDATE tenant_channels
		SET health_status = $2,
			last_heartbeat_at = NOW(),
			last_error = NULLIF($3, ''),
			last_error_at = CASE WHEN NULLIF($3, '') IS NULL THEN NULL ELSE NOW() END,
			updated_at = NOW()
		WHERE tenant_id = $1 AND platform_name = 'whatsapp'`, tenantID, healthStatus, lastError)
	if err != nil {
		log.Printf("[WhatsAppManager] Failed to persist channel health for tenant %s: %v\n", tenantID, err)
	}
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

func (m *WhatsAppManager) SetAIEngine(aiEngine *AIEngineService) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aiEngine = aiEngine
}

func (m *WhatsAppManager) SetAIAgentRepo(repo domain.AIAgentRepository) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aiAgentRepo = repo
}

func (m *WhatsAppManager) SetAIReplyDispatcher(dispatcher AIReplyDispatcher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aiDispatcher = dispatcher
}

func (m *WhatsAppManager) ProcessAIReplyJob(ctx context.Context, job AIReplyJob) error {
	if m.inboxRepo == nil {
		return fmt.Errorf("inbox repository is unavailable")
	}
	conv, err := m.inboxRepo.GetConversation(ctx, job.TenantID, job.ConversationID)
	if err != nil {
		return err
	}
	return m.handleAIAutoResponse(job.TenantID, conv, job.Recipient, job.IncomingText)
}

func (m *WhatsAppManager) restoreExistingSessions() {
	devices, err := m.container.GetAllDevices(context.Background())
	if err != nil {
		log.Printf("[WhatsAppManager] Warning: failed to fetch devices: %v\n", err)
		return
	}

	rows, err := m.db.Query(
		`SELECT tenant_id, sender_identity, COALESCE(account_identity, ''), encrypted_credentials, status FROM tenant_channels WHERE platform_name = 'whatsapp' AND status = 'active'`,
	)
	if err != nil {
		log.Printf("[WhatsAppManager] Warning: failed to query tenant_channels: %v\n", err)
		return
	}
	defer rows.Close()

	type chRecord struct {
		tenantID        string
		senderIdentity  string
		accountIdentity string
		encCreds        []byte
		status          string
	}
	var channels []chRecord
	for rows.Next() {
		var ch chRecord
		if err := rows.Scan(&ch.tenantID, &ch.senderIdentity, &ch.accountIdentity, &ch.encCreds, &ch.status); err == nil {
			channels = append(channels, ch)
		}
	}

	for _, dev := range devices {
		if dev.ID == nil {
			continue
		}

		var matchedTenantID, matchedIdentity string
		phone := dev.ID.User
		phoneIdentity := domain.NormalizeWhatsAppPhoneIdentity(phone)

		// The database ownership key is authoritative. Do not use the first
		// matching row if a legacy/partially migrated database still contains
		// ambiguous mappings.
		var candidates []chRecord
		for _, ch := range channels {
			if ch.accountIdentity == phoneIdentity {
				candidates = append(candidates, ch)
			}
		}

		// Legacy fallback for rows created before account_identity existed.
		if len(candidates) == 0 {
			for _, ch := range channels {
				if strings.Contains(ch.senderIdentity, phone) {
					candidates = append(candidates, ch)
					continue
				}
				if len(ch.encCreds) > 0 {
					plain, decErr := utils.DecryptBytes(ch.encCreds, m.encryptionKey)
					if decErr == nil {
						var creds map[string]interface{}
						if json.Unmarshal(plain, &creds) == nil {
							if jidStr, _ := creds["jid"].(string); jidStr == dev.ID.String() {
								candidates = append(candidates, ch)
								continue
							}
							if credPhone, _ := creds["phone"].(string); credPhone == phone {
								candidates = append(candidates, ch)
							}
						}
					}
				}
			}
		}

		if len(candidates) == 1 {
			matchedTenantID = candidates[0].tenantID
			matchedIdentity = candidates[0].senderIdentity
		} else if len(candidates) > 1 {
			log.Printf("[WhatsAppManager] Refusing to restore ambiguous device JID %s: matched %d active workspace mappings\n", dev.ID.String(), len(candidates))
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
			m.updateChannelHealth(context.Background(), tenantID, "healthy", "", true)
			// On reconnect, asynchronously sync contacts from the local store.
			// After reconnection the whatsmeow store may have fresher LID→phone mappings.
			go func() {
				syncCtx, syncCancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer syncCancel()
				count, err := m.SyncContacts(syncCtx, tenantID, nil)
				if err != nil {
					log.Printf("[WhatsAppManager] ⚠️ Background contact sync failed for tenant %s: %v\n", tenantID, err)
				} else {
					log.Printf("[WhatsAppManager] ✅ Background contact sync complete for tenant %s: %d contacts\n", tenantID, count)
				}
			}()
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

			accountIdentity := domain.NormalizeWhatsAppPhoneIdentity(phone)
			_, err := m.db.Exec(`
				INSERT INTO tenant_channels (tenant_id, platform_name, sender_identity, account_identity, encrypted_credentials, status, updated_at)
				VALUES ($1, 'whatsapp', $2, $3, $4, 'active', NOW())
				ON CONFLICT (tenant_id, platform_name)
				DO UPDATE SET
					sender_identity = EXCLUDED.sender_identity,
					account_identity = EXCLUDED.account_identity,
					encrypted_credentials = EXCLUDED.encrypted_credentials,
					status = 'active',
					updated_at = NOW()
			`, tenantID, senderIdentity, accountIdentity, creds)
			if err != nil {
				var pqErr *pq.Error
				if errors.As(err, &pqErr) && pqErr.Constraint == "uq_active_whatsapp_account_identity" {
					log.Printf("[WhatsAppManager] Refusing WhatsApp pairing for tenant %s: this account is already connected to another workspace\n", tenantID)
					m.clients.Delete(tenantID)
					sess.client.Disconnect()
					return
				}
				log.Printf("[WhatsAppManager] Error persisting paired channel to DB: %v\n", err)
			} else {
				m.updateChannelHealth(context.Background(), tenantID, "healthy", "", true)
			}

		case *events.HistorySync:
			log.Printf("[WhatsAppManager] 🔄 Received HistorySync (%v) for tenant %s\n", evt.Data.GetSyncType(), tenantID)
			count := 0

			// 0. Store explicit Phone-to-LID mappings provided by WhatsApp
			if sess.client != nil {
				for _, mapping := range evt.Data.GetPhoneNumberToLidMappings() {
					pnStr := mapping.GetPnJID()
					lidStr := mapping.GetLidJID()
					if pnStr != "" && lidStr != "" {
						pnJID, pnErr := types.ParseJID(pnStr)
						lidJID, lidErr := types.ParseJID(lidStr)
						if pnErr == nil && lidErr == nil {
							sess.client.StoreLIDPNMapping(context.Background(), pnJID, lidJID)
							if sess.client.Store != nil && sess.client.Store.LIDs != nil {
								_ = sess.client.Store.LIDs.PutLIDMapping(context.Background(), lidJID, pnJID)
							}
						}
					}
				}
			}

			// 1. Process InlineContacts (direct companion contacts provided by WhatsApp)
			for _, ic := range evt.Data.GetInlineContacts() {
				pnStr := ic.GetPnJID()
				lidStr := ic.GetLidJID()
				var phoneUser string
				var lidUser string
				if pnStr != "" {
					if pnJID, err := types.ParseJID(pnStr); err == nil && pnJID.User != "" {
						phoneUser = pnJID.User
					}
				}
				if lidStr != "" {
					if lidJID, err := types.ParseJID(lidStr); err == nil && lidJID.User != "" {
						lidUser = lidJID.User
						if phoneUser != "" && sess.client != nil {
							if pnJID, err := types.ParseJID(pnStr); err == nil {
								sess.client.StoreLIDPNMapping(context.Background(), pnJID, lidJID)
								if sess.client.Store != nil && sess.client.Store.LIDs != nil {
									_ = sess.client.Store.LIDs.PutLIDMapping(context.Background(), lidJID, pnJID)
								}
							}
						}
					}
				}
				if phoneUser == "" && lidUser != "" {
					if sess.client != nil && sess.client.Store != nil {
						resCtx, resCancel := context.WithTimeout(context.Background(), 2*time.Second)
						lidJID := types.NewJID(lidUser, types.HiddenUserServer)
						if alt, altErr := sess.client.Store.GetAltJID(resCtx, lidJID); altErr == nil && alt.Server == types.DefaultUserServer && alt.User != "" {
							phoneUser = alt.User
						} else if sess.client.Store.LIDs != nil {
							if pn2, lidErr := sess.client.Store.LIDs.GetPNForLID(resCtx, lidJID); lidErr == nil && pn2.Server == types.DefaultUserServer && pn2.User != "" {
								phoneUser = pn2.User
							}
						}
						resCancel()
					}
					if phoneUser == "" {
						phoneUser = lidUser
					} else {
						m.resolveAndMigrateLID(tenantID, lidUser, phoneUser)
					}
				}
				if phoneUser == "" {
					continue
				}
				name := ic.GetFullName()
				if name == "" {
					name = ic.GetFirstName()
				}
				if name == "" {
					name = ic.GetUsername()
				}
				m.saveContact(tenantID, phoneUser, name, "whatsapp_sync")
				count++
			}

			// 2. Process Conversations — includes both phone and LID-based chats
			for _, conv := range evt.Data.GetConversations() {
				chatID := conv.GetID()
				if chatID == "" {
					continue
				}
				jid, err := types.ParseJID(chatID)
				if err != nil || jid.IsEmpty() || jid.Server == types.GroupServer || jid.Server == types.BroadcastServer {
					continue // skip groups and broadcasts
				}
				if jid.User == "" {
					continue
				}

				name := conv.GetName()
				if name == "" {
					name = conv.GetDisplayName()
				}
				if name == "" {
					name = conv.GetUsername()
				}

				phoneUser := jid.User
				pnJIDStr := conv.GetPnJID()
				if pnJIDStr != "" {
					if pnJID, pnErr := types.ParseJID(pnJIDStr); pnErr == nil && pnJID.User != "" {
						phoneUser = pnJID.User
						if jid.Server == types.HiddenUserServer && sess.client != nil {
							sess.client.StoreLIDPNMapping(context.Background(), pnJID, jid)
							if sess.client.Store != nil && sess.client.Store.LIDs != nil {
								_ = sess.client.Store.LIDs.PutLIDMapping(context.Background(), jid, pnJID)
							}
						}
					}
				}

				if jid.Server == types.HiddenUserServer && phoneUser == jid.User && sess.client != nil && sess.client.Store != nil {
					// Attempt to resolve LID → real phone number
					resCtx, resCancel := context.WithTimeout(context.Background(), 2*time.Second)
					if alt, altErr := sess.client.Store.GetAltJID(resCtx, jid); altErr == nil && alt.Server == types.DefaultUserServer && alt.User != "" {
						phoneUser = alt.User
						m.resolveAndMigrateLID(tenantID, jid.User, phoneUser)
						log.Printf("[WhatsAppManager] 🔀 HistorySync conv: resolved LID %s → phone %s\n", jid.String(), phoneUser)
					} else if sess.client.Store.LIDs != nil {
						if pn2, lidErr := sess.client.Store.LIDs.GetPNForLID(resCtx, jid); lidErr == nil && pn2.Server == types.DefaultUserServer && pn2.User != "" {
							phoneUser = pn2.User
							m.resolveAndMigrateLID(tenantID, jid.User, phoneUser)
							log.Printf("[WhatsAppManager] 🔀 HistorySync conv: resolved LID %s → phone %s via LIDStore\n", jid.String(), phoneUser)
						}
					}
					resCancel()
				}

				// Check messages inside conversation for sender pushname if name is still empty
				if name == "" {
					for _, hmsg := range conv.GetMessages() {
						if hmsg != nil && hmsg.GetMessage() != nil && hmsg.GetMessage().GetPushName() != "" {
							name = hmsg.GetMessage().GetPushName()
							break
						}
					}
				}

				m.saveContact(tenantID, phoneUser, name, "whatsapp_sync")
				count++
			}

			// 3. Process Pushnames — includes LID pushnames
			for _, pn := range evt.Data.GetPushnames() {
				jidStr := pn.GetID()
				if jidStr == "" {
					continue
				}
				jid, err := types.ParseJID(jidStr)
				if err != nil || jid.IsEmpty() || jid.Server == types.GroupServer || jid.Server == types.BroadcastServer {
					continue
				}
				if jid.User == "" {
					continue
				}
				phoneUser := jid.User
				if jid.Server == types.HiddenUserServer && sess.client != nil && sess.client.Store != nil {
					resCtx, resCancel := context.WithTimeout(context.Background(), 2*time.Second)
					if alt, altErr := sess.client.Store.GetAltJID(resCtx, jid); altErr == nil && alt.Server == types.DefaultUserServer && alt.User != "" {
						phoneUser = alt.User
						m.resolveAndMigrateLID(tenantID, jid.User, phoneUser)
						log.Printf("[WhatsAppManager] 🔀 HistorySync pushname: resolved LID %s → phone %s\n", jid.String(), phoneUser)
					} else if sess.client.Store.LIDs != nil {
						if pn2, lidErr := sess.client.Store.LIDs.GetPNForLID(resCtx, jid); lidErr == nil && pn2.Server == types.DefaultUserServer && pn2.User != "" {
							phoneUser = pn2.User
							m.resolveAndMigrateLID(tenantID, jid.User, phoneUser)
							log.Printf("[WhatsAppManager] 🔀 HistorySync pushname: resolved LID %s → phone %s via LIDStore\n", jid.String(), phoneUser)
						}
					}
					resCancel()
				}
				m.saveContact(tenantID, phoneUser, pn.GetPushname(), "whatsapp_sync")
				count++
			}
			log.Printf("[WhatsAppManager] 📥 Auto-imported %d contacts from HistorySync for tenant %s\n", count, tenantID)

		case *events.PushName:
			phoneUser := evt.JID.User
			if evt.JID.Server == types.HiddenUserServer && sess.client != nil && sess.client.Store != nil {
				resCtx, resCancel := context.WithTimeout(context.Background(), 2*time.Second)
				if alt, altErr := sess.client.Store.GetAltJID(resCtx, evt.JID); altErr == nil && alt.Server == types.DefaultUserServer && alt.User != "" {
					phoneUser = alt.User
					m.resolveAndMigrateLID(tenantID, evt.JID.User, phoneUser)
				} else if sess.client.Store.LIDs != nil {
					if pn2, lidErr := sess.client.Store.LIDs.GetPNForLID(resCtx, evt.JID); lidErr == nil && pn2.Server == types.DefaultUserServer && pn2.User != "" {
						phoneUser = pn2.User
						m.resolveAndMigrateLID(tenantID, evt.JID.User, phoneUser)
					}
				}
				resCancel()
			}
			if phoneUser != "" && evt.NewPushName != "" && evt.JID.Server != types.GroupServer && evt.JID.Server != types.BroadcastServer {
				m.saveContact(tenantID, phoneUser, evt.NewPushName, "whatsapp_sync")
			}

		case *events.Contact:
			phoneUser := evt.JID.User
			if evt.JID.Server == types.HiddenUserServer && sess.client != nil && sess.client.Store != nil {
				resCtx, resCancel := context.WithTimeout(context.Background(), 2*time.Second)
				if alt, altErr := sess.client.Store.GetAltJID(resCtx, evt.JID); altErr == nil && alt.Server == types.DefaultUserServer && alt.User != "" {
					phoneUser = alt.User
					m.resolveAndMigrateLID(tenantID, evt.JID.User, phoneUser)
				} else if sess.client.Store.LIDs != nil {
					if pn2, lidErr := sess.client.Store.LIDs.GetPNForLID(resCtx, evt.JID); lidErr == nil && pn2.Server == types.DefaultUserServer && pn2.User != "" {
						phoneUser = pn2.User
						m.resolveAndMigrateLID(tenantID, evt.JID.User, phoneUser)
					}
				}
				resCancel()
			}
			if phoneUser != "" && evt.Action != nil && evt.JID.Server != types.GroupServer && evt.JID.Server != types.BroadcastServer {
				name := evt.Action.GetFullName()
				if name == "" {
					name = evt.Action.GetFirstName()
				}
				m.saveContact(tenantID, phoneUser, name, "whatsapp_sync")
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
				if evt.Info.Chat.User != "" {
					contactPhone = m.resolveWhatsAppContactPhone(context.Background(), sess, evt.Info.Chat, tenantID)
					direction = domain.MessageDirectionOutbound
					senderType = domain.MessageSenderTypeAgent
				}
			} else {
				// Inbound message received from contact (1-on-1 chats)
				if !evt.Info.IsGroup {
					pushName = evt.Info.PushName
					// Use the same canonical identity resolver for inbound and outbound
					// events. WhatsApp may represent the same person as a phone JID in
					// one event and a LID JID in another.
					contactPhone = m.resolveWhatsAppContactPhone(context.Background(), sess, evt.Info.Chat, tenantID)
					if contactPhone == "" {
						contactPhone = m.resolveWhatsAppContactPhone(context.Background(), sess, evt.Info.Sender, tenantID)
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

				var mediaURL *string
				var mediaType *string

				// Extract and download media attachments (voicenotes, audio, photos)
				if audioMsg := evt.Message.GetAudioMessage(); audioMsg != nil && sess.client != nil {
					dlCtx, dlCancel := context.WithTimeout(context.Background(), 15*time.Second)
					audioBytes, dlErr := sess.client.Download(dlCtx, audioMsg)
					dlCancel()
					if dlErr == nil && len(audioBytes) > 0 {
						mime := audioMsg.GetMimetype()
						if mime == "" {
							mime = "audio/ogg; codecs=opus"
						}
						dataURI := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(audioBytes))
						mediaURL = &dataURI
						mType := "audio"
						mediaType = &mType
						log.Printf("[WhatsAppManager] 🎙️ Downloaded voicenote (%d bytes, %s) for tenant %s\n", len(audioBytes), mime, tenantID)
					} else {
						log.Printf("[WhatsAppManager] ⚠️ Voicenote download failed: %v\n", dlErr)
					}
				} else if imgMsg := evt.Message.GetImageMessage(); imgMsg != nil && sess.client != nil {
					dlCtx, dlCancel := context.WithTimeout(context.Background(), 15*time.Second)
					imgBytes, dlErr := sess.client.Download(dlCtx, imgMsg)
					dlCancel()
					if dlErr == nil && len(imgBytes) > 0 {
						mime := imgMsg.GetMimetype()
						if mime == "" {
							mime = "image/jpeg"
						}
						dataURI := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(imgBytes))
						mediaURL = &dataURI
						mType := "image"
						mediaType = &mType
						log.Printf("[WhatsAppManager] 🖼️ Downloaded image attachment (%d bytes) for tenant %s\n", len(imgBytes), tenantID)
					}
				}

				if (text != "" || mediaURL != nil) && contactID != "" && m.inboxRepo != nil {
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
							MediaURL:          mediaURL,
							MediaType:         mediaType,
							PlatformMessageID: &msgID,
							Status:            initialStatus,
						}
						messageCreated := false
						if err := m.inboxRepo.CreateMessage(ctx, newMsg); err != nil {
							if errors.Is(err, domain.ErrMessageAlreadyExists) {
								log.Printf("[WhatsAppManager/Inbox] Ignored duplicate %s platform message for tenant %s (conversation=%s, platform_id=%s)", direction, tenantID, conv.ID, msgID)
							} else {
								log.Printf("[WhatsAppManager/Inbox] Failed to persist %s message for tenant %s (conversation=%s, platform_id=%s): %v", direction, tenantID, conv.ID, msgID, err)
							}
						} else {
							messageCreated = true
							log.Printf("[WhatsAppManager/Inbox] Persisted %s message id=%s for tenant %s conversation=%s", direction, newMsg.ID, tenantID, conv.ID)
							if m.inboxHub != nil {
								m.inboxHub.BroadcastMessage(tenantID, conv, newMsg)
								log.Printf("[WhatsAppManager/Inbox] Broadcast %s message id=%s for tenant %s conversation=%s", direction, newMsg.ID, tenantID, conv.ID)
							} else {
								log.Printf("[WhatsAppManager/Inbox] Cannot broadcast message id=%s: inbox hub is unavailable", newMsg.ID)
							}
						}
						// Autonomous AI Business Assistant: handle inbound customer inquiries asynchronously
						if messageCreated && direction == domain.MessageDirectionInbound && text != "" {
							job := AIReplyJob{
								TenantID: tenantID, ConversationID: conv.ID, Channel: "whatsapp",
								Recipient: contactPhone, IncomingText: text, PlatformID: msgID,
							}
							if m.aiDispatcher != nil {
								if err := m.aiDispatcher.EnqueueAIReply(ctx, job); err != nil {
									log.Printf("[WhatsAppManager/AI] Queue unavailable, falling back to in-process reply: %v\n", err)
									go m.handleAIAutoResponse(tenantID, conv, contactPhone, text)
								}
							} else {
								go m.handleAIAutoResponse(tenantID, conv, contactPhone, text)
							}
						}
						if messageCreated {
							log.Printf("[WhatsAppManager] 💬 Synced %s message for tenant %s with %s (conv %s, media=%v): %q", direction, tenantID, contactPhone, conv.ID, mediaType != nil, text)
						}
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
			m.updateChannelHealth(context.Background(), tenantID, "reconnect_required", "WhatsApp was disconnected. Reconnect this channel to resume messaging.", false)

			// Keep the channel record so the workspace can see that recovery is
			// required. Suspending it releases the partial unique ownership lock,
			// so the account can still be intentionally connected elsewhere.
			_, _ = m.db.Exec(`
				UPDATE tenant_channels
				SET status = 'suspended', health_status = 'reconnect_required',
					last_error = 'WhatsApp was disconnected. Reconnect this channel to resume messaging.',
					last_error_at = NOW(), updated_at = NOW()
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

// resolveWhatsAppContactPhone returns the canonical phone identifier for a
// WhatsApp chat JID. LIDs are device-scoped identifiers and must not be used
// as the contact key when WhatsApp can provide the corresponding phone JID.
func (m *WhatsAppManager) resolveWhatsAppContactPhone(ctx context.Context, sess *tenantSession, jid types.JID, tenantID string) string {
	if jid.User == "" {
		return ""
	}
	if jid.Server == types.DefaultUserServer {
		return jid.User
	}
	if jid.Server != types.HiddenUserServer || sess == nil || sess.client == nil || sess.client.Store == nil {
		return jid.User
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if alt, err := sess.client.Store.GetAltJID(lookupCtx, jid); err == nil && alt.Server == types.DefaultUserServer && alt.User != "" {
		m.resolveAndMigrateLID(tenantID, jid.User, alt.User)
		log.Printf("[WhatsAppManager] 🔀 Resolved LID %s to phone %s via AltJID\n", jid.String(), alt.User)
		return alt.User
	}
	if sess.client.Store.LIDs != nil {
		if phoneJID, err := sess.client.Store.LIDs.GetPNForLID(lookupCtx, jid); err == nil && phoneJID.Server == types.DefaultUserServer && phoneJID.User != "" {
			m.resolveAndMigrateLID(tenantID, jid.User, phoneJID.User)
			log.Printf("[WhatsAppManager] 🔀 Resolved LID %s to phone %s via LIDStore\n", jid.String(), phoneJID.User)
			return phoneJID.User
		}
	}

	// Keep the raw LID only as a last resort. The next mapped event will migrate
	// this temporary contact to the canonical phone identifier.
	return jid.User
}

// resolveAndMigrateLID updates any contact previously stored under a raw LID routing value
// (+<lidUser>) to the real phone number (+<phoneUser>) once the phone mapping is known,
// merging or deleting redundant placeholders.
func (m *WhatsAppManager) resolveAndMigrateLID(tenantID, lidUser, phoneUser string) {
	if lidUser == "" || phoneUser == "" || lidUser == phoneUser {
		return
	}
	lidRouting := "+" + strings.TrimPrefix(strings.TrimSpace(lidUser), "+")
	phoneRouting := "+" + strings.TrimPrefix(strings.TrimSpace(phoneUser), "+")

	execCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Update existing LID contact record to real phone number if no conflict exists
	_, _ = m.db.ExecContext(execCtx, `
		UPDATE contacts 
		SET routing_value = $1, updated_at = NOW()
		WHERE tenant_id = $2 AND channel = 'whatsapp' AND routing_value = $3
		AND NOT EXISTS (
			SELECT 1 FROM contacts WHERE tenant_id = $2 AND channel = 'whatsapp' AND routing_value = $1
		)
	`, phoneRouting, tenantID, lidRouting)

	// If a record already exists with phoneRouting, delete the temporary LID record to avoid duplicate
	_, _ = m.db.ExecContext(execCtx, `
		DELETE FROM contacts 
		WHERE tenant_id = $1 AND channel = 'whatsapp' AND routing_value = $2
		AND EXISTS (
			SELECT 1 FROM contacts WHERE tenant_id = $1 AND channel = 'whatsapp' AND routing_value = $3
		)
	`, tenantID, lidRouting, phoneRouting)
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
		DELETE FROM tenant_channels
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

// SyncContacts reads all saved contacts from the linked WhatsApp account, groups, and local store.
// It handles both standard phone-number JIDs (s.whatsapp.net) and LID-based JIDs (lid server)
// by attempting to resolve LIDs to their real phone numbers before saving.
func (m *WhatsAppManager) SyncContacts(ctx context.Context, tenantID string, contactRepo domain.ContactRepository) (int, error) {
	sess, err := m.ensureSession(ctx, tenantID)
	if err != nil {
		return 0, fmt.Errorf("WhatsApp is not connected for tenant %s: %w", tenantID, err)
	}

	syncedCount := 0
	lidResolutionFailed := 0

	// 1. Sync from local store — process both DefaultUserServer and HiddenUserServer (LID) contacts
	if contactsMap, err := sess.client.Store.Contacts.GetAllContacts(ctx); err == nil && len(contactsMap) > 0 {
		log.Printf("[WhatsAppManager] 🔍 Found %d contacts in local WA store for tenant %s\n", len(contactsMap), tenantID)
		for jid, info := range contactsMap {
			if jid.Server == types.GroupServer || jid.Server == types.BroadcastServer || jid.User == "" {
				continue // skip groups and broadcast lists
			}
			name := info.FullName
			if name == "" {
				name = info.FirstName
			}
			if name == "" {
				name = info.PushName
			}
			if name == "" {
				name = info.BusinessName
			}

			phoneUser := jid.User
			if jid.Server == types.HiddenUserServer {
				// LID contact: attempt to resolve to real phone number
				resolved := false
				if sess.client.Store != nil {
					resCtx, resCancel := context.WithTimeout(context.Background(), 2*time.Second)
					if alt, altErr := sess.client.Store.GetAltJID(resCtx, jid); altErr == nil && alt.Server == types.DefaultUserServer && alt.User != "" {
						phoneUser = alt.User
						resolved = true
						m.resolveAndMigrateLID(tenantID, jid.User, phoneUser)
						log.Printf("[WhatsAppManager] 🔀 SyncContacts: resolved LID %s → phone %s (name: %q)\n", jid.String(), phoneUser, name)
					} else if sess.client.Store.LIDs != nil {
						if pn2, lidErr := sess.client.Store.LIDs.GetPNForLID(resCtx, jid); lidErr == nil && pn2.Server == types.DefaultUserServer && pn2.User != "" {
							phoneUser = pn2.User
							resolved = true
							m.resolveAndMigrateLID(tenantID, jid.User, phoneUser)
							log.Printf("[WhatsAppManager] 🔀 SyncContacts: resolved LID %s → phone %s via LIDStore (name: %q)\n", jid.String(), phoneUser, name)
						}
					}
					resCancel()
				}
				if !resolved {
					// Save with LID identifier directly so the contact is not lost.
					// The routing_value will start with the LID numeric string and will be
					// updated to a real phone number when the mapping becomes available
					// (e.g. next time they send a message and AltJID resolves).
					lidResolutionFailed++
					log.Printf("[WhatsAppManager] ℹ️ SyncContacts: could not resolve LID %s, saving as-is (name: %q)\n", jid.String(), name)
				}
			}
			m.saveContact(tenantID, phoneUser, name, "whatsapp_sync")
			syncedCount++
		}
		log.Printf("[WhatsAppManager] 📋 SyncContacts store pass: %d processed, %d LIDs unresolved for tenant %s\n",
			syncedCount, lidResolutionFailed, tenantID)
	} else if err != nil {
		log.Printf("[WhatsAppManager] ⚠️ SyncContacts: failed to get contacts from local store: %v\n", err)
	}

	// 2. Sync from joined group participants (if any)
	if groups, groupErr := sess.client.GetJoinedGroups(ctx); groupErr == nil && len(groups) > 0 {
		groupCount := 0
		for _, group := range groups {
			for _, participant := range group.Participants {
				if participant.JID.IsEmpty() || participant.JID.User == "" {
					continue
				}
				phoneUser := participant.JID.User
				if participant.JID.Server == types.HiddenUserServer && sess.client != nil && sess.client.Store != nil {
					resCtx, resCancel := context.WithTimeout(context.Background(), 2*time.Second)
					if alt, altErr := sess.client.Store.GetAltJID(resCtx, participant.JID); altErr == nil && alt.Server == types.DefaultUserServer && alt.User != "" {
						phoneUser = alt.User
						m.resolveAndMigrateLID(tenantID, participant.JID.User, phoneUser)
					} else if sess.client.Store.LIDs != nil {
						if pn2, lidErr := sess.client.Store.LIDs.GetPNForLID(resCtx, participant.JID); lidErr == nil && pn2.Server == types.DefaultUserServer && pn2.User != "" {
							phoneUser = pn2.User
							m.resolveAndMigrateLID(tenantID, participant.JID.User, phoneUser)
						}
					}
					resCancel()
				}
				m.saveContact(tenantID, phoneUser, "WhatsApp Contact", "whatsapp_group_sync")
				groupCount++
			}
		}
		log.Printf("[WhatsAppManager] 👥 SyncContacts: imported %d group participants for tenant %s\n", groupCount, tenantID)
	}

	// 3. Query total count from DB for accurate feedback
	var totalInDB int
	_ = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts WHERE tenant_id = $1 AND channel = 'whatsapp'`, tenantID).Scan(&totalInDB)

	log.Printf("[WhatsAppManager] 📥 SyncContacts complete: %d WhatsApp contacts in DB for tenant %s\n", totalInDB, tenantID)
	return totalInDB, nil
}

func (m *WhatsAppManager) handleAIAutoResponse(tenantID string, conv *domain.Conversation, contactPhone, incomingText string) error {
	if m.aiEngine == nil {
		log.Printf("[WhatsAppManager/AI] Skipping reply for tenant %s: AI engine is unavailable\n", tenantID)
		return nil
	}
	if m.aiAgentRepo == nil {
		log.Printf("[WhatsAppManager/AI] Skipping reply for tenant %s: AI configuration repository is unavailable\n", tenantID)
		return nil
	}
	if !m.aiEngine.HasActiveProvider() {
		log.Printf("[WhatsAppManager/AI] Skipping reply for tenant %s: no AI provider key is configured\n", tenantID)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Check AI master toggle and configuration for tenant
	cfg, err := m.aiAgentRepo.GetConfig(ctx, tenantID)
	if err != nil {
		log.Printf("[WhatsAppManager/AI] Failed to load AI configuration for tenant %s: %v\n", tenantID, err)
		return err
	}
	if cfg == nil || !cfg.IsEnabled {
		log.Printf("[WhatsAppManager/AI] Skipping reply for tenant %s: workspace AI assistance is disabled\n", tenantID)
		return nil
	}

	// 2. Check conversation-level AI state (e.g. human agent paused it)
	aiState, pausedUntil, needsHuman, err := m.aiAgentRepo.GetConversationAIState(ctx, tenantID, conv.ID)
	if err == nil {
		if aiState == "paused_by_human" {
			if pausedUntil != nil && time.Now().Before(*pausedUntil) {
				log.Printf("[WhatsAppManager/AI] ⏸️ Skipping AI reply for conv %s: paused by agent until %v\n", conv.ID, pausedUntil)
				return nil
			}
			// Resume automatically after pause period ends
			_ = m.aiAgentRepo.SetConversationAIState(ctx, tenantID, conv.ID, "active", nil, false)
		} else if aiState == "disabled" {
			log.Printf("[WhatsAppManager/AI] Skipping reply for conversation %s: AI is disabled for this conversation\n", conv.ID)
			return nil
		} else if aiState == "escalated" || needsHuman {
			log.Printf("[WhatsAppManager/AI] Skipping reply for conversation %s: conversation is awaiting human attention\n", conv.ID)
			return nil
		}
	}

	// 3. Verify operational mode (always_on vs after_hours)
	if !m.aiEngine.ShouldRespond(cfg) {
		log.Printf("[WhatsAppManager/AI] Skipping reply for tenant %s: operational mode %q does not allow an automatic response right now\n", tenantID, cfg.OperationalMode)
		return nil
	}

	// 4. Fetch only relevant business knowledge for this message when the
	// repository supports optimized retrieval. Keep the full-list fallback for
	// compatibility with alternate repositories and tests.
	var knowledge []domain.AIKnowledgeItem
	if retriever, ok := m.aiAgentRepo.(domain.AIKnowledgeRetriever); ok {
		knowledge, err = retriever.SearchKnowledgeItems(ctx, tenantID, incomingText, 8)
		log.Printf("[WhatsAppManager/AI] Knowledge retrieval: mode=full_text selected=%d", len(knowledge))
	} else {
		knowledge, err = m.aiAgentRepo.ListKnowledgeItems(ctx, tenantID)
		log.Printf("[WhatsAppManager/AI] Knowledge retrieval: mode=fallback_full_list selected=%d", len(knowledge))
	}
	if err != nil {
		return err
	}
	if len(knowledge) == 0 {
		if fallbackKnowledge, fallbackErr := m.aiAgentRepo.ListKnowledgeItems(ctx, tenantID); fallbackErr == nil && len(fallbackKnowledge) > 0 {
			knowledge = fallbackKnowledge
			log.Printf("[WhatsAppManager/AI] Knowledge retrieval: search returned 0, fell back to active list (selected=%d)", len(knowledge))
		}
	}

	// 5. Fetch recent conversation history (last 6 messages) for context
	recentMsgs, err := m.inboxRepo.ListMessages(ctx, tenantID, conv.ID, 6, 0)
	if err != nil {
		return err
	}
	// Reverse to chronological order
	for i, j := 0, len(recentMsgs)-1; i < j; i, j = i+1, j-1 {
		recentMsgs[i], recentMsgs[j] = recentMsgs[j], recentMsgs[i]
	}

	// 6. Generate reply with dual-engine fallback (Gemini -> Groq)
	replyText, needsHuman, leadInfo, err := m.aiEngine.GenerateReply(ctx, cfg, knowledge, recentMsgs, incomingText)
	if err != nil {
		log.Printf("[WhatsAppManager/AI] ⚠️ Failed to generate AI reply for tenant %s: %v\n", tenantID, err)
		return err
	}

	// Auto-enrich audience contact details if lead info detected
	if leadInfo != nil && leadInfo.Name != "" {
		phoneRouting := "+" + strings.TrimPrefix(strings.TrimSpace(contactPhone), "+")
		_, _ = m.db.ExecContext(ctx, `
			UPDATE contacts
			SET first_name = $1, updated_at = NOW()
			WHERE tenant_id = $2 AND channel = 'whatsapp' AND (routing_value = $3 OR routing_value = $4)
		`, leadInfo.Name, tenantID, phoneRouting, contactPhone)
		log.Printf("[WhatsAppManager/AI] 🎯 Auto-captured lead name %q for %s\n", leadInfo.Name, contactPhone)
	}

	if strings.TrimSpace(replyText) == "" {
		return nil
	}

	// 7. Dispatch reply to customer via WhatsApp
	msgID, err := m.SendMessage(ctx, tenantID, contactPhone, replyText, nil)
	if err != nil {
		log.Printf("[WhatsAppManager/AI] ❌ Failed to dispatch WhatsApp message to %s: %v\n", contactPhone, err)
		return err
	}

	// 8. Persist the reply with sender_type = "bot"
	botMsg := &domain.Message{
		TenantID:          tenantID,
		ConversationID:    conv.ID,
		Direction:         domain.MessageDirectionOutbound,
		SenderType:        domain.MessageSenderTypeBot,
		Body:              replyText,
		PlatformMessageID: &msgID,
		Status:            "delivered",
		CreatedAt:         time.Now().UTC(),
	}
	if err := m.inboxRepo.CreateMessage(ctx, botMsg); err != nil {
		return err
	}

	// 9. If customer asked for human assistance, flag the conversation
	if needsHuman {
		_ = m.aiAgentRepo.SetConversationAIState(ctx, tenantID, conv.ID, "escalated", nil, true)
		if m.inboxHub != nil {
			m.inboxHub.BroadcastConversationAIState(tenantID, conv.ID, "escalated", nil, true)
		}
	}

	// 10. Broadcast the bot message to the Team Inbox live via WebSocket!
	if m.inboxHub != nil {
		m.inboxHub.BroadcastMessage(tenantID, conv, botMsg)
	}

	log.Printf("[WhatsAppManager/AI] 🤖 Successfully sent AI bot reply to %s for tenant %s (conv: %s, needsHuman: %v)\n",
		contactPhone, tenantID, conv.ID, needsHuman)
	return nil
}
