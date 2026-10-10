// Package redact removes credential material from strings before they are
// written to logs, error details or persisted events.
package redact

import (
	"regexp"
	"strings"
)

// Markers and key names.
const marker = "<redacted>"

// urlUserinfo rewrites scheme://user:password@host into scheme://<redacted>@host.
var urlUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

// keyValuePair matches "sensitive-name<separator>value" while keeping the
// name, so redaction stays readable.
var keyValuePair = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|session[_-]?token|refresh[_-]?token|credential)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`)

// authorizationHeader redacts the whole value of an Authorization header.
var authorizationHeader = regexp.MustCompile(`(?i)\b(authorization)\b\s*[:=]\s*\S.*`)

// bearerToken matches RFC 6750 style bearer tokens without a preceding label.
var bearerToken = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._+/=-]{8,}`)

// jwt matches JSON web tokens.
var jwt = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\b`)

// knownTokens matches provider token shapes.
var knownTokens = regexp.MustCompile(`\b(gh[opsu]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})\b`)

// String returns s with every recognized credential replaced by a redaction
// marker. It never mutates its input.
func String(s string) string {
	if s == "" {
		return s
	}
	out := urlUserinfo.ReplaceAllString(s, "${1}"+marker+"@")
	out = authorizationHeader.ReplaceAllString(out, "${1}=<redacted>")
	out = keyValuePair.ReplaceAllString(out, "${1}${2}"+marker)
	out = bearerToken.ReplaceAllString(out, "Bearer "+marker)
	out = jwt.ReplaceAllString(out, marker)
	out = knownTokens.ReplaceAllString(out, marker)
	return out
}

// Map returns a copy of m with sensitive keys and secret-shaped values
// redacted, so structured logging cannot leak credentials through nesting.
func Map(m map[string]any) map[string]any {
	sensitive := sensitiveKeys()
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sensitive[strings.ToLower(k)] {
			out[k] = marker
			continue
		}
		out[k] = Value(v)
	}
	return out
}

// Value redacts a single arbitrary value.
func Value(v any) any {
	switch t := v.(type) {
	case string:
		return String(t)
	case map[string]any:
		return Map(t)
	case map[string]string:
		out := make(map[string]string, len(t))
		for k, s := range t {
			if sensitiveKeys()[strings.ToLower(k)] {
				out[k] = marker
				continue
			}
			out[k] = String(s)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = Value(item)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, s := range t {
			out[i] = String(s)
		}
		return out
	default:
		return v
	}
}

func sensitiveKeys() map[string]bool {
	keys := Keys()
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// Keys returns the names of sensitive keys, used for structured-log audits.
func Keys() []string {
	return []string{
		"password", "passwd", "secret", "token", "api_key", "apikey",
		"access_key", "private_key", "authorization", "session_token",
		"database_url", "dsn", "stepfun_api_key", "github_app_private_key",
		"github_token", "runner_token", "credentials",
	}
}

// Header redacts an HTTP header value, always returning a redaction marker for
// credential-bearing headers.
func Header(name, value string) string {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key":
		return marker
	}
	return String(value)
}
