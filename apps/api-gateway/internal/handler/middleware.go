package handler

import (
	"context"
	"net/http"
	"strings"

	"omnipulse/apps/api-gateway/internal/utils"
)

// Define an unexported type for context keys to guarantee zero collision space
type contextKey string

const (
	TenantIDKey contextKey = "tenant_id"
	UserIDKey   contextKey = "user_id"
)

// AuthMiddleware intercepts raw HTTP streams to validate credentials
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Extract the Authorization header frame
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			utils.WriteError(w, http.StatusUnauthorized, "Missing required Authorization identity credentials")
			return
		}

		// 2. Parse the Bearer token scheme format
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			utils.WriteError(w, http.StatusUnauthorized, "Invalid authorization scheme formatting. Use 'Bearer <token>'")
			return
		}

		token := parts[1]

		// 3. Simulated Cryptographic Identity Verification
		// In the next evolution, this line will parse a JWT token or match an API key in Postgres.
		// For our testing loop, we will establish two mock client tenant keys:
		var tenantID, userID string

		switch token {
		case "omni_proto_token_xyz123":
			tenantID = "tenant-enterprise-acme-corp"
			userID = "user-senior-admin-alice"
		case "omni_test_token_abc789":
			tenantID = "tenant-smb-boutique-shop"
			userID = "user-manager-bob"
		default:
			utils.WriteError(w, http.StatusUnauthorized, "Provided identity token is expired or unauthorized")
			return
		}

		// 4. Inject the proven identity data cleanly downstream into the request lifetime context
		ctx := context.WithValue(r.Context(), TenantIDKey, tenantID)
		ctx = context.WithValue(ctx, UserIDKey, userID)

		// 5. Pass the structurally updated request envelope down to the next handler stage
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
