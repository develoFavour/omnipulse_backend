package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
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

// WhatsApp Cloud API phone number verification response
type whatsAppPhoneNumberResponse struct {
	VerifiedName string `json:"verified_name"`
	DisplayPhone string `json:"display_phone_number"`
	ID           string `json:"id"`
	Error        *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
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

// verifyWhatsAppCredentials calls the Meta Graph API to verify the phone number ID and access token are valid
func verifyWhatsAppCredentials(phoneNumberID, accessToken string) (string, string, error) {
	url := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?fields=verified_name,display_phone_number", phoneNumberID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to build Meta API request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to reach Meta Graph API: %v", err)
	}
	defer resp.Body.Close()

	var waResp whatsAppPhoneNumberResponse
	_ = json.NewDecoder(resp.Body).Decode(&waResp)

	if resp.StatusCode != http.StatusOK || waResp.Error != nil {
		if waResp.Error != nil {
			return "", "", fmt.Errorf("Meta API error (%d): %s", waResp.Error.Code, waResp.Error.Message)
		}
		return "", "", fmt.Errorf("invalid WhatsApp credentials (HTTP %d)", resp.StatusCode)
	}

	verifiedName := waResp.VerifiedName
	if verifiedName == "" {
		verifiedName = "WhatsApp Business"
	}

	displayPhone := waResp.DisplayPhone
	if displayPhone == "" {
		displayPhone = phoneNumberID
	}

	return verifiedName, displayPhone, nil
}

type MetaAppConfig struct {
	AppID         string
	AppSecret     string
	WABAConfigID  string
	WABAID        string
	PhoneNumberID string
}

type ChannelHandler struct {
	repo             domain.ChannelRepository
	publicAPIBaseURL string
	publicAppBaseURL string
	metaConfig       MetaAppConfig
}

func NewChannelHandler(repo domain.ChannelRepository, publicAPIBaseURL, publicAppBaseURL string, metaConfig MetaAppConfig) *ChannelHandler {
	return &ChannelHandler{repo: repo, publicAPIBaseURL: publicAPIBaseURL, publicAppBaseURL: publicAppBaseURL, metaConfig: metaConfig}
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
	} else if payload.PlatformName == "whatsapp" {
		var creds map[string]string
		if err := json.Unmarshal(payload.EncryptedCredentials, &creds); err != nil {
			utils.WriteError(w, http.StatusBadRequest, "Invalid credentials format")
			return
		}

		phoneNumberID := creds["phone_number_id"]
		accessToken := creds["access_token"]
		if phoneNumberID == "" || accessToken == "" {
			utils.WriteError(w, http.StatusBadRequest, "Missing phone_number_id or access_token for WhatsApp channel")
			return
		}

		// Verify credentials with Meta Graph API
		verifiedName, displayPhone, err := verifyWhatsAppCredentials(phoneNumberID, accessToken)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, fmt.Sprintf("WhatsApp verification failed: %v", err))
			return
		}

		payload.SenderIdentity = fmt.Sprintf("%s (%s)", verifiedName, displayPhone)

		// Store the verify_token if provided (used for webhook challenge)
		updatedCreds, _ := json.Marshal(creds)
		payload.EncryptedCredentials = updatedCreds
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

// HandleDisconnectChannel removes a channel connection by platform name
// DELETE /api/v1/channels/{platform}
func (h *ChannelHandler) HandleDisconnectChannel(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	platform := r.PathValue("platform")
	if platform == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing platform parameter")
		return
	}

	if err := h.repo.DeleteByPlatform(r.Context(), tenantID, platform); err != nil {
		utils.WriteError(w, http.StatusNotFound, err.Error())
		return
	}

	log.Printf("[Channel-Disconnect] %s channel disconnected for tenant %s\n", platform, tenantID)
	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"message":  fmt.Sprintf("%s channel disconnected successfully", platform),
		"platform": platform,
	})
}

func cleanBaseURL(raw string) string {
	b := strings.TrimSpace(raw)
	b = strings.TrimRight(b, "/")
	b = strings.TrimSuffix(b, "/connections")
	b = strings.TrimRight(b, "/")
	return b
}

