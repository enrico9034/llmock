// Package anthropic implements the Anthropic-compatible endpoints.
package anthropic

import (
	"encoding/json"
	"net/http"
)

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type errorBody struct {
	Type  string   `json:"type"`
	Error apiError `json:"error"`
}

func errorFor(status int, msg string) errorBody {
	t := "invalid_request_error"
	switch {
	case status == 401:
		t = "authentication_error"
	case status == 403:
		t = "permission_error"
	case status == 404:
		t = "not_found_error"
	case status == 413:
		t = "request_too_large"
	case status == 429:
		t = "rate_limit_error"
	case status == 529 || status == 503:
		t = "overloaded_error"
	case status >= 500:
		t = "api_error"
	}
	return errorBody{"error", apiError{t, msg}}
}

// WriteError writes an Anthropic-format error response.
func WriteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorFor(status, msg))
}
