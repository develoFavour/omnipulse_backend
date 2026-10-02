package utils

import (
	"encoding/json"
	"log"
	"net/http"
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorEnvelope{
		Success: false,
		Error:   message,
		Code:    code,
	})
}
