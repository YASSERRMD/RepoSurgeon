package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Error is the canonical API error. Every failure response uses this shape so
// clients can branch on code instead of parsing prose.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Common errors.
var (
	ErrNotFound         = &Error{Status: http.StatusNotFound, Code: "not_found", Message: "resource not found"}
	ErrMethodNotAllowed = &Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "method not allowed for this resource"}
	ErrInternal         = &Error{Status: http.StatusInternalServerError, Code: "internal_error", Message: "internal server error"}
	ErrBadRequest       = &Error{Status: http.StatusBadRequest, Code: "bad_request", Message: "request could not be processed"}
)

// errorBody is the wire envelope for failures.
type errorBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Details   any    `json:"details,omitempty"`
	} `json:"error"`
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// WriteError writes the canonical error envelope, echoing the request ID for
// traceability. Library errors are reduced to a safe message; details are only
// included when explicitly supplied.
func WriteError(w http.ResponseWriter, r *http.Request, apiErr *Error, details any) {
	if apiErr == nil {
		apiErr = ErrInternal
	}
	var body errorBody
	body.Error.Code = apiErr.Code
	message := apiErr.Message
	if apiErr.Status >= http.StatusInternalServerError {
		// Never leak internal failure detail to callers; log it instead.
		message = apiErr.Message
		if details != nil {
			if logger, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok && logger != nil {
				logger.Error("request failed", "code", apiErr.Code, "request_id", RequestID(r.Context()), "detail", details)
			}
		}
		details = nil
	}
	body.Error.Message = message
	body.Error.RequestID = RequestID(r.Context())
	body.Error.Details = details
	WriteJSON(w, apiErr.Status, body)
}
