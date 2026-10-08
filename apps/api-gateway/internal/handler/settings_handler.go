package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/utils"
)

type SettingsHandler struct{ db *sql.DB }

func NewSettingsHandler(db *sql.DB) *SettingsHandler { return &SettingsHandler{db: db} }

type workspaceSettings struct {
	LogoURL  string `json:"logo_url"`
	Timezone string `json:"timezone"`
	Language string `json:"language"`
}

func (h *SettingsHandler) GetWorkspaceSettings(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	var s workspaceSettings
	err := h.db.QueryRowContext(r.Context(), `SELECT logo_url, timezone, language FROM tenant_settings WHERE tenant_id=$1`, tenantID).Scan(&s.LogoURL, &s.Timezone, &s.Language)
	if err == sql.ErrNoRows {
		utils.WriteJSON(w, http.StatusOK, workspaceSettings{Timezone: "UTC", Language: "en-US"})
		return
	}
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load workspace settings")
		return
	}
	utils.WriteJSON(w, http.StatusOK, s)
}
func (h *SettingsHandler) UpdateWorkspaceSettings(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	var payload workspaceSettings
	if json.NewDecoder(r.Body).Decode(&payload) != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid workspace settings payload")
		return
	}

	// Fetch existing settings first so partial updates don't overwrite other fields
	var existing workspaceSettings
	err := h.db.QueryRowContext(r.Context(), `SELECT logo_url, timezone, language FROM tenant_settings WHERE tenant_id=$1`, tenantID).Scan(&existing.LogoURL, &existing.Timezone, &existing.Language)
	if err == nil {
		if payload.LogoURL == "" {
			payload.LogoURL = existing.LogoURL
		}
		if payload.Timezone == "" {
			payload.Timezone = existing.Timezone
		}
		if payload.Language == "" {
			payload.Language = existing.Language
		}
	} else {
		if payload.Timezone == "" {
			payload.Timezone = "UTC"
		}
		if payload.Language == "" {
			payload.Language = "en-US"
		}
	}

	_, err = h.db.ExecContext(r.Context(), `INSERT INTO tenant_settings(tenant_id,logo_url,timezone,language) VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id) DO UPDATE SET logo_url=EXCLUDED.logo_url,timezone=EXCLUDED.timezone,language=EXCLUDED.language,updated_at=NOW()`, tenantID, payload.LogoURL, payload.Timezone, payload.Language)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to save workspace settings")
		return
	}
	utils.WriteJSON(w, http.StatusOK, payload)
}

type profilePayload struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
}

func (h *SettingsHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing user context")
		return
	}
	var p profilePayload
	if err := h.db.QueryRowContext(r.Context(), `SELECT COALESCE(first_name,''), COALESCE(last_name,''), email FROM users WHERE id=$1`, userID).Scan(&p.FirstName, &p.LastName, &p.Email); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to fetch profile")
		return
	}
	utils.WriteJSON(w, http.StatusOK, p)
}
func (h *SettingsHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(UserIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing user context")
		return
	}
	var p profilePayload
	if json.NewDecoder(r.Body).Decode(&p) != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid profile payload")
		return
	}
	p.FirstName = strings.TrimSpace(p.FirstName)
	p.LastName = strings.TrimSpace(p.LastName)
	p.Email = strings.TrimSpace(p.Email)
	if p.Email == "" {
		utils.WriteError(w, http.StatusBadRequest, "Email is required")
		return
	}
	_, err := h.db.ExecContext(r.Context(), `UPDATE users SET first_name=$1,last_name=$2,email=$3,updated_at=NOW() WHERE id=$4`, p.FirstName, p.LastName, p.Email, userID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to update profile")
		return
	}
	utils.WriteJSON(w, http.StatusOK, p)
}

type preference struct {
	Key   string `json:"key"`
	Email bool   `json:"email"`
	InApp bool   `json:"in_app"`
}

func (h *SettingsHandler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	h.writePreferences(w, r, http.MethodGet)
}
func (h *SettingsHandler) UpdatePreferences(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	var body struct {
		Preferences []preference `json:"preferences"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid preferences payload")
		return
	}
	raw, _ := json.Marshal(body.Preferences)
	if _, err := h.db.ExecContext(r.Context(), `INSERT INTO tenant_settings(tenant_id,notification_preferences) VALUES($1,$2) ON CONFLICT(tenant_id) DO UPDATE SET notification_preferences=EXCLUDED.notification_preferences,updated_at=NOW()`, tenantID, raw); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to save notification preferences")
		return
	}
	utils.WriteJSON(w, http.StatusOK, body.Preferences)
}
func (h *SettingsHandler) writePreferences(w http.ResponseWriter, r *http.Request, _ string) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}
	var raw []byte
	err := h.db.QueryRowContext(r.Context(), `SELECT notification_preferences FROM tenant_settings WHERE tenant_id=$1`, tenantID).Scan(&raw)
	if err == sql.ErrNoRows {
		utils.WriteJSON(w, http.StatusOK, []preference{})
		return
	}
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load notification preferences")
		return
	}
	var p []preference
	if json.Unmarshal(raw, &p) != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Invalid stored notification preferences")
		return
	}
	utils.WriteJSON(w, http.StatusOK, p)
}

type apiKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func (h *SettingsHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, 401, "Missing tenant context")
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT id,name,prefix,created_at,last_used_at FROM api_keys WHERE tenant_id=$1 AND revoked_at IS NULL ORDER BY created_at DESC`, tenantID)
	if err != nil {
		utils.WriteError(w, 500, "Failed to list API keys")
		return
	}
	defer rows.Close()
	out := []apiKey{}
	for rows.Next() {
		var k apiKey
		if rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt) == nil {
			out = append(out, k)
		}
	}
	utils.WriteJSON(w, 200, out)
}
func (h *SettingsHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, 401, "Missing tenant context")
		return
	}
	var b struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || strings.TrimSpace(b.Name) == "" {
		utils.WriteError(w, 400, "A key name is required")
		return
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		utils.WriteError(w, 500, "Failed to generate API key")
		return
	}
	secret := "op_live_" + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(secret))
	prefix := secret[:14]
	var id string
	var created time.Time
	err := h.db.QueryRowContext(r.Context(), `INSERT INTO api_keys(tenant_id,name,prefix,key_hash) VALUES($1,$2,$3,$4) RETURNING id,created_at`, tenantID, strings.TrimSpace(b.Name), prefix, hex.EncodeToString(sum[:])).Scan(&id, &created)
	if err != nil {
		utils.WriteError(w, 500, "Failed to create API key")
		return
	}
	utils.WriteJSON(w, 201, map[string]any{"id": id, "name": b.Name, "prefix": prefix, "created_at": created, "key": secret})
}
func (h *SettingsHandler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, 401, "Missing tenant context")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE api_keys SET revoked_at=NOW() WHERE id=$1 AND tenant_id=$2 AND revoked_at IS NULL`, r.PathValue("id"), tenantID); err != nil {
		utils.WriteError(w, 500, "Failed to revoke API key")
		return
	}
	utils.WriteJSON(w, 200, map[string]string{"message": "API key revoked"})
}
