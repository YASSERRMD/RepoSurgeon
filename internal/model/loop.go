package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
)

// ValidateRequest enforces the gateway's own rules before a provider is
// contacted: known model, allowed reasoning effort, at least one message and
// within-limits message count.
func ValidateRequest(req CompletionRequest) error {
	if req.Model == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("%w: at least one message is required", ErrInvalidRequest)
	}
	if len(req.Messages) > 512 {
		return fmt.Errorf("%w: at most 512 messages are supported", ErrInvalidRequest)
	}
	for i, message := range req.Messages {
		if message.Role == "" {
			return fmt.Errorf("%w: message %d has no role", ErrInvalidRequest, i)
		}
		if message.Role == RoleTool && message.ToolCallID == "" {
			return fmt.Errorf("%w: tool message %d has no tool_call_id", ErrInvalidRequest, i)
		}
	}
	if req.ReasoningEffort != "" && !req.ReasoningEffort.Valid() {
		return fmt.Errorf("%w: reasoning_effort must be low, medium or high", ErrInvalidRequest)
	}
	return nil
}

// LoopResult is the outcome of a tool-calling conversation.
type LoopResult struct {
	Messages  []Message
	Usage     Usage
	Turns     int
	ToolCalls []ExecutedToolCall
	Final     CompletionResponse
}

// ExecutedToolCall records one locally executed tool invocation.
type ExecutedToolCall struct {
	Call   ToolCall
	Result string
	Err    string
}

// RunToolLoop drives the provider until it answers without requesting tools,
// executing every requested tool locally and appending the results with the
// provider's tool_call_id. Tool arguments are untrusted data: they are parsed
// and validated here before execution, and an invalid call turns into a tool
// result rather than a crash.
func RunToolLoop(ctx context.Context, provider Provider, req CompletionRequest, executor ToolExecutor, maxTurns int) (LoopResult, error) {
	if err := ValidateRequest(req); err != nil {
		return LoopResult{}, err
	}
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	if maxTurns > maxTurnsCeiling {
		maxTurns = maxTurnsCeiling
	}
	if executor == nil {
		return LoopResult{}, fmt.Errorf("%w: tool executor is required", ErrInvalidRequest)
	}

	messages := append([]Message(nil), req.Messages...)
	req.Messages = messages
	result := LoopResult{}
	total := Usage{}

	for turn := 1; turn <= maxTurns; turn++ {
		response, err := provider.Complete(ctx, req)
		if err != nil {
			return result, err
		}
		total.PromptTokens += response.Usage.PromptTokens
		total.CachedPromptTokens += response.Usage.CachedPromptTokens
		total.CompletionTokens += response.Usage.CompletionTokens
		total.TotalTokens += response.Usage.TotalTokens
		result.Turns = turn
		result.Usage = total

		if len(response.Message.ToolCalls) == 0 {
			if response.Message.Content == "" && response.FinishReason == "" {
				return result, fmt.Errorf("%w: response carried neither content nor finish_reason", ErrEmptyCompletion)
			}
			messages = append(messages, response.Message)
			result.Messages = messages
			result.Final = response
			result.Final.Usage = total
			return result, nil
		}

		messages = append(messages, response.Message)
		for _, call := range response.Message.ToolCalls {
			if call.ID == "" {
				call.ID = "tc_" + ids.MustNew("toolcall")
			}
			if !validJSON(call.Function.Arguments) {
				call.Function.Arguments = "{}"
			}
			executed := ExecutedToolCall{Call: call}
			output, execErr := executor.Execute(ctx, call)
			if execErr != nil {
				executed.Err = execErr.Error()
				output = fmt.Sprintf(`{"status":"error","error":%q}`, execErr.Error())
			}
			executed.Result = output
			result.ToolCalls = append(result.ToolCalls, executed)
			messages = append(messages, Message{
				Role:       RoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    output,
			})
		}
		req.Messages = messages
	}

	return result, fmt.Errorf("%w: tool loop exceeded %d turns", ErrInvalidRequest, maxTurns)
}

// validJSON reports whether raw is a JSON object.
func validJSON(raw string) bool {
	if raw == "" {
		return false
	}
	var payload map[string]any
	return json.Unmarshal([]byte(raw), &payload) == nil
}

// ParseToolArguments decodes tool arguments into out, rejecting non-object
// payloads. Callers use it before executing a tool.
func ParseToolArguments(call ToolCall, out any) error {
	if strings.TrimSpace(call.Function.Arguments) == "" {
		return fmt.Errorf("%w: %s received no arguments", ErrToolArgsInvalid, call.Function.Name)
	}
	var probe any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &probe); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrToolArgsInvalid, call.Function.Name, err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return fmt.Errorf("%w: %s arguments must be a JSON object", ErrToolArgsInvalid, call.Function.Name)
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), out); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrToolArgsInvalid, call.Function.Name, err)
	}
	return nil
}
