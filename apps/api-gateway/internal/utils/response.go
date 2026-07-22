package utils

import (
	"encoding/json"
	"log"
	"net/http"
)

// JSONEnvelope defines the standardized JSON structure for all successful responses
type JSONEnvelope struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
}

// ErrorEnvelope defines the standardized JSON structure for all API anomalies
type ErrorEnvelope struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// WriteJSON sends a standardized 200-range success payload down the wire
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(JSONEnvelope{
		Success: true,
		Data:    data,
	})
}

// WriteError intercepts application failures and formats them uniformly
func WriteError(w http.ResponseWriter, status int, message string) {
	log.Printf("[HTTP ERROR] %d - %s\n", status, message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(ErrorEnvelope{
		Success: false,
		Error:   message,
	})
}
