package model

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateRequest(t *testing.T) {
	valid := CompletionRequest{
		Model:    DefaultStepFunModel,
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	}
	if err := ValidateRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	cases := map[string]CompletionRequest{
		"no model":    {Messages: valid.Messages},
		"no messages": {Model: DefaultStepFunModel},
		"bad role":    {Model: DefaultStepFunModel, Messages: []Message{{Role: "", Content: "x"}}},
		"tool no id":  {Model: DefaultStepFunModel, Messages: []Message{{Role: RoleTool, Content: "x"}}},
		"bad effort":  {Model: DefaultStepFunModel, Messages: valid.Messages, ReasoningEffort: "extreme"},
	}
	for name, req := range cases {
		if err := ValidateRequest(req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s: err = %v, want ErrInvalidRequest", name, err)
		}
	}
}

func TestReasoningEffortValidation(t *testing.T) {
	for _, effort := range []ReasoningEffort{ReasoningLow, ReasoningMedium, ReasoningHigh} {
		if !effort.Valid() {
			t.Fatalf("%q should be valid", effort)
		}
	}
	if ReasoningEffort("extreme").Valid() {
		t.Fatal("unknown effort accepted")
	}
}

func TestRunToolLoopExecutesToolsAndAppendsResults(t *testing.T) {
	provider := NewFakeProvider(
		ToolCallResponse("call_1", "read_file", `{"path":"main.go"}`),
		TextResponse("the file defines Add"),
	)
	var seen []ToolCall
	executor := ToolExecutorFunc(func(_ context.Context, call ToolCall) (string, error) {
		seen = append(seen, call)
		return `{"content":"package main"}`, nil
	})

	result, err := RunToolLoop(context.Background(), provider, CompletionRequest{
		Model:    DefaultStepFunModel,
		Messages: []Message{{Role: RoleUser, Content: "what does main.go contain?"}},
	}, executor, 0)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}
	if len(seen) != 1 || seen[0].Function.Name != "read_file" {
		t.Fatalf("executed calls = %+v", seen)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Result != `{"content":"package main"}` {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	// The tool result must be appended with the provider's tool_call_id.
	last := result.Messages[len(result.Messages)-2]
	if last.Role != RoleTool || last.ToolCallID != "call_1" {
		t.Fatalf("appended tool message = %+v", last)
	}
	// Usage accumulates across turns.
	if result.Usage.TotalTokens != 28 {
		t.Fatalf("total tokens = %d, want 28", result.Usage.TotalTokens)
	}
	if result.Final.Message.Content != "the file defines Add" {
		t.Fatalf("final content = %q", result.Final.Message.Content)
	}
}

func TestRunToolLoopSurvivesToolFailure(t *testing.T) {
	provider := NewFakeProvider(
		ToolCallResponse("call_1", "read_file", `{"path":""}`),
		TextResponse("the path was invalid, so nothing was read"),
	)
	executor := ToolExecutorFunc(func(_ context.Context, _ ToolCall) (string, error) {
		return "", errors.New("path is required")
	})

	result, err := RunToolLoop(context.Background(), provider, CompletionRequest{
		Model:    DefaultStepFunModel,
		Messages: []Message{{Role: RoleUser, Content: "read"}},
	}, executor, 0)
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result.ToolCalls[0].Err == "" {
		t.Fatal("tool failure was not recorded")
	}
	if !strings.Contains(result.ToolCalls[0].Result, "path is required") {
		t.Fatalf("failure result = %q", result.ToolCalls[0].Result)
	}
	if result.Final.Message.Content == "" {
		t.Fatal("model should still answer after a tool failure")
	}
}

func TestRunToolLoopStopsAtTurnLimit(t *testing.T) {
	provider := NewFakeProvider(ToolCallResponse("call_1", "read_file", `{}`))
	executor := ToolExecutorFunc(func(_ context.Context, _ ToolCall) (string, error) {
		return "ok", nil
	})
	_, err := RunToolLoop(context.Background(), provider, CompletionRequest{
		Model:    DefaultStepFunModel,
		Messages: []Message{{Role: RoleUser, Content: "loop"}},
	}, executor, 3)
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v, want turn-limit error", err)
	}
}

func TestRunToolLoopRejectsEmptyCompletion(t *testing.T) {
	provider := NewFakeProvider(CompletionResponse{Message: Message{Role: RoleAssistant}})
	executor := ToolExecutorFunc(func(_ context.Context, _ ToolCall) (string, error) {
		return "", nil
	})
	_, err := RunToolLoop(context.Background(), provider, CompletionRequest{
		Model:    DefaultStepFunModel,
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, executor, 0)
	if !errors.Is(err, ErrEmptyCompletion) {
		t.Fatalf("err = %v, want ErrEmptyCompletion", err)
	}
}

func TestRunToolLoopRejectsProviderError(t *testing.T) {
	provider := NewFakeProviderWithErrors(nil, map[int]error{1: ErrProviderRateLimit})
	executor := ToolExecutorFunc(func(_ context.Context, _ ToolCall) (string, error) { return "", nil })
	_, err := RunToolLoop(context.Background(), provider, CompletionRequest{
		Model:    DefaultStepFunModel,
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, executor, 0)
	if !errors.Is(err, ErrProviderRateLimit) {
		t.Fatalf("err = %v, want ErrProviderRateLimit", err)
	}
}

func TestParseToolArguments(t *testing.T) {
	call := ToolCall{ID: "c1", Function: ToolCallFunction{Name: "read_file", Arguments: `{"path":"main.go"}`}}
	var payload struct {
		Path string `json:"path"`
	}
	if err := ParseToolArguments(call, &payload); err != nil {
		t.Fatalf("ParseToolArguments: %v", err)
	}
	if payload.Path != "main.go" {
		t.Fatalf("path = %q", payload.Path)
	}

	bad := []string{"", "not json", `["array"]`, `"string"`}
	for _, args := range bad {
		call.Function.Arguments = args
		if err := ParseToolArguments(call, &payload); !errors.Is(err, ErrToolArgsInvalid) {
			t.Fatalf("arguments %q: err = %v, want ErrToolArgsInvalid", args, err)
		}
	}
}

func TestSentinelForStatus(t *testing.T) {
	cases := map[int]error{
		408: ErrProviderTimeout,
		429: ErrProviderRateLimit,
		500: ErrProviderServer,
		503: ErrProviderServer,
		400: ErrProviderResponse,
	}
	for status, want := range cases {
		if got := SentinelForStatus(status); !errors.Is(got, want) {
			t.Fatalf("status %d: sentinel = %v, want %v", status, got, want)
		}
	}
}
