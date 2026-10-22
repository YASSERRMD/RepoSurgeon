package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
)

// Page bounds shared by every keyset-paginated listing in the store.
const (
	DefaultPageLimit = 20
	MaxPageLimit     = 100
)

// queryTimeout bounds every repository query and transaction so a slow
// statement can never hold a pooled connection or a transaction open forever.
const queryTimeout = 5 * time.Second

// ErrValidation reports client-correctable input problems, such as a
// malformed cursor or an out-of-range limit. It is the domain sentinel so the
// API layer can match it with errors.Is and return a 400.
var ErrValidation = domain.ErrValidation

// Page is one keyset-paginated result set. It is the API wire shape: the
// cursor is only set when more rows exist and encodes the sort key of the
// last returned item.
type Page[T any] = domain.Page[T]

// cursorSeparator joins the sort-key parts inside an opaque cursor.
const cursorSeparator = "|"

// encodeCursor returns an opaque, URL-safe token for the given sort-key parts.
func encodeCursor(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, cursorSeparator)))
}

// decodeCursor splits an opaque cursor back into its sort-key parts. It
// rejects anything that was not produced by encodeCursor.
func decodeCursor(cursor string) ([]string, error) {
	if cursor == "" {
		return nil, fmt.Errorf("%w: cursor must not be empty", ErrValidation)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("%w: cursor is not opaque base64", ErrValidation)
	}
	parts := strings.Split(string(raw), cursorSeparator)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("%w: cursor does not carry the expected sort key", ErrValidation)
	}
	return parts, nil
}

// createdCursor encodes the (created_at, id) sort key used by listings ordered
// by created_at DESC, id DESC.
func createdCursor(at time.Time, id string) string {
	return encodeCursor(at.UTC().Format(time.RFC3339Nano), id)
}

// parseCreatedCursor decodes a cursor produced by createdCursor.
func parseCreatedCursor(cursor string) (time.Time, string, error) {
	parts, err := decodeCursor(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: cursor created_at is not a timestamp", ErrValidation)
	}
	return at.UTC(), parts[1], nil
}

// SequenceCursor encodes the (sequence, id) sort key used by listings ordered
// by sequence DESC, id DESC.
func SequenceCursor(sequence int64, id string) string {
	return encodeCursor(strconv.FormatInt(sequence, 10), id)
}

// ParseSequenceCursor decodes a cursor produced by SequenceCursor.
func ParseSequenceCursor(cursor string) (int64, string, error) {
	parts, err := decodeCursor(cursor)
	if err != nil {
		return 0, "", err
	}
	sequence, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("%w: cursor sequence is not an integer", ErrValidation)
	}
	return sequence, parts[1], nil
}

// clampLimit applies the default and maximum page bounds.
func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultPageLimit
	case limit > MaxPageLimit:
		return MaxPageLimit
	default:
		return limit
	}
}

// pageFromRows turns a probe slice that fetched limit+1 rows into a Page. The
// caller must have ordered the query by the sort key encoded by cursorFor,
// which is applied to the last row that is actually returned.
func pageFromRows[T any](items []T, limit int, cursorFor func(T) string) Page[T] {
	if len(items) == 0 {
		return Page[T]{Items: []T{}}
	}
	if len(items) > limit {
		returned := items[:limit]
		return Page[T]{
			Items:      returned,
			NextCursor: cursorFor(returned[len(returned)-1]),
		}
	}
	return Page[T]{Items: items}
}

// withQueryTimeout bounds ctx so every repository call is cancellable.
func withQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, queryTimeout)
}
