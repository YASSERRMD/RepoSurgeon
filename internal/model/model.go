// Package model defines the model-agnostic provider contract used by
// RepoSurgeon and the StepFun Step 5 Preview implementation of it.
//
// The gateway owns three responsibilities:
//  1. Speak the provider protocol (chat completions, tool calls, SSE).
//  2. Execute tool calls locally and append the results, because the model is
//     never assumed to browse, execute code or read files itself.
//  3. Account usage (prompt, cached and completion tokens) for every request.
package model

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Role constants for chat messages.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ReasoningEffort controls how much deliberation the model spends. StepFun
// accepts low, medium and high.
type ReasoningEffort string

// Reasoning effort levels.
const (
	ReasoningLow    ReasoningEffort = "low"
	ReasoningMedium ReasoningEffort = "medium"
	ReasoningHigh   ReasoningEffort = "high"
)

// Valid reports whether the effort is supported.
func (e ReasoningEffort) Valid() bool {
	switch e {
	case ReasoningLow, ReasoningMedium, ReasoningHigh:
		return true
	}
	return false
}

// Message is one chat message. Tool calls and tool results are carried on it
// so the transcript can be replayed to the provider.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is a model-requested tool invocation. The provider returns an ID
// that must be echoed when the result is appended.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction names the tool and carries its raw JSON arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDeclaration describes a tool to the provider.
type ToolDeclaration struct {
	Type     string         `json:"type"`
	Function ToolDefinition `json:"function"`
}

// ToolDefinition is the JSON-schema description of a tool.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Usage is the token accounting for one request.
type Usage struct {
	PromptTokens       int64 `json:"prompt_tokens"`
	CachedPromptTokens int64 `json:"cached_tokens,omitempty"`
	CompletionTokens   int64 `json:"completion_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
}

// CompletionRequest is the provider-neutral chat request.
type CompletionRequest struct {
	Model           string            `json:"model"`
	Messages        []Message         `json:"messages"`
	Tools           []ToolDeclaration `json:"tools,omitempty"`
	ReasoningEffort ReasoningEffort   `json:"reasoning_effort,omitempty"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	Stream          bool              `json:"stream,omitempty"`
}

// CompletionResponse is one provider answer with its usage accounting.
type CompletionResponse struct {
	Message      Message `json:"message"`
	Usage        Usage   `json:"usage"`
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	LatencyMS    int64   `json:"latency_ms"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

// Provider is the model-agnostic interface every inference engine implements.
// Step 3.7 Flash and the CI conformance provider satisfy the same contract.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

// Sentinel errors so callers can branch without parsing provider messages.
var (
	ErrInvalidRequest    = errors.New("model: invalid request")
	ErrProviderTimeout   = errors.New("model: provider timed out")
	ErrProviderRateLimit = errors.New("model: provider rate limit")
	ErrProviderServer    = errors.New("model: provider server error")
	ErrProviderTransport = errors.New("model: provider transport error")
	ErrEmptyCompletion   = errors.New("model: provider returned an empty completion")
	ErrToolArgsInvalid   = errors.New("model: tool arguments are not valid JSON")
	ErrUnsupportedTool   = errors.New("model: unsupported tool")
	ErrProviderResponse  = errors.New("model: provider response is malformed")
)

// ProviderError carries the HTTP status and a redacted message.
type ProviderError struct {
	Status      int
	Code        string
	Message     string
	RetryAfter  time.Duration
	sentinelErr error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s (status %d): %s", e.Code, e.Status, e.Message)
}

// Unwrap maps the provider error onto the sentinel callers switch on.
func (e *ProviderError) Unwrap() error { return e.sentinelErr }

// NewProviderError builds a provider error whose Unwrap reports the sentinel
// for its status, so callers can branch with errors.Is.
func NewProviderError(status int, code, message string) *ProviderError {
	return &ProviderError{
		Status:      status,
		Code:        code,
		Message:     message,
		sentinelErr: SentinelForStatus(status),
	}
}

// SentinelForStatus maps an HTTP status onto the package sentinel.
func SentinelForStatus(status int) error {
	switch {
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return ErrProviderTimeout
	case status == http.StatusTooManyRequests:
		return ErrProviderRateLimit
	case status >= 500:
		return ErrProviderServer
	default:
		return ErrProviderResponse
	}
}

// ToolExecutor runs one tool call and returns its result payload. The tool
// broker implements this from M07 onward; M03 ships the contract, the loop and
// a scripted executor for conformance tests.
type ToolExecutor interface {
	Execute(ctx context.Context, call ToolCall) (result string, err error)
}

// ToolExecutorFunc adapts a function to the ToolExecutor interface.
type ToolExecutorFunc func(ctx context.Context, call ToolCall) (string, error)

// Execute runs the wrapped function.
func (f ToolExecutorFunc) Execute(ctx context.Context, call ToolCall) (string, error) {
	return f(ctx, call)
}

// Loop limits for tool-calling conversations.
const (
	DefaultMaxTurns = 8
	maxTurnsCeiling = 32
)
