// Package ctxkeys defines shared request-scoped context keys that are used
// across multiple packages (handler, service, worker) without creating import cycles.
// All keys use an unexported type to guarantee no collision with third-party middleware.
package ctxkeys

// ContextKey is an unexported string type for context keys defined in this package.
type ContextKey string

const (
	TenantIDKey ContextKey = "tenant_id"
	UserIDKey   ContextKey = "user_id"
	UserRoleKey ContextKey = "user_role"
)
