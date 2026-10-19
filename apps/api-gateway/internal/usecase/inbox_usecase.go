package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/service"
)

type InboxUseCase interface {
	ListConversations(ctx context.Context, tenantID string, filter domain.ConversationFilter) ([]domain.Conversation, int, error)
	GetConversation(ctx context.Context, tenantID, conversationID string) (*domain.Conversation, error)
	ListMessages(ctx context.Context, tenantID, conversationID string, limit, offset int) ([]domain.Message, error)
	UpdateConversationStatus(ctx context.Context, tenantID, conversationID string, status domain.ConversationStatus) error
	AssignConversation(ctx context.Context, tenantID, conversationID string, userID *string) error
	MarkConversationAsRead(ctx context.Context, tenantID, conversationID string) error
	SendReply(ctx context.Context, tenantID, userID, conversationID string, req domain.SendReplyRequest) (*domain.Message, error)
	EditMessage(ctx context.Context, tenantID, messageID, newBody string) (*domain.Message, error)
	DeleteMessage(ctx context.Context, tenantID, messageID string) error
	SetBroadcaster(b domain.InboxBroadcaster)
	SetAIAgentRepo(repo domain.AIAgentRepository)
}

type inboxUseCase struct {
	inboxRepo   domain.InboxRepository
	contactRepo domain.ContactRepository
	channelRepo domain.ChannelRepository
	waManager   *service.WhatsAppManager
	broadcaster domain.InboxBroadcaster
	aiAgentRepo domain.AIAgentRepository
}

func (u *inboxUseCase) SetBroadcaster(b domain.InboxBroadcaster) {
	u.broadcaster = b
}

func (u *inboxUseCase) SetAIAgentRepo(repo domain.AIAgentRepository) {
	u.aiAgentRepo = repo
}

func NewInboxUseCase(
	inboxRepo domain.InboxRepository,
	contactRepo domain.ContactRepository,
	channelRepo domain.ChannelRepository,
	waManager *service.WhatsAppManager,
) InboxUseCase {
	return &inboxUseCase{
		inboxRepo:   inboxRepo,
		contactRepo: contactRepo,
		channelRepo: channelRepo,
		waManager:   waManager,
	}
}

func (u *inboxUseCase) ListConversations(ctx context.Context, tenantID string, filter domain.ConversationFilter) ([]domain.Conversation, int, error) {
	return u.inboxRepo.ListConversations(ctx, tenantID, filter)
}

func (u *inboxUseCase) GetConversation(ctx context.Context, tenantID, conversationID string) (*domain.Conversation, error) {
	return u.inboxRepo.GetConversation(ctx, tenantID, conversationID)
}

func (u *inboxUseCase) ListMessages(ctx context.Context, tenantID, conversationID string, limit, offset int) ([]domain.Message, error) {
	return u.inboxRepo.ListMessages(ctx, tenantID, conversationID, limit, offset)
}

func (u *inboxUseCase) UpdateConversationStatus(ctx context.Context, tenantID, conversationID string, status domain.ConversationStatus) error {
	return u.inboxRepo.UpdateConversationStatus(ctx, tenantID, conversationID, status)
}

func (u *inboxUseCase) AssignConversation(ctx context.Context, tenantID, conversationID string, userID *string) error {
	return u.inboxRepo.AssignConversation(ctx, tenantID, conversationID, userID)
}

func (u *inboxUseCase) MarkConversationAsRead(ctx context.Context, tenantID, conversationID string) error {
	return u.inboxRepo.MarkConversationAsRead(ctx, tenantID, conversationID)
}

