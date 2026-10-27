// Package openai implements the OpenAI-compatible endpoints.
package openai

import (
	"encoding/json"
	"net/http"
)

type apiError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

func errorFor(status int, msg string) apiError {
	e := apiError{Message: msg, Type: "invalid_request_error"}
	code := func(c string) { e.Code = &c }
	switch {
	case status == 401:
		code("invalid_api_key")
	case status == 403:
		e.Type = "permission_error"
	case status == 413:
		code("request_too_large")
	case status == 429:
		e.Type = "requests"
		code("rate_limit_exceeded")
	case status >= 500:
		e.Type = "server_error"
	}
	return e
}

// WriteError writes an OpenAI-format error response.
func WriteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]apiError{"error": errorFor(status, msg)})
}
