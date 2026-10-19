package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/service"
	"omnipulse/apps/api-gateway/internal/utils"
)

type WebhookHandler struct {
	contactUC             domain.ContactUseCase
	channelRepo           domain.ChannelRepository
	destinationRepo       domain.TelegramDestinationRepository
	inboxRepo             domain.InboxRepository
	metaAppSecret         string
	telegramWebhookSecret string
	broadcaster           domain.InboxBroadcaster
	aiEngine              *service.AIEngineService
	aiAgentRepo           domain.AIAgentRepository
	aiDispatcher          service.AIReplyDispatcher
}

func (h *WebhookHandler) SetBroadcaster(b domain.InboxBroadcaster) {
	h.broadcaster = b
}

func (h *WebhookHandler) SetAIEngine(aiEngine *service.AIEngineService) {
	h.aiEngine = aiEngine
}

func (h *WebhookHandler) SetAIAgentRepo(repo domain.AIAgentRepository) {
	h.aiAgentRepo = repo
}

func (h *WebhookHandler) SetAIReplyDispatcher(dispatcher service.AIReplyDispatcher) {
	h.aiDispatcher = dispatcher
}

func (h *WebhookHandler) ProcessAIReplyJob(ctx context.Context, job service.AIReplyJob) error {
	if h.inboxRepo == nil {
		return fmt.Errorf("inbox repository is unavailable")
	}
	conv, err := h.inboxRepo.GetConversation(ctx, job.TenantID, job.ConversationID)
	if err != nil {
		return err
	}
	h.handleTelegramAIResponse(job.TenantID, conv, job.Recipient, job.IncomingText)
	return nil
}

func NewWebhookHandler(
	contactUC domain.ContactUseCase,
	channelRepo domain.ChannelRepository,
	destinationRepo domain.TelegramDestinationRepository,
	inboxRepo domain.InboxRepository,
	metaAppSecret string,
	telegramWebhookSecret string,
) *WebhookHandler {
	return &WebhookHandler{
		contactUC:             contactUC,
		channelRepo:           channelRepo,
		destinationRepo:       destinationRepo,
		inboxRepo:             inboxRepo,
		metaAppSecret:         metaAppSecret,
		telegramWebhookSecret: telegramWebhookSecret,
	}
}

type TelegramWebhookPayload struct {
	UpdateID int `json:"update_id"`
	Message  struct {
		MessageID int `json:"message_id"`
		From      struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Chat TelegramChat `json:"chat"`
		Text string       `json:"text"`
	} `json:"message"`
	MyChatMember struct {
		Chat TelegramChat `json:"chat"`
	} `json:"my_chat_member"`
	ChannelPost struct {
		Chat TelegramChat `json:"chat"`
		Text string       `json:"text"`
	} `json:"channel_post"`
}

type TelegramChat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type WhatsAppWebhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata         struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
				Contacts []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WaID string `json:"wa_id"`
				} `json:"contacts"`
				Messages []struct {
					From string `json:"from"`
					ID   string `json:"id"`
					Type string `json:"type"`
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// verifyMetaSignature checks the X-Hub-Signature-256 header against the request body
// using the Meta App Secret. Returns true if the signature is valid.
func verifyMetaSignature(body []byte, signatureHeader, appSecret string) bool {
	if appSecret == "" {
		// If no secret configured, allow through (dev/test mode) but log a warning
		log.Println("[Webhook/WhatsApp] ⚠️  META_APP_SECRET not set — skipping signature verification (insecure)")
		return true
	}
	if len(signatureHeader) < 8 || signatureHeader[:7] != "sha256=" {
		return false
	}
	expectedSig := signatureHeader[7:]
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	computed := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(computed), []byte(expectedSig))
}

