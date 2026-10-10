package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestRouter() *Router {
	r := NewRouter()
	r.Handle(http.MethodGet, "/health/live", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Handle(http.MethodGet, "/api/v1/projects/{project_id}", func(w http.ResponseWriter, req *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"project_id": Params(req.Context())["project_id"]})
	})
	r.Handle(http.MethodPatch, "/api/v1/projects/{project_id}", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"patched": "yes"})
	})
	return r
}

func TestRouterDispatchesAndExtractsParams(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/projects/proj_123", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "proj_123") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRouterUnknownPathIsJSON404(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not_found") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRouterMethodMismatchIsJSON405WithAllow(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/health/live", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET" {
		t.Fatalf("allow = %q", allow)
	}
	if !strings.Contains(rec.Body.String(), "method_not_allowed") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRouterMethodMismatchListsAllMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/projects/abc", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, PATCH" {
		t.Fatalf("allow = %q, want sorted list", allow)
	}
}

func TestRouterRejectsEmptyAndEscapedParams(t *testing.T) {
	// Empty param value does not match the dynamic segment.
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/projects/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}

	// Param containing an encoded slash cannot forge a different path.
	rec = httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/projects/a%2Fb", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("encoded slash status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestRouterTrailingSlashIsDistinct(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRouterRejectsInvalidRegistration(t *testing.T) {
	cases := []struct{ method, pattern string }{
		{"FETCH", "/x"},
		{"GET", "health/live"},
		{"GET", "/x/{}/y"},
		{"GET", "/a/b}c"},
	}
	for _, tc := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("registration %q %q should panic", tc.method, tc.pattern)
				}
			}()
			NewRouter().Handle(tc.method, tc.pattern, http.NotFound)
		}()
	}
}

func TestRouterRejectsDuplicateRegistration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration should panic")
		}
	}()
	r := NewRouter()
	r.Handle(http.MethodGet, "/health/live", http.NotFound)
	r.Handle(http.MethodGet, "/health/live", http.NotFound)
}

func TestParamsMissingFromContext(t *testing.T) {
	params := Params(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil).Context())
	if len(params) != 0 {
		t.Fatalf("params = %v", params)
	}
}
