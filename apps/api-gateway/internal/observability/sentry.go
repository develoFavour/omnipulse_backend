package observability

import (
	"net/http"
	"regexp"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

// Recipient routing values are phone numbers or Telegram chat IDs. They reach
// Sentry through error strings, so they are redacted rather than sent.
var (
	phonePattern = regexp.MustCompile(`\+?\d{9,15}`)
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	tokenPath    = regexp.MustCompile(`/(review|invitations)/[^/?#]+`)
)

// InitSentry enables error reporting only when a DSN is configured, so local
// and self-hosted runs stay offline and do not need a Sentry account.
func InitSentry(dsn, environment, release string) (bool, error) {
	if dsn == "" {
		return false, nil
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:            dsn,
		Environment:    environment,
		Release:        release,
		SendDefaultPII: false,
		BeforeSend:     scrubEvent,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// Flush drains buffered events before the process exits.
func Flush() {
	sentry.Flush(2 * time.Second)
}

// Middleware reports panics and capture-worthy errors from every route.
func Middleware(next http.Handler) http.Handler {
	return sentryhttp.New(sentryhttp.Options{Repanic: false}).Handle(next)
}

func scrubText(value string) string {
	value = tokenPath.ReplaceAllString(value, "/$1/[redacted]")
	value = emailPattern.ReplaceAllString(value, "[email]")
	value = phonePattern.ReplaceAllString(value, "[recipient]")
	return value
}

func scrubEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event == nil {
		return nil
	}
	event.Message = scrubText(event.Message)
	for i := range event.Exception {
		event.Exception[i].Value = scrubText(event.Exception[i].Value)
	}
	for _, breadcrumb := range event.Breadcrumbs {
		if breadcrumb == nil {
			continue
		}
		breadcrumb.Message = scrubText(breadcrumb.Message)
		for key, value := range breadcrumb.Data {
			if text, ok := value.(string); ok {
				breadcrumb.Data[key] = scrubText(text)
			}
		}
	}

	// Request payloads can carry message copy, consent notes, and credentials.
	if req := event.Request; req != nil {
		req.Data = ""
		req.Cookies = ""
		req.Env = nil
		req.QueryString = ""
		req.URL = scrubText(req.URL)
		for _, header := range []string{
			"Authorization", "Cookie", "X-Api-Key",
			"X-Telegram-Bot-Api-Secret-Token", "X-Hub-Signature-256",
		} {
			delete(req.Headers, header)
		}
	}

	event.User.Email = ""
	event.User.IPAddress = ""
	event.User.Username = ""
	return event
}
