package api

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Router is a method-aware path router with JSON error semantics. Unlike
// http.ServeMux it never emits plain-text 404/405 bodies: unknown paths always
// return the canonical error envelope, and method mismatches advertise the
// allowed methods.
type Router struct {
	routes []*route
}

type segment struct {
	// value is the literal text for static segments.
	value string
	// param is the parameter name for dynamic segments, empty otherwise.
	param string
}

type route struct {
	method   string
	pattern  string
	segments []segment
	handler  http.HandlerFunc
}

type paramsKey struct{}

// NewRouter returns an empty router.
func NewRouter() *Router { return &Router{} }

// Handle registers handler for method and pattern. Patterns are absolute
// paths such as "/api/v1/runs/{run_id}". Registration panics on programming
// errors (invalid method or pattern, duplicate route) so misconfiguration
// fails at startup rather than at request time.
func (r *Router) Handle(method, pattern string, handler http.HandlerFunc) {
	if !validMethod(method) {
		panic("api: invalid HTTP method " + method)
	}
	if !strings.HasPrefix(pattern, "/") {
		panic("api: pattern must start with '/': " + pattern)
	}
	if len(pattern) > 1 && strings.HasSuffix(pattern, "/") {
		panic("api: pattern must not end with '/': " + pattern)
	}
	segs, err := parseSegments(pattern)
	if err != nil {
		panic("api: invalid pattern " + pattern + ": " + err.Error())
	}
	for _, existing := range r.routes {
		if existing.method == method && existing.pattern == pattern {
			panic("api: duplicate route " + method + " " + pattern)
		}
	}
	r.routes = append(r.routes, &route{method: method, pattern: pattern, segments: segs, handler: handler})
}

// Params returns the path parameters extracted for the current request.
func Params(ctx context.Context) map[string]string {
	if v, ok := ctx.Value(paramsKey{}).(map[string]string); ok {
		return v
	}
	return map[string]string{}
}

// ServeHTTP classifies the request and dispatches to the matching handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	pathSegs := splitPath(req.URL.Path)
	for _, rt := range r.routes {
		if rt.method != req.Method {
			continue
		}
		if params, ok := rt.match(pathSegs); ok {
			ctx := context.WithValue(req.Context(), paramsKey{}, params)
			rt.handler(w, req.WithContext(ctx))
			return
		}
	}
	if allowed := r.allowedMethods(pathSegs); len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		WriteError(w, req, ErrMethodNotAllowed, map[string]any{"allowed_methods": allowed})
		return
	}
	WriteError(w, req, ErrNotFound, nil)
}

func (r *Router) allowedMethods(pathSegs []string) []string {
	var allowed []string
	for _, rt := range r.routes {
		if _, ok := rt.match(pathSegs); ok {
			allowed = append(allowed, rt.method)
		}
	}
	sort.Strings(allowed)
	return allowed
}

func (rt *route) match(pathSegs []string) (map[string]string, bool) {
	if len(pathSegs) != len(rt.segments) {
		return nil, false
	}
	params := map[string]string{}
	for i, seg := range rt.segments {
		switch {
		case seg.param != "":
			value, err := url.PathUnescape(pathSegs[i])
			if err != nil || value == "" || strings.Contains(value, "/") {
				return nil, false
			}
			params[seg.param] = value
		default:
			value, err := url.PathUnescape(pathSegs[i])
			if err != nil || value != seg.value {
				return nil, false
			}
		}
	}
	return params, true
}

// parseSegments converts a pattern into comparable segments.
func parseSegments(pattern string) ([]segment, error) {
	trimmed := strings.Trim(pattern, "/")
	if trimmed == "" {
		return []segment{{value: ""}}, nil
	}
	parts := strings.Split(trimmed, "/")
	out := make([]segment, 0, len(parts))
	for _, part := range parts {
		unescaped, err := url.PathUnescape(part)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(unescaped, "{") || strings.HasSuffix(unescaped, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(unescaped, "{"), "}")
			if name == "" || strings.ContainsAny(name, "{}") {
				return nil, errInvalidPattern
			}
			out = append(out, segment{param: name})
			continue
		}
		if strings.ContainsAny(unescaped, "{}") {
			return nil, errInvalidPattern
		}
		out = append(out, segment{value: unescaped})
	}
	return out, nil
}

type patternError string

func (e patternError) Error() string { return string(e) }

const errInvalidPattern patternError = "api: path parameters must span a whole segment"

// splitPath splits a request path into segments. A trailing slash is a
// distinct path, so "/health/live/" never matches "/health/live".
func splitPath(p string) []string {
	if p == "" || p == "/" {
		return []string{""}
	}
	if strings.HasSuffix(p, "/") {
		return nil
	}
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}

func validMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return true
	}
	return false
}
