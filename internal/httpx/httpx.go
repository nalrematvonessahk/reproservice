// Package httpx holds small HTTP helpers shared by the demo services, proxy and controller.
package httpx

import (
	"encoding/json"
	"log"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpx: encode response: %v", err)
	}
}

type ErrorBody struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	Field         string `json:"field,omitempty"`
	CorrelationID string `json:"correlation_id"`
}

func WriteError(w http.ResponseWriter, status int, code, message, correlationID string) {
	WriteJSON(w, status, ErrorBody{Code: code, Message: message, CorrelationID: correlationID})
}

func DecodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
