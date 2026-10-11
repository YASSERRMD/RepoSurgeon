package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sseBody joins SSE events into a streaming response body.
func sseBody(events ...string) string {
	var builder strings.Builder
	for _, event := range events {
		builder.WriteString("data: ")
		builder.WriteString(event)
		builder.WriteString("\n\n")
	}
	builder.WriteString("data: [DONE]\n\n")
	return builder.String()
}

func newTestProvider(t *testing.T, handler http.HandlerFunc) (*StepFunProvider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := NewStepFunProvider(StepFunConfig{
		Endpoint:   server.URL,
		APIKey:     "test-key",
		Model:      DefaultStepFunModel,
		HTTPClient: server.Client(),
		Timeout:    5 * time.Second,
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("NewStepFunProvider: %v", err)
	}
	return provider, server
}

func baseRequest() CompletionRequest {
	return CompletionRequest{
		Model:           DefaultStepFunModel,
		Messages:        []Message{{Role: RoleUser, Content: "summarize the repository"}},
		ReasoningEffort: ReasoningHigh,
		MaxTokens:       512,
	}
}

func TestCompleteAssemblesStreamedContent(t *testing.T) {
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization header = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(
			`{"model":"step-5-preview","choices":[{"index":0,"delta":{"role":"assistant","content":"RepoSurgeon "}}]}`,
			`{"model":"step-5-preview","choices":[{"index":0,"delta":{"content":"is a workspace"}}]}`,
			`{"model":"step-5-preview","choices":[{"index":0,"delta":{"content":" for engineers."},"finish_reason":"stop"}],"usage":{"prompt_tokens":42,"cached_tokens":16,"completion_tokens":9,"total_tokens":51}}`,
		))
	})

	response, err := provider.Complete(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if response.Message.Content != "RepoSurgeon is a workspace for engineers." {
		t.Fatalf("content = %q", response.Message.Content)
	}
	if response.Usage.PromptTokens != 42 || response.Usage.CachedPromptTokens != 16 || response.Usage.CompletionTokens != 9 {
		t.Fatalf("usage = %+v", response.Usage)
	}
	if response.FinishReason != "stop" {
		t.Fatalf("finish reason = %q", response.FinishReason)
	}
}

func TestCompleteAssemblesFragmentedToolCallDeltas(t *testing.T) {
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(
			`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"ma"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"in.go\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":6,"total_tokens":16}}`,
		))
	})

	response, err := provider.Complete(context.Background(), CompletionRequest{
		Model:    DefaultStepFunModel,
		Messages: []Message{{Role: RoleUser, Content: "read main.go"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call_abc" || call.Function.Name != "read_file" {
		t.Fatalf("call = %+v", call)
	}
	if call.Function.Arguments != `{"path":"main.go"}` {
		t.Fatalf("fragmented arguments = %q", call.Function.Arguments)
	}
}

func TestCompleteHandlesMultipleToolCallsInOrder(t *testing.T) {
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search_code","arguments":"{\"q\":\"Add\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"git_status","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		))
	})

	response, err := provider.Complete(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(response.Message.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", response.Message.ToolCalls)
	}
	if response.Message.ToolCalls[0].ID != "call_1" || response.Message.ToolCalls[1].ID != "call_2" {
		t.Fatalf("ordering lost: %+v", response.Message.ToolCalls)
	}
}

func TestCompleteSendsToolsAndEffort(t *testing.T) {
	var captured map[string]any
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody(`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`))
	})

	req := baseRequest()
	req.Tools = []ToolDeclaration{{
		Type: "function",
		Function: ToolDefinition{
			Name:        "read_file",
			Description: "Read an authorized repository file",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
			},
		},
	}}
	if _, err := provider.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if captured["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", captured["reasoning_effort"])
	}
	if captured["stream"] != true {
		t.Fatalf("stream = %v", captured["stream"])
	}
	tools, ok := captured["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", captured["tools"])
	}
}

func TestCompleteMapsProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"rate limit", http.StatusTooManyRequests, ErrProviderRateLimit},
		{"server error", http.StatusInternalServerError, ErrProviderServer},
		{"bad request", http.StatusBadRequest, ErrProviderResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"error":{"message":"boom"}}`)
			})
			_, err := provider.Complete(context.Background(), baseRequest())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatal("error must be a *ProviderError")
			}
			if providerErr.Status != tc.status {
				t.Fatalf("status = %d", providerErr.Status)
			}
			if strings.Contains(providerErr.Message, "test-key") {
				t.Fatal("provider error leaked the api key")
			}
		})
	}
}

func TestCompleteRetriesRetryableStatuses(t *testing.T) {
	var attempts atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       http.NoBody,
			Header:     http.Header{},
			Request:    req,
		}, nil
	})}
	provider, err := NewStepFunProvider(StepFunConfig{
		Endpoint:   "https://example.invalid/v1/chat/completions",
		APIKey:     "test-key",
		HTTPClient: client,
		MaxRetries: 2,
		RetryBase:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepFunProvider: %v", err)
	}

	_, err = provider.Complete(context.Background(), baseRequest())
	if !errors.Is(err, ErrProviderServer) {
		t.Fatalf("err = %v, want server error after retries", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestCompleteHonoursContextCancellation(t *testing.T) {
	// A canceled context must fail fast, before the provider is contacted.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider, server := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()
	_, err := provider.Complete(ctx, baseRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCompleteHonoursClientTimeout(t *testing.T) {
	// A client-side timeout surfaces as a transport error rather than a hang.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A bounded sleep keeps the handler short-lived so the server can
		// close promptly after the client gives up.
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	provider, err := NewStepFunProvider(StepFunConfig{
		Endpoint:   server.URL,
		APIKey:     "test-key",
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("NewStepFunProvider: %v", err)
	}
	_, err = provider.Complete(context.Background(), baseRequest())
	if !errors.Is(err, ErrProviderTimeout) {
		t.Fatalf("err = %v, want ErrProviderTimeout", err)
	}
}

func TestCompleteRejectsEmptyStream(t *testing.T) {
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ":\n\n")
	})
	_, err := provider.Complete(context.Background(), baseRequest())
	if !errors.Is(err, ErrEmptyCompletion) {
		t.Fatalf("err = %v, want ErrEmptyCompletion", err)
	}
}

func TestCompleteRejectsMalformedChunk(t *testing.T) {
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {not json}\n\n")
	})
	_, err := provider.Complete(context.Background(), baseRequest())
	if !errors.Is(err, ErrProviderResponse) {
		t.Fatalf("err = %v, want ErrProviderResponse", err)
	}
}

func TestNewStepFunProviderRequiresKey(t *testing.T) {
	if _, err := NewStepFunProvider(StepFunConfig{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

func TestFakeProviderReplaysScriptedResponses(t *testing.T) {
	provider := NewFakeProvider(ToolCallResponse("c1", "read_file", `{}`), TextResponse("done"))
	executor := ToolExecutorFunc(func(_ context.Context, _ ToolCall) (string, error) { return "ok", nil })

	result, err := RunToolLoop(context.Background(), provider, baseRequest(), executor, 0)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result.Final.Message.Content != "done" {
		t.Fatalf("final = %q", result.Final.Message.Content)
	}
	if len(provider.Requests()) != 2 {
		t.Fatalf("provider saw %d requests", len(provider.Requests()))
	}
}
