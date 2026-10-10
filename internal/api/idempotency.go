package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// idempotencyTTL bounds how long a recorded response stays replayable.
const idempotencyTTL = 24 * time.Hour

// maxReplayBody caps a recorded response body.
const maxReplayBody = 64 * 1024

// idempotent executes handler once per (key, request fingerprint) and records
// the outcome. A repeated key with the same body replays the stored response;
// a repeated key with a different body is rejected.
func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, endpoint string, handler func(subject Subject) (int, any)) {
	subject, ok := requireSubject(w, r)
	if !ok {
		return
	}
	key, ok := idempotencyKeyFor(w, r)
	if !ok {
		return
	}
	body, err := readBody(r)
	if err != nil {
		WriteError(w, r, &Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: "request body could not be read"}, nil)
		return
	}
	fingerprint := requestFingerprint(body)

	status, recorded, found, err := s.services.Idempotency.Get(r.Context(), key)
	if err != nil {
		s.log.Error("idempotency lookup failed", "endpoint", endpoint, "err", err)
		WriteError(w, r, ErrInternal, nil)
		return
	}
	if found {
		if recordedFingerprint, _ := recorded["fingerprint"].(string); recordedFingerprint != fingerprint {
			WriteError(w, r, &Error{
				Status:  http.StatusConflict,
				Code:    "idempotency_key_reuse",
				Message: "Idempotency-Key was already used with a different request body",
			}, nil)
			return
		}
		s.log.Debug("idempotent replay", "endpoint", endpoint, "key", key)
		if body, ok := recorded["body"]; ok {
			WriteJSON(w, status, body)
			return
		}
		WriteJSON(w, status, recorded)
		return
	}

	status, payload := handler(subject)
	if status < 200 || status >= 300 {
		// Failure responses are not recorded: a client retry may succeed once
		// the invalid input is fixed.
		return
	}
	record := map[string]any{"status": status, "body": payload, "fingerprint": fingerprint}
	if err := s.services.Idempotency.Store(r.Context(), key, subject.OrganizationID, endpoint, fingerprint, status, record, idempotencyTTL); err != nil {
		// The operation already succeeded; a recording failure is logged and
		// the client still receives its result.
		s.log.Error("idempotency record failed", "endpoint", endpoint, "err", err)
	}
	WriteJSON(w, status, payload)
}

// readBody buffers the request body so it can be fingerprinted and decoded.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	limited := io.LimitReader(r.Body, maxReplayBody)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Restore the body for handlers that decode it afterwards.
	r.Body = io.NopCloser(strings.NewReader(string(data)))
	return data, nil
}

// decodeInto decodes one JSON object into out, rejecting unknown fields. It
// runs after readBody has restored the body, so it can decode directly.
func decodeInto(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		WriteError(w, r, &Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: "request body must be a JSON object"}, nil)
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		message := "request body is not a valid JSON object"
		if errors.Is(err, io.EOF) {
			message = "request body must be a JSON object"
		}
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: message,
			Details: map[string]any{"fields": map[string]string{"body": "invalid JSON"}},
		}, nil)
		return false
	}
	return true
}