func (u *inboxUseCase) SendReply(ctx context.Context, tenantID, userID, conversationID string, req domain.SendReplyRequest) (*domain.Message, error) {
	conv, err := u.inboxRepo.GetConversation(ctx, tenantID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch conversation: %w", err)
	}

	if conv.Contact == nil {
		return nil, fmt.Errorf("conversation has no associated contact")
	}

	routingValue := conv.Contact.RoutingValue
	var platformMsgID *string
	var sendErr error

	// Dispatch outbound reply based on channel
	if conv.Channel == "whatsapp" {
		sent := false
		// 1. Try WhatsApp Multi-Device session first
		if u.waManager != nil {
			waMsgID, waErr := u.waManager.SendMessage(ctx, tenantID, routingValue, req.Body, req.MediaURL)
			if waErr == nil {
				sent = true
				platformMsgID = &waMsgID
				log.Printf("[INBOX] WhatsApp Multi-Device reply sent to %s for tenant %s (msgID: %s)\n", routingValue, tenantID, waMsgID)
			} else {
				log.Printf("[INBOX] WhatsApp Multi-Device reply failed: %v. Attempting Cloud API...\n", waErr)
			}
		}

		// 2. Fallback to WhatsApp Cloud API
		if !sent {
			ch, chErr := u.channelRepo.FindActiveByPlatform(ctx, tenantID, "whatsapp")
			if chErr != nil {
				sendErr = fmt.Errorf("no active WhatsApp channel found: %w", chErr)
			} else {
				var creds map[string]interface{}
				_ = json.Unmarshal(ch.EncryptedCredentials, &creds)
				phoneID, _ := creds["phone_number_id"].(string)
				token, _ := creds["access_token"].(string)

				if phoneID == "" || token == "" {
					sendErr = fmt.Errorf("missing WhatsApp Cloud API credentials")
				} else {
					var waPayload map[string]interface{}
					if req.MediaURL != nil && *req.MediaURL != "" {
						waPayload = map[string]interface{}{
							"messaging_product": "whatsapp",
							"recipient_type":    "individual",
							"to":                routingValue,
							"type":              "image",
							"image": map[string]interface{}{
								"link":    *req.MediaURL,
								"caption": req.Body,
							},
						}
					} else {
						waPayload = map[string]interface{}{
							"messaging_product": "whatsapp",
							"recipient_type":    "individual",
							"to":                routingValue,
							"type":              "text",
							"text": map[string]interface{}{
								"preview_url": false,
								"body":        req.Body,
							},
						}
					}

					bodyBytes, _ := json.Marshal(waPayload)
					url := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/messages", phoneID)
					httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
					if reqErr == nil {
						httpReq.Header.Set("Authorization", "Bearer "+token)
						httpReq.Header.Set("Content-Type", "application/json")
						client := &http.Client{Timeout: 15 * time.Second}
						resp, doErr := client.Do(httpReq)
						if doErr != nil {
							sendErr = fmt.Errorf("failed to call WhatsApp API: %w", doErr)
						} else {
							defer resp.Body.Close()
							if resp.StatusCode >= 400 {
								sendErr = fmt.Errorf("WhatsApp API returned error %d", resp.StatusCode)
							} else {
								var waResp struct {
									Messages []struct {
										ID string `json:"id"`
									} `json:"messages"`
								}
								if err := json.NewDecoder(resp.Body).Decode(&waResp); err == nil && len(waResp.Messages) > 0 {
									platformMsgID = &waResp.Messages[0].ID
								}
							}
						}
					}
				}
			}
		}
	} else if conv.Channel == "telegram" {
		ch, chErr := u.channelRepo.FindActiveByPlatform(ctx, tenantID, "telegram")
		if chErr != nil {
			sendErr = fmt.Errorf("no active Telegram channel found: %w", chErr)
		} else {
			var creds struct {
				BotToken string `json:"bot_token"`
			}
			if err := json.Unmarshal(ch.EncryptedCredentials, &creds); err != nil || creds.BotToken == "" {
				sendErr = fmt.Errorf("invalid Telegram bot token")
			} else {
				var tgURL string
				var tgPayload map[string]interface{}
				if req.MediaURL != nil && *req.MediaURL != "" {
					tgURL = fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", creds.BotToken)
					tgPayload = map[string]interface{}{
						"chat_id": routingValue,
						"photo":   *req.MediaURL,
						"caption": req.Body,
					}
				} else {
					tgURL = fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", creds.BotToken)
					tgPayload = map[string]interface{}{
						"chat_id": routingValue,
						"text":    req.Body,
					}
				}

				bodyBytes, _ := json.Marshal(tgPayload)
				httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, tgURL, bytes.NewBuffer(bodyBytes))
				if reqErr == nil {
					httpReq.Header.Set("Content-Type", "application/json")
					client := &http.Client{Timeout: 15 * time.Second}
					resp, doErr := client.Do(httpReq)
					if doErr != nil {
						sendErr = fmt.Errorf("network error calling Telegram API: %w", doErr)
					} else {
						defer resp.Body.Close()
						if resp.StatusCode != http.StatusOK {
							sendErr = fmt.Errorf("Telegram API returned error %d", resp.StatusCode)
						} else {
							var tgResp struct {
								Ok     bool `json:"ok"`
								Result struct {
									MessageID int `json:"message_id"`
								} `json:"result"`
							}
							if err := json.NewDecoder(resp.Body).Decode(&tgResp); err == nil && tgResp.Result.MessageID != 0 {
								midStr := strconv.Itoa(tgResp.Result.MessageID)
								platformMsgID = &midStr
							}
						}
					}
				}
			}
		}
	} else {
		sendErr = fmt.Errorf("unsupported channel for direct reply: %s", conv.Channel)
	}

	if sendErr != nil {
		return nil, sendErr
	}

	// Persist outbound message in database
	msgStatus := "sent"
	msg := &domain.Message{
		TenantID:          tenantID,
		ConversationID:    conversationID,
		Direction:         domain.MessageDirectionOutbound,
		SenderType:        domain.MessageSenderTypeAgent,
		SenderID:          &userID,
		Body:              req.Body,
		MediaURL:          req.MediaURL,
		PlatformMessageID: platformMsgID,
		Status:            msgStatus,
		CreatedAt:         time.Now().UTC(),
	}

	if err := u.inboxRepo.CreateMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("message was sent to recipient but failed to record in database: %w", err)
	}

	// Autonomous AI Handoff: When a human agent manually replies from the inbox,
	// automatically pause the AI for 30 minutes so it does not speak over the agent.
	if u.aiAgentRepo != nil {
		pausedUntil := time.Now().Add(30 * time.Minute)
		_ = u.aiAgentRepo.SetConversationAIState(ctx, tenantID, conversationID, "paused_by_human", &pausedUntil, false)
		if u.broadcaster != nil {
			u.broadcaster.BroadcastConversationAIState(tenantID, conversationID, "paused_by_human", &pausedUntil, false)
		}
	}

	if u.broadcaster != nil {
		u.broadcaster.BroadcastMessage(tenantID, conv, msg)
	}

	return msg, nil
}

