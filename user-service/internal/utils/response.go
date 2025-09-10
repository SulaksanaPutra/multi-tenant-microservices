package utils

import (
	"encoding/json"
	"net/http"
)

type StandardResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func WriteJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func WriteSuccess(w http.ResponseWriter, statusCode int, message string, data interface{}) {
	WriteJSON(w, statusCode, StandardResponse{
		Status:  "success",
		Message: message,
		Data:    data,
	})
}

func WriteError(w http.ResponseWriter, statusCode int, message string) {
	WriteJSON(w, statusCode, ErrorResponse{Error: message})
}
