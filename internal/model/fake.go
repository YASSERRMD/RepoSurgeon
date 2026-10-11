package model

import (
	"context"
	"fmt"
	"time"
)

// FakeProvider is a deterministic in-memory provider used by CI conformance
// tests and local development. It replays scripted responses, so tests never
// depend on a live provider account.
type FakeProvider struct {
	name      string
	responses []CompletionResponse
	errs      []error
	requests  []CompletionRequest
	// Delay simulates provider latency; zero by default.
	Delay time.Duration
}

// NewFakeProvider builds a provider that replays the given responses in order.
// When the responses run out, the last one is reused.
func NewFakeProvider(responses ...CompletionResponse) *FakeProvider {
	return &FakeProvider{name: "fake", responses: responses}
}

// NewFakeProviderWithErrors builds a provider that fails on the listed turns
// (1-based) and otherwise replays the responses.
func NewFakeProviderWithErrors(responses []CompletionResponse, errsByTurn map[int]error) *FakeProvider {
	fake := &FakeProvider{name: "fake", responses: responses}
	for turn, err := range errsByTurn {
		index := turn - 1
		for len(fake.errs) <= index {
			fake.errs = append(fake.errs, nil)
		}
		fake.errs[index] = err
	}
	return fake
}

// Name implements Provider.
func (f *FakeProvider) Name() string { return f.name }

// Requests returns every request the fake has seen, for assertions.
func (f *FakeProvider) Requests() []CompletionRequest { return f.requests }

// Complete implements Provider.
func (f *FakeProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResponse, error) {
	if err := ValidateRequest(req); err != nil {
		return CompletionResponse{}, err
	}
	f.requests = append(f.requests, req)
	if f.Delay > 0 {
		time.Sleep(f.Delay)
	}
	index := len(f.requests) - 1
	if index < len(f.errs) && f.errs[index] != nil {
		return CompletionResponse{}, f.errs[index]
	}
	if len(f.responses) == 0 {
		return CompletionResponse{}, fmt.Errorf("%w: fake provider has no scripted responses", ErrEmptyCompletion)
	}
	if index >= len(f.responses) {
		index = len(f.responses) - 1
	}
	return f.responses[index], nil
}

// ToolCallResponse builds a response that requests a tool call.
func ToolCallResponse(id, name, arguments string) CompletionResponse {
	return CompletionResponse{
		Message: Message{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:   id,
				Type: "function",
				Function: ToolCallFunction{
					Name:      name,
					Arguments: arguments,
				},
			}},
		},
		Usage:        Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
		FinishReason: "tool_calls",
	}
}

// TextResponse builds a plain text completion.
func TextResponse(text string) CompletionResponse {
	return CompletionResponse{
		Message:      Message{Role: RoleAssistant, Content: text},
		Usage:        Usage{PromptTokens: 8, CompletionTokens: 6, TotalTokens: 14},
		FinishReason: "stop",
	}
}