// HandleTelegram handles POST /api/v1/webhooks/telegram/{tenant_id}
func (h *WebhookHandler) HandleTelegram(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing tenant_id in webhook URL")
		return
	}

	// Telegram secret token verification.
	// When setWebhook is called with secret_token, Telegram sends it in X-Telegram-Bot-Api-Secret-Token.
	if h.telegramWebhookSecret != "" {
		incomingSecret := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if incomingSecret != h.telegramWebhookSecret {
			log.Printf("[Webhook/Telegram] ⛔ Rejected request for tenant %s — invalid or missing X-Telegram-Bot-Api-Secret-Token\n", tenantID)
			utils.WriteError(w, http.StatusForbidden, "Invalid webhook secret")
			return
		}
	}

	var payload TelegramWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid telegram webhook payload")
		return
	}

	if chat := primaryDestinationChat(payload); chat.ID != 0 && isTelegramDestinationType(chat.Type) {
		h.syncTelegramDestination(r, tenantID, chat)
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	if payload.Message.From.ID == 0 || payload.Message.Chat.ID == 0 {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	chatType := payload.Message.Chat.Type
	if chatType != "" && chatType != "private" {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	chatIDStr := strconv.FormatInt(payload.Message.Chat.ID, 10)
	firstName := payload.Message.From.FirstName
	if firstName == "" {
		firstName = "Telegram User"
	}

	contact := &domain.Contact{
		TenantID:     tenantID,
		FirstName:    firstName,
		LastName:     payload.Message.From.LastName,
		Channel:      "telegram",
		RoutingValue: chatIDStr,
		Source:       "inbound_webhook",
		Status:       "active",
	}

	if err := h.contactUC.RegisterContact(r.Context(), contact); err != nil {
		log.Printf("[Webhook/Telegram] Failed to save inbound contact %s for tenant %s: %v\n", chatIDStr, tenantID, err)
	} else {
		log.Printf("[Webhook/Telegram] Synced inbound contact %s (%s) for tenant %s\n", chatIDStr, firstName, tenantID)
	}

	// Ensure contact.ID is populated for inbox recording
	if contact.ID == "" {
		if existing, err := h.contactUC.GetContactByRoutingValue(r.Context(), tenantID, "telegram", chatIDStr); err == nil && existing != nil {
			contact.ID = existing.ID
		}
	}

	// Record in Unified Team Inbox
	if h.inboxRepo != nil && payload.Message.Text != "" && contact.ID != "" {
		conv, cErr := h.inboxRepo.GetOrCreateConversation(r.Context(), tenantID, contact.ID, "telegram")
		if cErr == nil && conv != nil {
			newMsg := &domain.Message{
				TenantID:       tenantID,
				ConversationID: conv.ID,
				Direction:      domain.MessageDirectionInbound,
				SenderType:     domain.MessageSenderTypeContact,
				Body:           payload.Message.Text,
				PlatformMessageID: func() *string {
					if payload.Message.MessageID == 0 {
						return nil
					}
					id := strconv.Itoa(payload.Message.MessageID)
					return &id
				}(),
				Status: "delivered",
			}
			_ = h.inboxRepo.CreateMessage(r.Context(), newMsg)
			if h.broadcaster != nil {
				h.broadcaster.BroadcastMessage(tenantID, conv, newMsg)
			}
			// Autonomous AI Business Assistant: enqueue inbound customer inquiries
			if h.aiEngine != nil && h.aiAgentRepo != nil {
				job := service.AIReplyJob{
					TenantID: tenantID, ConversationID: conv.ID, Channel: "telegram",
					Recipient: chatIDStr, IncomingText: payload.Message.Text,
					PlatformID: strconv.Itoa(payload.Message.MessageID),
				}
				if h.aiDispatcher != nil {
					if err := h.aiDispatcher.EnqueueAIReply(r.Context(), job); err != nil {
						log.Printf("[Webhook/Telegram/AI] Queue unavailable, falling back to in-process reply: %v\n", err)
						go h.handleTelegramAIResponse(tenantID, conv, chatIDStr, payload.Message.Text)
					}
				} else {
					go h.handleTelegramAIResponse(tenantID, conv, chatIDStr, payload.Message.Text)
				}
			}
			log.Printf("[Webhook/Telegram] 💬 Recorded inbound message for tenant %s conversation %s: %q\n", tenantID, conv.ID, payload.Message.Text)
		} else {
			log.Printf("[Webhook/Telegram] ⚠️ Failed to get/create conversation for contact %s: %v\n", contact.ID, cErr)
		}
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// VerifyWhatsApp handles GET /api/v1/webhooks/whatsapp/{tenant_id} for Meta Webhook Challenge
func (h *WebhookHandler) VerifyWhatsApp(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	query := r.URL.Query()
	mode := query.Get("hub.mode")
	token := query.Get("hub.verify_token")
	challenge := query.Get("hub.challenge")

	if mode != "subscribe" || token == "" || challenge == "" {
		utils.WriteError(w, http.StatusForbidden, "Invalid verification request parameters")
		return
	}

	// Look up the stored verify_token for this tenant's WhatsApp channel
	channel, err := h.channelRepo.FindActiveByPlatform(r.Context(), tenantID, "whatsapp")
	if err != nil {
		log.Printf("[Webhook/WhatsApp] No active WhatsApp channel for tenant %s: %v\n", tenantID, err)
		utils.WriteError(w, http.StatusForbidden, "No active WhatsApp channel for this tenant")
		return
	}

	var creds map[string]string
	if err := json.Unmarshal(channel.EncryptedCredentials, &creds); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to read channel credentials")
		return
	}

	storedToken := creds["verify_token"]
	if storedToken == "" || storedToken != token {
		log.Printf("[Webhook/WhatsApp] ⛔ Verification token mismatch for tenant %s\n", tenantID)
		utils.WriteError(w, http.StatusForbidden, "Verification token mismatch")
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(challenge))
}

// HandleWhatsApp handles POST /api/v1/webhooks/whatsapp/{tenant_id} for Inbound Messaging
func (h *WebhookHandler) HandleWhatsApp(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing tenant_id in webhook URL")
		return
	}

	// Read body into buffer so we can both verify the signature AND decode JSON
	body, err := io.ReadAll(r.Body)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	defer r.Body.Close()

	// Verify Meta X-Hub-Signature-256
	sigHeader := r.Header.Get("X-Hub-Signature-256")
	if !verifyMetaSignature(body, sigHeader, h.metaAppSecret) {
		log.Printf("[Webhook/WhatsApp] ⛔ Rejected forged/unsigned request for tenant %s\n", tenantID)
		utils.WriteError(w, http.StatusForbidden, "Invalid webhook signature")
		return
	}

	var payload WhatsAppWebhookPayload
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid whatsapp webhook payload")
		return
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			val := change.Value
			for i, msg := range val.Messages {
				waID := msg.From
				if waID == "" {
					continue
				}

				name := "WhatsApp User"
				if i < len(val.Contacts) && val.Contacts[i].Profile.Name != "" {
					name = val.Contacts[i].Profile.Name
				}

				contact := &domain.Contact{
					TenantID:     tenantID,
					FirstName:    name,
					Channel:      "whatsapp",
					RoutingValue: waID,
					Source:       "inbound_webhook",
					Status:       "active",
				}

				if err := h.contactUC.RegisterContact(r.Context(), contact); err != nil {
					log.Printf("[Webhook/WhatsApp] Failed to save contact %s for tenant %s: %v\n", waID, tenantID, err)
				} else {
					log.Printf("[Webhook/WhatsApp] Synced inbound contact %s (%s) for tenant %s\n", waID, name, tenantID)
				}

				// Ensure contact.ID is populated for inbox recording
				if contact.ID == "" {
					if existing, err := h.contactUC.GetContactByRoutingValue(r.Context(), tenantID, "whatsapp", waID); err == nil && existing != nil {
						contact.ID = existing.ID
					}
				}

				// Record in Unified Team Inbox
				if h.inboxRepo != nil && contact.ID != "" {
					bodyText := msg.Text.Body
					if bodyText == "" && msg.Type != "" {
						bodyText = "[" + msg.Type + " message]"
					}
					if bodyText != "" {
						conv, cErr := h.inboxRepo.GetOrCreateConversation(r.Context(), tenantID, contact.ID, "whatsapp")
						if cErr == nil && conv != nil {
							newMsg := &domain.Message{
								TenantID:          tenantID,
								ConversationID:    conv.ID,
								Direction:         domain.MessageDirectionInbound,
								SenderType:        domain.MessageSenderTypeContact,
								Body:              bodyText,
								PlatformMessageID: &msg.ID,
								Status:            "delivered",
							}
							_ = h.inboxRepo.CreateMessage(r.Context(), newMsg)
							if h.broadcaster != nil {
								h.broadcaster.BroadcastMessage(tenantID, conv, newMsg)
							}
							log.Printf("[Webhook/WhatsApp] 💬 Recorded inbound message for tenant %s conversation %s: %q\n", tenantID, conv.ID, bodyText)
						} else {
							log.Printf("[Webhook/WhatsApp] ⚠️ Failed to get/create conversation for contact %s: %v\n", contact.ID, cErr)
						}
					}
				}
			}
		}
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *WebhookHandler) syncTelegramDestination(r *http.Request, tenantID string, chat TelegramChat) {
	channel, err := h.channelRepo.FindActiveByPlatform(r.Context(), tenantID, "telegram")
	if err != nil {
		log.Printf("[Webhook/Telegram] No active Telegram channel for tenant %s: %v\n", tenantID, err)
		return
	}

	title := chat.Title
	if title == "" && chat.Username != "" {
		title = "@" + chat.Username
	}
	if title == "" {
		title = fmt.Sprintf("Telegram %s", chat.Type)
	}

	destination := &domain.TelegramDestination{
		TenantID:       tenantID,
		ChannelID:      channel.ID,
		TelegramChatID: strconv.FormatInt(chat.ID, 10),
		Title:          title,
		Type:           chat.Type,
		Status:         "active",
		Source:         "webhook",
	}
	if err := h.destinationRepo.Upsert(r.Context(), destination); err != nil {
		log.Printf("[Webhook/Telegram] Failed to upsert destination %s for tenant %s: %v\n", destination.TelegramChatID, tenantID, err)
		return
	}
	log.Printf("[Webhook/Telegram] Synced destination %s (%s) for tenant %s\n", destination.Title, destination.TelegramChatID, tenantID)
}

func primaryDestinationChat(payload TelegramWebhookPayload) TelegramChat {
	if payload.MyChatMember.Chat.ID != 0 {
		return payload.MyChatMember.Chat
	}
	if payload.ChannelPost.Chat.ID != 0 {
		return payload.ChannelPost.Chat
	}
	return payload.Message.Chat
}

func isTelegramDestinationType(chatType string) bool {
	return chatType == "group" || chatType == "supergroup" || chatType == "channel"
}

func (h *WebhookHandler) handleTelegramAIResponse(tenantID string, conv *domain.Conversation, chatIDStr, incomingText string) {
	if h.aiEngine == nil {
		log.Printf("[Webhook/Telegram/AI] Skipping reply for tenant %s: AI engine is unavailable\n", tenantID)
		return
	}
	if h.aiAgentRepo == nil {
		log.Printf("[Webhook/Telegram/AI] Skipping reply for tenant %s: AI configuration repository is unavailable\n", tenantID)
		return
	}
	if !h.aiEngine.HasActiveProvider() {
		log.Printf("[Webhook/Telegram/AI] Skipping reply for tenant %s: no AI provider key is configured\n", tenantID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Check AI master toggle
	cfg, err := h.aiAgentRepo.GetConfig(ctx, tenantID)
	if err != nil {
		log.Printf("[Webhook/Telegram/AI] Failed to load AI configuration for tenant %s: %v\n", tenantID, err)
		return
	}
	if cfg == nil || !cfg.IsEnabled {
		log.Printf("[Webhook/Telegram/AI] Skipping reply for tenant %s: workspace AI assistance is disabled\n", tenantID)
		return
	}

	// 2. Check conversation AI state
	aiState, pausedUntil, needsHuman, err := h.aiAgentRepo.GetConversationAIState(ctx, tenantID, conv.ID)
	if err == nil {
		if aiState == "paused_by_human" {
			if pausedUntil != nil && time.Now().Before(*pausedUntil) {
				log.Printf("[Webhook/Telegram/AI] ⏸️ Skipping AI reply for conv %s: paused by human agent until %v\n", conv.ID, pausedUntil)
				return
			}
			_ = h.aiAgentRepo.SetConversationAIState(ctx, tenantID, conv.ID, "active", nil, false)
		} else if aiState == "disabled" {
			log.Printf("[Webhook/Telegram/AI] Skipping reply for conversation %s: AI is disabled for this conversation\n", conv.ID)
			return
		} else if aiState == "escalated" || needsHuman {
			log.Printf("[Webhook/Telegram/AI] Skipping reply for conversation %s: conversation is awaiting human attention\n", conv.ID)
			return
		}
	}

	// 3. Operational mode
	if !h.aiEngine.ShouldRespond(cfg) {
		log.Printf("[Webhook/Telegram/AI] Skipping reply for tenant %s: operational mode %q does not allow an automatic response right now\n", tenantID, cfg.OperationalMode)
		return
	}

	// 4. Knowledge base & history
	knowledge, _ := h.aiAgentRepo.ListKnowledgeItems(ctx, tenantID)
	recentMsgs, _ := h.inboxRepo.ListMessages(ctx, tenantID, conv.ID, 6, 0)
	for i, j := 0, len(recentMsgs)-1; i < j; i, j = i+1, j-1 {
		recentMsgs[i], recentMsgs[j] = recentMsgs[j], recentMsgs[i]
	}

	// 5. Generate reply with dual fallback
	replyText, needsHuman, leadInfo, err := h.aiEngine.GenerateReply(ctx, cfg, knowledge, recentMsgs, incomingText)
	if err != nil || strings.TrimSpace(replyText) == "" {
		return
	}

	// Auto-enrich audience contact details if lead info detected
	if leadInfo != nil && leadInfo.Name != "" && conv.ContactID != "" {
		_ = h.contactUC.RegisterContact(ctx, &domain.Contact{
			ID:           conv.ContactID,
			TenantID:     tenantID,
			FirstName:    leadInfo.Name,
			Channel:      "telegram",
			RoutingValue: chatIDStr,
			Source:       "telegram_lead_capture",
			Status:       "active",
		})
		log.Printf("[Webhook/Telegram/AI] 🎯 Auto-captured lead name %q for chat %s\n", leadInfo.Name, chatIDStr)
	}

	// 6. Send reply to Telegram API
	ch, err := h.channelRepo.FindActiveByPlatform(ctx, tenantID, "telegram")
	if err != nil {
		return
	}
	var creds struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.Unmarshal(ch.EncryptedCredentials, &creds); err != nil || creds.BotToken == "" {
		return
	}

	tgURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", creds.BotToken)
	tgPayload := map[string]interface{}{
		"chat_id": chatIDStr,
		"text":    replyText,
	}
	bodyBytes, _ := json.Marshal(tgPayload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tgURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Webhook/Telegram/AI] ❌ Failed to send Telegram reply: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("[Webhook/Telegram/AI] ❌ Telegram rejected AI reply (status %d): %s\n", resp.StatusCode, string(body))
		return
	}
	var telegramResponse struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&telegramResponse); err != nil || !telegramResponse.OK {
		log.Printf("[Webhook/Telegram/AI] ❌ Telegram returned an unsuccessful AI reply response\n")
		return
	}

	// 7. Persist bot reply in database
	platformMessageID := strconv.Itoa(telegramResponse.Result.MessageID)
	botMsg := &domain.Message{
		TenantID:          tenantID,
		ConversationID:    conv.ID,
		Direction:         domain.MessageDirectionOutbound,
		SenderType:        domain.MessageSenderTypeBot,
		Body:              replyText,
		PlatformMessageID: &platformMessageID,
		Status:            "delivered",
		CreatedAt:         time.Now().UTC(),
	}
	_ = h.inboxRepo.CreateMessage(ctx, botMsg)

	// 8. Update conversation state if escalated
	if needsHuman {
		_ = h.aiAgentRepo.SetConversationAIState(ctx, tenantID, conv.ID, "escalated", nil, true)
		if h.broadcaster != nil {
			h.broadcaster.BroadcastConversationAIState(tenantID, conv.ID, "escalated", nil, true)
		}
	}

	// 9. Broadcast bot reply to Team Inbox
	if h.broadcaster != nil {
		h.broadcaster.BroadcastMessage(tenantID, conv, botMsg)
	}

	log.Printf("[Webhook/Telegram/AI] 🤖 Dispatched AI bot reply for tenant %s to chat %s (conv: %s, needsHuman: %v)\n",
		tenantID, chatIDStr, conv.ID, needsHuman)
}