// EditMessage updates the message body on the platform (WhatsApp / Telegram) and in the DB,
// then broadcasts a MESSAGE_EDITED event to all connected clients.
func (u *inboxUseCase) EditMessage(ctx context.Context, tenantID, messageID, newBody string) (*domain.Message, error) {
	if newBody == "" {
		return nil, fmt.Errorf("new body cannot be empty")
	}

	// 1. Edit in DB (also validates that the message is outbound and not deleted)
	updated, err := u.inboxRepo.EditMessage(ctx, tenantID, messageID, newBody)
	if err != nil {
		return nil, err
	}

	// 2. Push edit to WhatsApp or Telegram platform
	conv, convErr := u.inboxRepo.GetConversation(ctx, tenantID, updated.ConversationID)
	if convErr == nil && conv != nil && conv.Contact != nil && updated.PlatformMessageID != nil && *updated.PlatformMessageID != "" {
		platformMsgID := *updated.PlatformMessageID
		routingValue := conv.Contact.RoutingValue

		if conv.Channel == "whatsapp" && u.waManager != nil {
			if eErr := u.waManager.EditMessage(ctx, tenantID, routingValue, platformMsgID, newBody); eErr != nil {
				log.Printf("[INBOX] Warning: failed to edit WhatsApp message on platform: %v\n", eErr)
			} else {
				log.Printf("[INBOX] ✅ WhatsApp message %s edited on platform for tenant %s\n", platformMsgID, tenantID)
			}
		} else if conv.Channel == "telegram" {
			ch, chErr := u.channelRepo.FindActiveByPlatform(ctx, tenantID, "telegram")
			if chErr == nil && ch != nil {
				var creds struct {
					BotToken string `json:"bot_token"`
				}
				if json.Unmarshal(ch.EncryptedCredentials, &creds) == nil && creds.BotToken != "" {
					msgIDInt, pErr := strconv.Atoi(platformMsgID)
					if pErr == nil {
						tgURL := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", creds.BotToken)
						payload := map[string]interface{}{
							"chat_id":    routingValue,
							"message_id": msgIDInt,
							"text":       newBody,
						}
						bodyBytes, _ := json.Marshal(payload)
						req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, tgURL, bytes.NewBuffer(bodyBytes))
						if rErr == nil {
							req.Header.Set("Content-Type", "application/json")
							client := &http.Client{Timeout: 10 * time.Second}
							resp, dErr := client.Do(req)
							if dErr == nil {
								resp.Body.Close()
								log.Printf("[INBOX] ✅ Telegram message %s edited on platform\n", platformMsgID)
							}
						}
					}
				}
			}
		}
	}

	// 3. Broadcast real-time update
	if u.broadcaster != nil {
		u.broadcaster.BroadcastMessageEdit(tenantID, updated.ConversationID, updated.ID, updated.Body)
	}

	return updated, nil
}

