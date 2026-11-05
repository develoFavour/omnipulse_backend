package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rateLimitRule targets one class of traffic. Rules are evaluated in order, so
// a narrow rule must be registered before the broader prefix it falls under.
type rateLimitRule struct {
	class  string
	match  func(method, path string) bool
	limit  int
	window time.Duration
}

func hasPrefix(path, prefix string) bool { return strings.HasPrefix(path, prefix) }
func hasSuffix(path, suffix string) bool { return strings.HasSuffix(path, suffix) }

// defaultRateLimitRules caps unauthenticated and expensive endpoints. Webhook
// paths are deliberately absent: providers retry in bursts and a 429 there
// turns into duplicate or dropped events.
var defaultRateLimitRules = []rateLimitRule{
	{
		class: "review-decision",
		match: func(m, p string) bool {
			return m == http.MethodPost && hasPrefix(p, "/api/v1/public/reviews/") && hasSuffix(p, "/decision")
		},
		limit:  10,
		window: time.Minute,
	},
	{
		class: "review-outcome-csv",
		match: func(m, p string) bool {
			return m == http.MethodGet && hasPrefix(p, "/api/v1/public/reviews/") && hasSuffix(p, "/outcome.csv")
		},
		limit:  10,
		window: time.Minute,
	},
	{
		class:  "review-read",
		match:  func(m, p string) bool { return m == http.MethodGet && hasPrefix(p, "/api/v1/public/reviews/") },
		limit:  60,
		window: time.Minute,
	},
	{
		class:  "invitation-accept",
		match:  func(m, p string) bool { return m == http.MethodPost && p == "/api/v1/invitations/accept" },
		limit:  10,
		window: time.Minute,
	},
	{
		class:  "invitation-preview",
		match:  func(m, p string) bool { return m == http.MethodGet && hasPrefix(p, "/api/v1/invitations/preview") },
		limit:  30,
		window: time.Minute,
	},
	{
		class:  "auth-sync",
		match:  func(m, p string) bool { return m == http.MethodPost && p == "/api/v1/auth/sync" },
		limit:  60,
		window: time.Minute,
	},
	{
		class:  "contact-import",
		match:  func(m, p string) bool { return m == http.MethodPost && p == "/api/v1/contacts/import" },
		limit:  10,
		window: time.Minute,
	},
	{
		class: "export",
		match: func(m, p string) bool {
			return m == http.MethodGet && (hasSuffix(p, "/export") || hasSuffix(p, "/consent-export"))
		},
		limit:  10,
		window: time.Minute,
	},
}

type rateLimitBucket struct {
	count   int
	resetAt time.Time
}

// IPRateLimiter is an in-process fixed-window limiter. It damps abuse per
// instance rather than enforcing a global quota; that is enough to stop token
// guessing and accidental import loops without adding shared infrastructure.
type IPRateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*rateLimitBucket
	rules     []rateLimitRule
	now       func() time.Time
	lastSweep time.Time
	maxKeys   int
}

const rateLimitMaxKeys = 20000

func NewIPRateLimiter() *IPRateLimiter {
	return newIPRateLimiter(time.Now)
}

func newIPRateLimiter(now func() time.Time) *IPRateLimiter {
	return &IPRateLimiter{
		buckets:   make(map[string]*rateLimitBucket),
		rules:     defaultRateLimitRules,
		now:       now,
		lastSweep: now(),
		maxKeys:   rateLimitMaxKeys,
	}
}

func (l *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rule, ok := l.ruleFor(r.Method, r.URL.Path)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		allowed, retryAfter := l.allow(rule.class+"|"+clientIP(r), rule)
		if allowed {
			next.ServeHTTP(w, r)
			return
		}

		retrySeconds := int(math.Ceil(retryAfter.Seconds()))
		if retrySeconds < 1 {
			retrySeconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Too many requests. Please wait a moment and try again.",
			"code":    "rate_limited",
		})
	})
}

func (l *IPRateLimiter) ruleFor(method, path string) (rateLimitRule, bool) {
	for _, rule := range l.rules {
		if rule.match(method, path) {
			return rule, true
		}
	}
	return rateLimitRule{}, false
}

func (l *IPRateLimiter) allow(key string, rule rateLimitRule) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now, rule.window)

	bucket, exists := l.buckets[key]
	if exists && !now.Before(bucket.resetAt) {
		bucket.count = 0
		bucket.resetAt = now.Add(rule.window)
	}

	if !exists {
		// Fail open at capacity rather than let the limiter's own memory be
		// the denial-of-service vector.
		if len(l.buckets) >= l.maxKeys {
			return true, 0
		}
		bucket = &rateLimitBucket{resetAt: now.Add(rule.window)}
		l.buckets[key] = bucket
	}

	if bucket.count >= rule.limit {
		return false, bucket.resetAt.Sub(now)
	}

	bucket.count++
	return true, 0
}

// sweepLocked drops expired windows so the map stays bounded under many IPs.
func (l *IPRateLimiter) sweepLocked(now time.Time, window time.Duration) {
	if now.Sub(l.lastSweep) < window {
		return
	}
	for key, bucket := range l.buckets {
		if !now.Before(bucket.resetAt) {
			delete(l.buckets, key)
		}
	}
	l.lastSweep = now
}
