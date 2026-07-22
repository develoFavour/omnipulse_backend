package handler

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"

	"github.com/clerk/clerk-sdk-go/v2/jwt"
)

// Define an unexported type for context keys to guarantee zero collision space
type contextKey string

const (
	TenantIDKey contextKey = "tenant_id"
	UserIDKey   contextKey = "user_id"
)

type statusResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rw *statusResponseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *statusResponseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// RequestLoggerMiddleware logs Gin/Fiber style HTTP requests with method, path, status, and duration
func RequestLoggerMiddleware(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			srw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(srw, r)

			duration := time.Since(start)
			logger.Printf("%s %s | %d | %v | %s\n",
				r.Method,
				r.URL.Path,
				srw.status,
				duration,
				r.RemoteAddr,
			)
		})
	}
}

// AuthMiddleware intercepts raw HTTP streams to validate credentials
func AuthMiddleware(identityUC *usecase.IdentityUseCase) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bypass health check and external webhooks
			if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/api/v1/webhooks/") {
				next.ServeHTTP(w, r)
				return
			}

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

			// 3. Cryptographic Identity Verification via Clerk
			claims, err := jwt.Verify(r.Context(), &jwt.VerifyParams{
				Token: token,
			})
			if err != nil {
				utils.WriteError(w, http.StatusUnauthorized, "Provided identity token is expired or unauthorized")
				return
			}

			// 4. JIT Provisioning & Tenant Resolution
			clerkUserID := claims.Subject
			syncRes, err := identityUC.SyncUser(r.Context(), clerkUserID, clerkUserID+"@placeholder.com")
			if err != nil {
				utils.WriteError(w, http.StatusInternalServerError, "Failed to resolve tenant workspace context")
				return
			}

			// 5. Inject the proven identity data cleanly downstream into the request lifetime context
			ctx := context.WithValue(r.Context(), TenantIDKey, syncRes.Tenant.ID)
			ctx = context.WithValue(ctx, UserIDKey, syncRes.User.ID)

			// 6. Pass the structurally updated request envelope down to the next handler stage
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
