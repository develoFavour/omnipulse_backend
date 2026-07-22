package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/apps/api-gateway/internal/utils"
)

type telegramGetMeResponse struct {
	Ok     bool `json:"ok"`
	Result struct {
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	} `json:"result"`
	Description string `json:"description"`
}

type telegramSetWebhookResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
}

func verifyTelegramToken(token string) (string, string, error) {
	resp, err := http.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token))
	if err != nil {
		return "", "", fmt.Errorf("failed to reach telegram api: %v", err)
	}
	defer resp.Body.Close()

	var tgResp telegramGetMeResponse
	_ = json.NewDecoder(resp.Body).Decode(&tgResp)

	if resp.StatusCode != http.StatusOK || !tgResp.Ok {
		if tgResp.Description != "" {
			return "", "", errors.New(tgResp.Description)
		}
		return "", "", fmt.Errorf("invalid bot token")
	}

	identity := tgResp.Result.FirstName
	if tgResp.Result.Username != "" {
		identity = "@" + tgResp.Result.Username
	}
	return identity, tgResp.Result.Username, nil
}

func configureTelegramWebhook(token, publicAPIBaseURL, tenantID string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(publicAPIBaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("PUBLIC_API_BASE_URL is required to configure Telegram webhooks")
	}

	webhookURL := fmt.Sprintf("%s/api/v1/webhooks/telegram/%s", baseURL, tenantID)
	body, _ := json.Marshal(map[string]string{"url": webhookURL})
	resp, err := http.Post(fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", token), "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to reach telegram setWebhook: %v", err)
	}
	defer resp.Body.Close()

	var tgResp telegramSetWebhookResponse
	_ = json.NewDecoder(resp.Body).Decode(&tgResp)
	if resp.StatusCode != http.StatusOK || !tgResp.Ok {
		if tgResp.Description != "" {
			return errors.New(tgResp.Description)
		}
		return fmt.Errorf("telegram rejected webhook configuration")
	}
	return nil
}

type ChannelHandler struct {
	repo             domain.ChannelRepository
	publicAPIBaseURL string
}

func NewChannelHandler(repo domain.ChannelRepository, publicAPIBaseURL string) *ChannelHandler {
	return &ChannelHandler{repo: repo, publicAPIBaseURL: publicAPIBaseURL}
}

func (h *ChannelHandler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	var payload domain.TenantChannel
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	payload.TenantID = tenantID
	payload.Status = "active"

	if payload.PlatformName == "telegram" {
		var creds map[string]string
		if err := json.Unmarshal(payload.EncryptedCredentials, &creds); err != nil {
			utils.WriteError(w, http.StatusBadRequest, "Invalid credentials format")
			return
		}

		token := creds["bot_token"]
		if token == "" {
			utils.WriteError(w, http.StatusBadRequest, "Missing bot_token for telegram channel")
			return
		}

		botIdentity, botUsername, err := verifyTelegramToken(token)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("Telegram verification failed: %v", err))
			return
		}

		creds["bot_username"] = botUsername
		updatedCreds, _ := json.Marshal(creds)
		payload.EncryptedCredentials = updatedCreds
		payload.SenderIdentity = botIdentity

		if err := configureTelegramWebhook(token, h.publicAPIBaseURL, tenantID); err != nil {
			utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("Telegram webhook setup failed: %v", err))
			return
		}
	}

	if err := h.repo.CreateChannel(r.Context(), &payload); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to create channel")
		return
	}

	utils.WriteJSON(w, http.StatusCreated, payload)
}

func (h *ChannelHandler) ListChannels(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	channels, err := h.repo.ListByTenant(r.Context(), tenantID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list channels")
		return
	}

	if channels == nil {
		channels = []domain.TenantChannel{}
	}
	utils.WriteJSON(w, http.StatusOK, channels)
}