// DeleteMessage soft-deletes the message in the DB and revokes it on the platform (WhatsApp / Telegram),
// then broadcasts a MESSAGE_DELETED event to all connected clients.
func (u *inboxUseCase) DeleteMessage(ctx context.Context, tenantID, messageID string) error {
	// 1. Soft-delete in DB
	deleted, err := u.inboxRepo.DeleteMessage(ctx, tenantID, messageID)
	if err != nil {
		return err
	}

	// 2. Revoke message on WhatsApp or Telegram platform
	conv, convErr := u.inboxRepo.GetConversation(ctx, tenantID, deleted.ConversationID)
	if convErr == nil && conv != nil && conv.Contact != nil && deleted.PlatformMessageID != nil && *deleted.PlatformMessageID != "" {
		platformMsgID := *deleted.PlatformMessageID
		routingValue := conv.Contact.RoutingValue

		if conv.Channel == "whatsapp" && u.waManager != nil {
			if rErr := u.waManager.RevokeMessage(ctx, tenantID, routingValue, platformMsgID); rErr != nil {
				log.Printf("[INBOX] Warning: failed to revoke WhatsApp message on platform: %v\n", rErr)
			} else {
				log.Printf("[INBOX] ✅ WhatsApp message %s revoked on platform for tenant %s\n", platformMsgID, tenantID)
			}
		} else if conv.Channel == "telegram" {
			ch, chErr := u.channelRepo.FindActiveByPlatform(ctx, tenantID, "telegram")
			if chErr == nil && ch != nil {
				var creds struct {
					BotToken string `json:"bot_token"`
				}
				if json.Unmarshal(ch.EncryptedCredentials, &creds) == nil && creds.BotToken != "" {
					msgIDInt, pErr := strconv.Atoi(platformMsgID)
					if pErr == nil {
						tgURL := fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", creds.BotToken)
						payload := map[string]interface{}{
							"chat_id":    routingValue,
							"message_id": msgIDInt,
						}
						bodyBytes, _ := json.Marshal(payload)
						req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, tgURL, bytes.NewBuffer(bodyBytes))
						if rErr == nil {
							req.Header.Set("Content-Type", "application/json")
							client := &http.Client{Timeout: 10 * time.Second}
							resp, dErr := client.Do(req)
							if dErr == nil {
								resp.Body.Close()
								log.Printf("[INBOX] ✅ Telegram message %s deleted on platform\n", platformMsgID)
							}
						}
					}
				}
			}
		}
	}

	// 3. Broadcast real-time delete
	if u.broadcaster != nil {
		u.broadcaster.BroadcastMessageDelete(tenantID, deleted.ConversationID, deleted.ID)
	}

	return nil
}
