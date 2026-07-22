package handler

import (
	"encoding/json"
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
		title = "Telegram " + chat.Type
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
