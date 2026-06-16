// Package httpx provides shared HTTP server building blocks: JSON
// responses, the standard error envelope, pagination, and middleware.
package httpx

import "net/http"

// ErrorCode is a machine-readable error code (mirrors OpenAPI ErrorCode).
type ErrorCode string

// Machine-readable error codes (mirrors OpenAPI ErrorCode).
const (
	CodeValidation   ErrorCode = "validation_error"
	CodeUnauthorized ErrorCode = "unauthorized"
	CodeForbidden    ErrorCode = "forbidden"
	CodeNotFound     ErrorCode = "not_found"
	CodeConflict     ErrorCode = "conflict"
	CodeInternal     ErrorCode = "internal"
)

// ErrorDetail is an optional per-field validation message.
type ErrorDetail struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// ErrorBody is the inner error object.
type ErrorBody struct {
	Code    ErrorCode     `json:"code"`
	Message string        `json:"message"`
	Details []ErrorDetail `json:"details,omitempty"`
}

// ErrorResponse is the envelope: {"error": {...}}.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// WriteError writes the standard error envelope.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, msg string, details ...ErrorDetail) {
	WriteJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: msg, Details: details}})
}

// NotFound writes a 404 not_found error envelope.
func NotFound(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusNotFound, CodeNotFound, msg)
}

// Internal writes a 500 internal error envelope.
func Internal(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusInternalServerError, CodeInternal, msg)
}

// Unauthorized writes a 401 unauthorized error envelope.
func Unauthorized(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusUnauthorized, CodeUnauthorized, msg)
}
