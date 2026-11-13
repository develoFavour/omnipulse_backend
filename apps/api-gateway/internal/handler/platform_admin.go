package handler

import (
	"log"
	"net/http"
	"strings"

	"omnipulse/apps/api-gateway/internal/utils"
)

// PlatformAdminGate admits only the Clerk user IDs on PLATFORM_ADMIN_USER_IDS.
// This is a separate axis from workspace roles: owning one workspace must never
// grant a cross-tenant view, so nothing here reads UserRoleKey.
type PlatformAdminGate struct {
	allowed map[string]struct{}
}

func NewPlatformAdminGate(userIDs []string) *PlatformAdminGate {
	allowed := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	return &PlatformAdminGate{allowed: allowed}
}

// Configured reports how many identities can reach the admin surface, for boot logging.
func (g *PlatformAdminGate) Configured() int {
	return len(g.allowed)
}

func (g *PlatformAdminGate) Allows(userID string) bool {
	if userID == "" {
		return false
	}
	_, ok := g.allowed[userID]
	return ok
}

// RequirePlatformAdmin rejects anyone off the allowlist and logs every decision,
// including denials, so admin access stays attributable.
func (g *PlatformAdminGate) RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(UserIDKey).(string)
		if !g.Allows(userID) {
			log.Printf("[PLATFORM-ADMIN] denied user=%q %s %s from %s\n", userID, r.Method, r.URL.Path, r.RemoteAddr)
			utils.WriteError(w, http.StatusForbidden, "Access forbidden: platform admin access required")
			return
		}
		log.Printf("[PLATFORM-ADMIN] access user=%s %s %s?%s\n", userID, r.Method, r.URL.Path, r.URL.RawQuery)
		next.ServeHTTP(w, r)
	})
}

// Me lets the app decide whether to show the admin navigation. Reaching this
// handler at all proves the allowlist admitted the caller.
func (g *PlatformAdminGate) Me(w http.ResponseWriter, r *http.Request) {
	utils.WriteJSON(w, http.StatusOK, map[string]bool{"platform_admin": true})
}
