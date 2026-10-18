package utils

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// JSONEnvelope defines the standardized JSON structure for all successful responses:
//
//	{ "success": true, "data": <payload> }
type JSONEnvelope struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
}

// ErrorEnvelope defines the standardized JSON structure for all API errors:
//
//	{ "success": false, "error": "<human-readable message>", "code": "<machine-readable code>" }
//
// The Code field is optional — omit it for generic errors; set it when the client
// needs to branch on a specific condition (e.g. "invitation_expired", "already_member").
type ErrorEnvelope struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
}

// WriteJSON sends a standardized 2xx success payload.
//
//	utils.WriteJSON(w, http.StatusOK, myStruct)
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(JSONEnvelope{
		Success: true,
		Data:    data,
	})
}

// WriteNoContent sends a 204 No Content response (no body).
//
//	utils.WriteNoContent(w)
func WriteNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// WriteError formats an application failure uniformly and logs it.
// For simple cases pass an empty code ("").
//
//	utils.WriteError(w, http.StatusBadRequest, "invalid token")
//	utils.WriteError(w, http.StatusConflict, "already a member")  // code inferred from message
func WriteError(w http.ResponseWriter, status int, message string) {
	log.Printf("[HTTP ERROR] %d - %s\n", status, message)
	message = userFacingError(status, message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorEnvelope{
		Success: false,
		Error:   message,
	})
}

// WriteErrorWithCode is WriteError with an explicit machine-readable code.
// Use this when the frontend needs to distinguish error types programmatically.
//
//	utils.WriteErrorWithCode(w, http.StatusGone, "invitation_expired", "This invitation link has expired.")
func WriteErrorWithCode(w http.ResponseWriter, status int, code, message string) {
	log.Printf("[HTTP ERROR] %d [%s] - %s\n", status, code, message)
	message = userFacingError(status, message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorEnvelope{
		Success: false,
		Error:   message,
		Code:    code,
	})
}

// userFacingError keeps implementation details out of API responses while
// preserving useful validation and plan-limit messages. The original message
// is logged by WriteError/WriteErrorWithCode before it is sanitized.
func userFacingError(status int, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return friendlyStatusMessage(status)
	}

	technical := status >= http.StatusInternalServerError || containsTechnicalDetail(message) || startsWithHTTPStatus(message)
	if !technical {
		return message
	}
	return friendlyStatusMessage(status)
}

func containsTechnicalDetail(message string) bool {
	lower := strings.ToLower(message)
	markers := []string{
		"pq:", "sql:", "sqlstate", "database", "constraint", "deadlock",
		"connection refused", "connection reset", "context deadline", "context canceled",
		"no rows in result set", "failed to scan", "failed to query", "runtime error",
		"panic:", "stack trace", "unexpected eof", "json: cannot",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func startsWithHTTPStatus(message string) bool {
	if len(message) < 4 || message[3] != ' ' {
		return false
	}
	for _, r := range message[:3] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func friendlyStatusMessage(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "We couldn't process that request. Please check the information and try again."
	case http.StatusPaymentRequired:
		return "This feature requires a higher plan or more available quota."
	case http.StatusUnauthorized:
		return "Your session has expired. Please sign in again."
	case http.StatusForbidden:
		return "You don't have permission to perform this action."
	case http.StatusNotFound:
		return "We couldn't find what you were looking for."
	case http.StatusConflict:
		return "This action conflicts with the current information. Please refresh and try again."
	case http.StatusUnprocessableEntity:
		return "Some of the information needs attention. Please review it and try again."
	case http.StatusTooManyRequests:
		return "Too many requests were made. Please wait a moment and try again."
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "A connected service is temporarily unavailable. Please try again shortly."
	default:
		if status >= http.StatusInternalServerError {
			return "Something went wrong on our side. Please try again shortly."
		}
		return "We couldn't complete that request. Please try again."
	}
}