// HandleWhatsAppOAuthConfig returns the Meta App ID and Config ID needed by the frontend
// to initialize the Facebook SDK Embedded Signup flow.
// GET /api/v1/channels/whatsapp/oauth/config
func (h *ChannelHandler) HandleWhatsAppOAuthConfig(w http.ResponseWriter, r *http.Request) {
	if h.metaConfig.AppID == "" || h.metaConfig.WABAConfigID == "" {
		utils.WriteError(w, http.StatusPreconditionFailed, "Meta Embedded Signup is not configured. Set META_APP_ID and META_WABA_CONFIG_ID environment variables.")
		return
	}

	base := cleanBaseURL(h.publicAppBaseURL)
	redirectURI := ""
	if base != "" {
		redirectURI = base + "/connections"
	}

	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"app_id":       h.metaConfig.AppID,
		"config_id":    h.metaConfig.WABAConfigID,
		"redirect_uri": redirectURI,
	})
}

// HandleWhatsAppOAuthCallback receives the auth code from Meta Embedded Signup,
// exchanges it for a permanent token, fetches WABA details, and saves the channel.
// POST /api/v1/channels/whatsapp/oauth/callback
func (h *ChannelHandler) HandleWhatsAppOAuthCallback(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	if h.metaConfig.AppID == "" || h.metaConfig.AppSecret == "" {
		utils.WriteError(w, http.StatusPreconditionFailed, "Meta OAuth is not configured. Set META_APP_ID and META_APP_SECRET environment variables.")
		return
	}

	var req struct {
		Code          string `json:"code"`
		WABAID        string `json:"waba_id,omitempty"`
		PhoneNumberID string `json:"phone_number_id,omitempty"`
		RedirectURI   string `json:"redirect_uri,omitempty"`
		Source        string `json:"source,omitempty"` // "embedded_signup" or "direct_dialog"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body. Expected { \"code\": \"...\" }")
		return
	}
	if req.Code == "" {
		utils.WriteError(w, http.StatusBadRequest, "Missing authorization code")
		return
	}

	log.Printf("[WhatsApp-OAuth] stage=received_code app_id=%s code_length=%d source=%q waba_id_present=%t phone_id_present=%t\n", h.metaConfig.AppID, len(req.Code), req.Source, req.WABAID != "", req.PhoneNumberID != "")

	var tokenResult struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Error       *struct {
			Message      string `json:"message"`
			Code         int    `json:"code"`
			ErrorSubcode int    `json:"error_subcode"`
			FBTraceID    string `json:"fbtrace_id"`
		} `json:"error"`
	}

	if req.Source == "embedded_signup" {
		redirectURI := req.RedirectURI
		if redirectURI == "" && h.publicAppBaseURL != "" {
			redirectURI = cleanBaseURL(h.publicAppBaseURL) + "/connections"
		}
		formData := url.Values{}
		formData.Set("client_id", h.metaConfig.AppID)
		formData.Set("client_secret", h.metaConfig.AppSecret)
		formData.Set("code", req.Code)
		formData.Set("grant_type", "authorization_code")
		if redirectURI != "" {
			formData.Set("redirect_uri", redirectURI)
		}
		log.Printf("[WhatsApp-OAuth-Exchange] Exchanging FB.login code with Meta (redirect_uri: %q)...\n", redirectURI)
		resp, err := http.PostForm("https://graph.facebook.com/v21.0/oauth/access_token", formData)
		if err != nil {
			log.Printf("[WhatsApp-OAuth-Exchange-Error] Network error: %v\n", err)
			utils.WriteError(w, http.StatusBadGateway, "Failed to connect to Meta for token exchange")
			return
		}
		_ = json.NewDecoder(resp.Body).Decode(&tokenResult)
		resp.Body.Close()
		log.Printf("[WhatsApp-OAuth] stage=meta_token_response http_status=%s source=embedded_signup\n", resp.Status)
	} else {
		// Direct dialog codes: try redirect_uri candidates via POST
		rawCandidates := []string{}
		if req.RedirectURI != "" {
			cleaned := strings.TrimRight(req.RedirectURI, "/")
			if strings.HasSuffix(cleaned, "/connections/connections") {
				cleaned = strings.TrimSuffix(cleaned, "/connections")
			}
			if cleaned != "" {
				rawCandidates = append(rawCandidates, cleaned)
			}
		}
		base := cleanBaseURL(h.publicAppBaseURL)
		if base != "" {
			rawCandidates = append(rawCandidates, base+"/connections")
		}

		// Deduplicate candidates preserving priority order
		seen := make(map[string]bool)
		var candidateURIs []string
		for _, c := range rawCandidates {
			if !seen[c] {
				seen[c] = true
				candidateURIs = append(candidateURIs, c)
			}
		}

		log.Printf("[WhatsApp-OAuth] candidates=%v\n", candidateURIs)

		for _, rURI := range candidateURIs {
			formData := url.Values{}
			formData.Set("client_id", h.metaConfig.AppID)
			formData.Set("client_secret", h.metaConfig.AppSecret)
			formData.Set("code", req.Code)
			formData.Set("grant_type", "authorization_code")
			if rURI != "" {
				formData.Set("redirect_uri", rURI)
			}

			log.Printf("[WhatsApp-OAuth-Exchange] Trying code exchange with Meta (redirect_uri: %q)...\n", rURI)
			resp, err := http.PostForm("https://graph.facebook.com/v21.0/oauth/access_token", formData)
			if err != nil {
				log.Printf("[WhatsApp-OAuth-Exchange-Error] Network error with redirect_uri %q: %v\n", rURI, err)
				continue
			}
			log.Printf("[WhatsApp-OAuth] stage=meta_token_response http_status=%s redirect_uri=%q\n", resp.Status, rURI)

			var attempt struct {
				AccessToken string `json:"access_token"`
				TokenType   string `json:"token_type"`
				ExpiresIn   int    `json:"expires_in"`
				Error       *struct {
					Message      string `json:"message"`
					Code         int    `json:"code"`
					ErrorSubcode int    `json:"error_subcode"`
					FBTraceID    string `json:"fbtrace_id"`
				} `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&attempt)
			resp.Body.Close()

			if attempt.AccessToken != "" {
				tokenResult = attempt
				log.Printf("[WhatsApp-OAuth-Exchange-Success] Token acquired via redirect_uri: %q\n", rURI)
				break
			}

			if attempt.Error != nil {
				tokenResult = attempt
				log.Printf("[WhatsApp-OAuth-Exchange-Attempt] redirect_uri %q failed: Meta Error %d (subcode %d): %s\n", rURI, attempt.Error.Code, attempt.Error.ErrorSubcode, attempt.Error.Message)
			}
		}
	}

	if tokenResult.Error != nil {
		log.Printf("[WhatsApp-OAuth] stage=failed error_code=%d error_subcode=%d fbtrace_id=%s message=%q\n", tokenResult.Error.Code, tokenResult.Error.ErrorSubcode, tokenResult.Error.FBTraceID, tokenResult.Error.Message)
		utils.WriteError(w, http.StatusBadGateway, fmt.Sprintf("Meta OAuth error (%d): %s (trace: %s)", tokenResult.Error.Code, tokenResult.Error.Message, tokenResult.Error.FBTraceID))
		return
	}

	if tokenResult.AccessToken == "" {
		utils.WriteError(w, http.StatusBadGateway, "Meta returned an empty access token")
		return
	}

	log.Printf("[WhatsApp-OAuth] stage=token_received token_present=true\n")

	longLivedToken := tokenResult.AccessToken
	client := &http.Client{}

	// Step 2: Discover or verify the WABA ID
	wabaID := req.WABAID
	verifiedName := "WhatsApp Business"
	displayPhone := ""

	if wabaID == "" {
		// Method 1 (Official & Preferred): Inspect token's granular_scopes via debug_token
		debugURL := fmt.Sprintf("https://graph.facebook.com/v21.0/debug_token?input_token=%s&access_token=%s|%s",
			url.QueryEscape(longLivedToken),
			url.QueryEscape(h.metaConfig.AppID),
			url.QueryEscape(h.metaConfig.AppSecret),
		)
		debugReq, _ := http.NewRequest("GET", debugURL, nil)
		debugResp, err := client.Do(debugReq)
		if err == nil {
			var debugResult struct {
				Data struct {
					GranularScopes []struct {
						Scope     string   `json:"scope"`
						TargetIDs []string `json:"target_ids"`
					} `json:"granular_scopes"`
				} `json:"data"`
			}
			if json.NewDecoder(debugResp.Body).Decode(&debugResult) == nil {
				for _, gs := range debugResult.Data.GranularScopes {
					if len(gs.TargetIDs) > 0 {
						wabaID = gs.TargetIDs[0]
						log.Printf("[WhatsApp-OAuth] Discovered WABA ID %s from granular_scopes (%s)\n", wabaID, gs.Scope)
						break
					}
				}
			}
			debugResp.Body.Close()
		}
	}

	if wabaID != "" {
		// Verify and fetch details for the WABA
		wabaInfoURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?fields=id,name,verified_name,display_phone_number", wabaID)
		wabaInfoReq, _ := http.NewRequest("GET", wabaInfoURL, nil)
		wabaInfoReq.Header.Set("Authorization", "Bearer "+longLivedToken)

		wabaInfoResp, err := client.Do(wabaInfoReq)
		if err == nil {
			var wabaObj struct {
				ID                 string `json:"id"`
				Name               string `json:"name"`
				VerifiedName       string `json:"verified_name"`
				DisplayPhoneNumber string `json:"display_phone_number"`
			}
			if json.NewDecoder(wabaInfoResp.Body).Decode(&wabaObj) == nil && wabaObj.ID != "" {
				if wabaObj.VerifiedName != "" {
					verifiedName = wabaObj.VerifiedName
				} else if wabaObj.Name != "" {
					verifiedName = wabaObj.Name
				}
				if wabaObj.DisplayPhoneNumber != "" {
					displayPhone = wabaObj.DisplayPhoneNumber
				}
			}
			wabaInfoResp.Body.Close()
		}
	} else {
		// Method 2 (Fallback): Query /me/businesses for accounts with business_management permission
		businessURL := "https://graph.facebook.com/v21.0/me/businesses?fields=id,name"
		businessReq, _ := http.NewRequest("GET", businessURL, nil)
		businessReq.Header.Set("Authorization", "Bearer "+longLivedToken)
		businessResp, err := client.Do(businessReq)
		if err == nil {
			var businessResult struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.NewDecoder(businessResp.Body).Decode(&businessResult) == nil {
				for _, business := range businessResult.Data {
					for _, edge := range []string{"owned_whatsapp_business_accounts", "client_whatsapp_business_accounts"} {
						wabaSearchURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/%s?fields=id,name", business.ID, edge)
						wabaReq, _ := http.NewRequest("GET", wabaSearchURL, nil)
						wabaReq.Header.Set("Authorization", "Bearer "+longLivedToken)

						wabaResp, requestErr := client.Do(wabaReq)
						if requestErr != nil {
							continue
						}

						var wabaResult struct {
							Data []struct {
								ID   string `json:"id"`
								Name string `json:"name"`
							} `json:"data"`
						}
						_ = json.NewDecoder(wabaResp.Body).Decode(&wabaResult)
						wabaResp.Body.Close()

						if len(wabaResult.Data) > 0 {
							wabaID = wabaResult.Data[0].ID
							if wabaResult.Data[0].Name != "" {
								verifiedName = wabaResult.Data[0].Name
							}
							break
						}
					}
					if wabaID != "" {
						break
					}
				}
			}
			businessResp.Body.Close()
		}

		if wabaID == "" {
			utils.WriteError(w, http.StatusNotFound, "No WhatsApp Business Account found for the connected Meta account. Ensure the Embedded Signup completed and a WhatsApp Business profile was selected.")
			return
		}
	}

	// Step 3: Get or verify the Phone Number ID
	phoneNumberID := req.PhoneNumberID

	if phoneNumberID != "" {
		phoneInfoURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s?fields=id,verified_name,display_phone_number", phoneNumberID)
		phoneInfoReq, _ := http.NewRequest("GET", phoneInfoURL, nil)
		phoneInfoReq.Header.Set("Authorization", "Bearer "+longLivedToken)

		phoneInfoResp, err := client.Do(phoneInfoReq)
		if err == nil {
			var phoneObj struct {
				ID                 string `json:"id"`
				VerifiedName       string `json:"verified_name"`
				DisplayPhoneNumber string `json:"display_phone_number"`
			}
			if json.NewDecoder(phoneInfoResp.Body).Decode(&phoneObj) == nil && phoneObj.ID != "" {
				if phoneObj.VerifiedName != "" {
					verifiedName = phoneObj.VerifiedName
				}
				if phoneObj.DisplayPhoneNumber != "" {
					displayPhone = phoneObj.DisplayPhoneNumber
				}
			}
			phoneInfoResp.Body.Close()
		}
	} else {
		phoneURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/phone_numbers?fields=id,verified_name,display_phone_number", wabaID)
		phoneReq, _ := http.NewRequest("GET", phoneURL, nil)
		phoneReq.Header.Set("Authorization", "Bearer "+longLivedToken)

		phoneResp, err := client.Do(phoneReq)
		if err == nil {
			var phoneResult struct {
				Data []struct {
					ID                 string `json:"id"`
					VerifiedName       string `json:"verified_name"`
					DisplayPhoneNumber string `json:"display_phone_number"`
				} `json:"data"`
			}
			json.NewDecoder(phoneResp.Body).Decode(&phoneResult)
			phoneResp.Body.Close()

			if len(phoneResult.Data) > 0 {
				phoneNumberID = phoneResult.Data[0].ID
				if phoneResult.Data[0].VerifiedName != "" {
					verifiedName = phoneResult.Data[0].VerifiedName
				}
				if phoneResult.Data[0].DisplayPhoneNumber != "" {
					displayPhone = phoneResult.Data[0].DisplayPhoneNumber
				}
			}
		}
	}

	if phoneNumberID == "" {
		phoneNumberID = wabaID
	}

	// Step 4: Auto-subscribe OmniPulse App to this WABA's webhooks (zero-touch vendor experience)
	subURL := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/subscribed_apps", wabaID)
	subReq, _ := http.NewRequest("POST", subURL, nil)
	subReq.Header.Set("Authorization", "Bearer "+longLivedToken)
	subResp, subErr := client.Do(subReq)
	if subErr == nil {
		log.Printf("[WhatsApp-OAuth] Webhook auto-subscribed for WABA %s (Status: %d)\n", wabaID, subResp.StatusCode)
		subResp.Body.Close()
	} else {
		log.Printf("[WhatsApp-OAuth-Warning] Could not auto-subscribe webhooks for WABA %s: %v\n", wabaID, subErr)
	}

	// Step 5: Save the WhatsApp channel
	creds := map[string]string{
		"access_token":    longLivedToken,
		"phone_number_id": phoneNumberID,
		"waba_id":         wabaID,
		"verify_token":    "omnipulse_webhook_" + tenantID,
	}
	credsJSON, _ := json.Marshal(creds)

	senderIdentity := fmt.Sprintf("%s (%s)", verifiedName, displayPhone)
	if displayPhone == "" {
		senderIdentity = verifiedName
	}

	channel := &domain.TenantChannel{
		TenantID:             tenantID,
		PlatformName:         "whatsapp",
		SenderIdentity:       senderIdentity,
		EncryptedCredentials: credsJSON,
		Status:               "active",
	}

	if err := h.repo.CreateChannel(r.Context(), channel); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to save WhatsApp channel")
		return
	}

	log.Printf("[WhatsApp-OAuth] Channel connected for tenant %s: %s (WABA: %s, PhoneID: %s)\n",
		tenantID, senderIdentity, wabaID, phoneNumberID)

	utils.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"channel":         channel,
		"waba_id":         wabaID,
		"phone_number_id": phoneNumberID,
		"sender_identity": senderIdentity,
	})
}
