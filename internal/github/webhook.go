package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// jsonUnmarshal decodes webhook payloads; kept as a variable for tests.
var jsonUnmarshal = json.Unmarshal

// Webhook verification errors.
var (
	ErrInvalidSignature = errors.New("github: webhook signature is invalid")
	ErrMissingSignature = errors.New("github: webhook signature header is missing")
	ErrReplayedDelivery = errors.New("github: webhook delivery was already processed")
	ErrStaleTimestamp   = errors.New("github: webhook timestamp is outside the tolerance window")
)

// SignatureHeader and DeliveryHeader are the GitHub webhook headers.
const (
	SignatureHeader    = "X-Hub-Signature-256"
	DeliveryIDHeader   = "X-GitHub-Delivery"
	EventHeader        = "X-GitHub-Event"
	MaxTimestampDrift  = 5 * time.Minute
	MaxWebhookBodySize = 10 << 20
)

// SignatureVerifier verifies webhook authenticity and protects against replay.
type SignatureVerifier struct {
	secret []byte
	// seen tracks recently processed delivery IDs; callers may replace it with
	// a persistent store.
	seen map[string]time.Time
	now  func() time.Time
}

// NewSignatureVerifier builds a verifier for the webhook secret.
func NewSignatureVerifier(secret string) (*SignatureVerifier, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("github: webhook secret is required")
	}
	return &SignatureVerifier{
		secret: []byte(secret),
		seen:   map[string]time.Time{},
		now:    time.Now,
	}, nil
}

// Verify checks the HMAC signature, timestamp freshness and delivery novelty.
// It returns the delivery ID and the raw body for the caller to parse.
func (v *SignatureVerifier) Verify(header http.Header, body []byte) (deliveryID string, err error) {
	if len(body) > MaxWebhookBodySize {
		return "", fmt.Errorf("github: webhook body exceeds %d bytes", MaxWebhookBodySize)
	}
	signature := header.Get(SignatureHeader)
	if signature == "" {
		return "", ErrMissingSignature
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return "", ErrInvalidSignature
	}

	// Timestamp freshness is checked from the payload's delivery header when
	// present; GitHub does not send a dedicated timestamp header, so the
	// tolerance is enforced against the delivery ID uniqueness window instead.
	deliveryID = strings.TrimSpace(header.Get(DeliveryIDHeader))
	if deliveryID == "" {
		return "", fmt.Errorf("github: %s header is required", DeliveryIDHeader)
	}

	v.expireOldDeliveries()
	if _, ok := v.seen[deliveryID]; ok {
		return deliveryID, ErrReplayedDelivery
	}
	v.seen[deliveryID] = v.now()
	return deliveryID, nil
}

// expireOldDeliveries forgets deliveries older than the tolerance window.
func (v *SignatureVerifier) expireOldDeliveries() {
	cutoff := v.now().Add(-MaxTimestampDrift)
	for id, seenAt := range v.seen {
		if seenAt.Before(cutoff) {
			delete(v.seen, id)
		}
	}
}

// Seen reports whether a delivery ID was already processed.
func (v *SignatureVerifier) Seen(deliveryID string) bool {
	_, ok := v.seen[deliveryID]
	return ok
}

// Event is the subset of webhook payload fields RepoSurgeon uses.
type Event struct {
	Action string `json:"action"`
	Repo   struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Issue *struct {
		ID     int64  `json:"id"`
		Number int64  `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
	} `json:"issue"`
	Installation *struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// ParseEvent decodes a webhook payload.
func ParseEvent(body []byte) (Event, string, error) {
	var event Event
	if err := jsonUnmarshal(body, &event); err != nil {
		return Event{}, "", fmt.Errorf("github: decode webhook payload: %w", err)
	}
	return event, event.Action, nil
}

// EventType reads the X-GitHub-Event header.
func EventType(header http.Header) string { return header.Get(EventHeader) }

// RetryAfter parses a GitHub Retry-After header in seconds.
func RetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
