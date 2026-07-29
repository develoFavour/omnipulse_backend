package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type WebhookHandler struct {
	contactUC       domain.ContactUseCase
	channelRepo     domain.ChannelRepository
	destinationRepo domain.TelegramDestinationRepository
}

func NewWebhookHandler(contactUC domain.ContactUseCase, channelRepo domain.ChannelRepository, destinationRepo domain.TelegramDestinationRepository) *WebhookHandler {
	return &WebhookHandler{contactUC: contactUC, channelRepo: channelRepo, destinationRepo: destinationRepo}
}

type TelegramWebhookPayload struct {
	UpdateID int `json:"update_id"`
	Message  struct {
		From struct {
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
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
			Field string `json:"field"`
		} `json:"changes"`
	} `json:"entry"`
}

func (h *WebhookHandler) HandleTelegram(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing tenant_id in webhook URL")
		return
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
		firstName = payload.Message.From.Username
	}
	if firstName == "" {
		firstName = "Unknown Telegram User"
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
		log.Printf("[Webhook/WhatsApp] Failed to parse credentials for tenant %s: %v\n", tenantID, err)
		utils.WriteError(w, http.StatusForbidden, "Invalid channel configuration")
		return
	}

	storedVerifyToken := creds["verify_token"]
	if storedVerifyToken == "" || storedVerifyToken != token {
		log.Printf("[Webhook/WhatsApp] Verify token mismatch for tenant %s\n", tenantID)
		utils.WriteError(w, http.StatusForbidden, "Verify token does not match")
		return
	}

	log.Printf("[Webhook/WhatsApp] Verified webhook challenge for tenant %s\n", tenantID)
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

	var payload WhatsAppWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
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
