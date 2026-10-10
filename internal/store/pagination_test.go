package store

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 10, 12, 34, 56, 123456000, time.UTC)
	cursor := createdCursor(at, "prj_abcdefghijklmnopqrstuvwxy234567")

	gotAt, gotID, err := parseCreatedCursor(cursor)
	if err != nil {
		t.Fatalf("parseCreatedCursor: %v", err)
	}
	if !gotAt.Equal(at) {
		t.Fatalf("created_at = %s, want %s", gotAt, at)
	}
	if gotID != "prj_abcdefghijklmnopqrstuvwxy234567" {
		t.Fatalf("id = %q", gotID)
	}
}

func TestSequenceCursorRoundTrip(t *testing.T) {
	cursor := sequenceCursor(9007199254740991, "aue_abcdefghijklmnopqrstuvwxy234567")

	sequence, id, err := parseSequenceCursor(cursor)
	if err != nil {
		t.Fatalf("parseSequenceCursor: %v", err)
	}
	if sequence != 9007199254740991 {
		t.Fatalf("sequence = %d", sequence)
	}
	if id != "aue_abcdefghijklmnopqrstuvwxy234567" {
		t.Fatalf("id = %q", id)
	}
}

func TestCursorIsOpaque(t *testing.T) {
	cursor := createdCursor(time.Now().UTC(), "prj_x")
	for _, part := range []string{"prj_x", "2026-", "|", "="} {
		if strings.Contains(cursor, part) {
			t.Fatalf("cursor %q leaks %q", cursor, part)
		}
	}
}

func TestParseCreatedCursorRejectsTampering(t *testing.T) {
	valid := createdCursor(time.Now().UTC(), "prj_x")
	cases := map[string]string{
		"empty":               "",
		"not base64":          "!!!!not-base64!!!!",
		"missing separator":   encodeCursor("onlyonepart"),
		"empty id":            encodeCursor(time.Now().UTC().Format(time.RFC3339Nano), ""),
		"bad timestamp":       encodeCursor("yesterday", "prj_x"),
		"prefix stripped":     valid[1:],
		"extra trailing part": encodeCursor("2026-10-10T00:00:00Z", "prj_x", "extra"),
		"raw timestamp":       "2026-10-10T00:00:00Z",
	}
	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseCreatedCursor(cursor)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("parseCreatedCursor(%q) error = %v, want ErrValidation", cursor, err)
			}
		})
	}
}

func TestParseSequenceCursorRejectsTampering(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"not base64":    "!!!!",
		"missing id":    encodeCursor("7", ""),
		"not a number":  encodeCursor("seven", "aue_x"),
		"overflow":      encodeCursor(strconv.FormatInt(1, 10)+"000000000000000000000", "aue_x"),
		"raw sequence":  "7",
		"extra segment": encodeCursor("7", "aue_x", "more"),
	}
	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseSequenceCursor(cursor)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("parseSequenceCursor(%q) error = %v, want ErrValidation", cursor, err)
			}
		})
	}
}

func TestClampLimit(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{-5, DefaultPageLimit},
		{0, DefaultPageLimit},
		{1, 1},
		{20, 20},
		{99, 99},
		{100, MaxPageLimit},
		{101, MaxPageLimit},
		{100000, MaxPageLimit},
	}
	for _, tc := range cases {
		if got := clampLimit(tc.in); got != tc.want {
			t.Errorf("clampLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if DefaultPageLimit != 20 || MaxPageLimit != 100 {
		t.Fatalf("page bounds changed: default %d, max %d", DefaultPageLimit, MaxPageLimit)
	}
}

func TestPageFromRows(t *testing.T) {
	items := []int{5, 4, 3}

	full := pageFromRows(items, 5, func(i int) string { return strconv.Itoa(i) })
	if len(full.Items) != 3 || full.NextCursor != "" {
		t.Fatalf("full page = %+v", full)
	}

	probed := pageFromRows(items, 2, func(i int) string { return strconv.Itoa(i) })
	if len(probed.Items) != 2 || probed.NextCursor == "" {
		t.Fatalf("probed page = %+v", probed)
	}
	if probed.NextCursor != "4" {
		t.Fatalf("next cursor = %q, want the last returned item", probed.NextCursor)
	}

	empty := pageFromRows([]int{}, 10, func(i int) string { return "" })
	if empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("empty page must expose a non-nil slice for JSON, got %+v", empty.Items)
	}
}

func TestLikeEscape(t *testing.T) {
	cases := map[string]string{
		"plain":      "plain",
		"100%":       `100\%`,
		"a_b":        `a\_b`,
		`back\slash`: `back\\slash`,
		"":           "",
	}
	for in, want := range cases {
		if got := likeEscape(in); got != want {
			t.Errorf("likeEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
