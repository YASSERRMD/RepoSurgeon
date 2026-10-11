package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultStepFunEndpoint is the first-party StepFun chat completions endpoint.
const DefaultStepFunEndpoint = "https://api.stepfun.ai/v1/chat/completions"

// DefaultStepFunModel is the default inference engine.
const DefaultStepFunModel = "step-5-preview"

// HTTPClient bounds every provider call.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// StepFunConfig configures the StepFun provider.
type StepFunConfig struct {
	Endpoint   string
	APIKey     string
	Model      string
	HTTPClient HTTPClient
	Timeout    time.Duration
	MaxRetries int
	RetryBase  time.Duration
}

// StepFunProvider implements Provider against the StepFun chat completions API.
type StepFunProvider struct {
	endpoint   string
	apiKey     string
	model      string
	httpClient HTTPClient
	timeout    time.Duration
	maxRetries int
	retryBase  time.Duration
}

// NewStepFunProvider builds a validated provider.
func NewStepFunProvider(cfg StepFunConfig) (*StepFunProvider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("%w: stepfun api key is required", ErrInvalidRequest)
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultStepFunEndpoint
	}
	model := cfg.Model
	if model == "" {
		model = DefaultStepFunModel
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.TimeoutOr(60 * time.Second)}
	}
	return &StepFunProvider{
		endpoint:   endpoint,
		apiKey:     cfg.APIKey,
		model:      model,
		httpClient: client,
		timeout:    cfg.TimeoutOr(60 * time.Second),
		maxRetries: orDefaultInt(cfg.MaxRetries, 2),
		retryBase:  orDefaultDuration(cfg.RetryBase, 250*time.Millisecond),
	}, nil
}

// TimeoutOr returns the configured timeout or the fallback.
func (c StepFunConfig) TimeoutOr(def time.Duration) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return def
}

func orDefaultInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func orDefaultDuration(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// Name implements Provider.
func (p *StepFunProvider) Name() string { return "stepfun" }

// Complete performs one chat completion, transparently following SSE streams so
// callers always receive a fully assembled message.
func (p *StepFunProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if err := ValidateRequest(req); err != nil {
		return CompletionResponse{}, err
	}
	body := p.requestBody(req)

	start := time.Now()
	raw, err := p.roundTrip(ctx, body)
	if err != nil {
		return CompletionResponse{}, err
	}
	if raw.Streamed {
		response, err := assembleStream(raw.Body)
		if err != nil {
			return CompletionResponse{}, err
		}
		response.LatencyMS = time.Since(start).Milliseconds()
		return response, nil
	}

	var payload chatCompletionPayload
	if err := json.Unmarshal(raw.Body, &payload); err != nil {
		return CompletionResponse{}, fmt.Errorf("%w: %v", ErrProviderResponse, err)
	}
	response, err := payload.toResponse()
	if err != nil {
		return CompletionResponse{}, err
	}
	response.LatencyMS = time.Since(start).Milliseconds()
	return response, nil
}

// requestBody builds the provider payload. Streaming is enabled so tool-call
// argument deltas are handled the same way for every caller.
func (p *StepFunProvider) requestBody(req CompletionRequest) map[string]any {
	model := req.Model
	if model == "" {
		model = p.model
	}
	body := map[string]any{
		"model":    model,
		"messages": req.Messages,
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.ReasoningEffort != "" {
		body["reasoning_effort"] = string(req.ReasoningEffort)
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	return body
}

type roundTripResult struct {
	Body     []byte
	Streamed bool
	Status   int
}

// roundTrip sends the request with bounded retries for retryable statuses.
func (p *StepFunProvider) roundTrip(ctx context.Context, body map[string]any) (roundTripResult, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return roundTripResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}

	var lastErr error
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			delay := p.retryBase << (attempt - 1)
			select {
			case <-ctx.Done():
				return roundTripResult{}, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
		if err != nil {
			return roundTripResult{}, fmt.Errorf("%w: %v", ErrProviderTransport, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.httpClient.Do(req)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return roundTripResult{}, err
			}
			if isTimeout(err) {
				return roundTripResult{}, fmt.Errorf("%w: %v", ErrProviderTimeout, err)
			}
			lastErr = fmt.Errorf("%w: %v", ErrProviderTransport, err)
			continue
		}

		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%w: %v", ErrProviderTransport, readErr)
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			streamed := strings.Contains(resp.Header.Get("Content-Type"), "event-stream")
			return roundTripResult{Body: raw, Streamed: streamed, Status: resp.StatusCode}, nil
		}

		providerErr := &ProviderError{
			Status:      resp.StatusCode,
			Code:        "provider_error",
			Message:     summarizeBody(raw),
			RetryAfter:  parseRetryAfter(resp.Header.Get("Retry-After")),
			sentinelErr: SentinelForStatus(resp.StatusCode),
		}
		if retryableStatus(resp.StatusCode) && attempt < p.maxRetries {
			lastErr = providerErr
			continue
		}
		return roundTripResult{}, providerErr
	}
	return roundTripResult{}, lastErr
}

// isTimeout reports whether err is a client-side timeout, whether it comes
// from the request context or from the HTTP client's own timeout.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "Client.Timeout")
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func summarizeBody(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) > 400 {
		return trimmed[:400]
	}
	return trimmed
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	seconds := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		seconds = seconds*10 + int(r-'0')
	}
	return time.Duration(seconds) * time.Second
}

// ---- provider payload types ------------------------------------------------

type chatCompletionPayload struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CachedTokens     int64 `json:"cached_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
}

func (p chatCompletionPayload) toResponse() (CompletionResponse, error) {
	if len(p.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("%w: response carried no choices", ErrEmptyCompletion)
	}
	choice := p.Choices[0]
	message := Message{
		Role:      choice.Message.Role,
		Content:   choice.Message.Content,
		ToolCalls: choice.Message.ToolCalls,
	}
	if message.Role == "" {
		message.Role = RoleAssistant
	}
	usage := Usage{
		PromptTokens:       p.Usage.PromptTokens,
		CachedPromptTokens: p.Usage.CachedTokens,
		CompletionTokens:   p.Usage.CompletionTokens,
		TotalTokens:        p.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return CompletionResponse{
		Message:      message,
		Usage:        usage,
		Model:        p.Model,
		Provider:     "stepfun",
		FinishReason: choice.FinishReason,
	}, nil
}

// ---- SSE assembly ----------------------------------------------------------

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CachedTokens     int64 `json:"cached_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
}

// assembleStream reads an SSE stream and stitches content and tool-call
// argument deltas back together. Fragmented tool arguments are a required
// case: providers routinely split a JSON argument string across chunks.
func assembleStream(raw []byte) (CompletionResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	var (
		content      strings.Builder
		finishReason string
		model        string
		usage        Usage
		toolCalls    = map[int]*ToolCall{}
		order        []int
		sawData      bool
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		sawData = true

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return CompletionResponse{}, fmt.Errorf("%w: malformed stream chunk: %v", ErrProviderResponse, err)
		}
		if chunk.Usage != nil {
			usage.PromptTokens = chunk.Usage.PromptTokens
			usage.CachedPromptTokens = chunk.Usage.CachedTokens
			usage.CompletionTokens = chunk.Usage.CompletionTokens
			usage.TotalTokens = chunk.Usage.TotalTokens
		}
		for _, choice := range chunk.Choices {
			content.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			for _, delta := range choice.Delta.ToolCalls {
				call, ok := toolCalls[delta.Index]
				if !ok {
					call = &ToolCall{Type: "function"}
					toolCalls[delta.Index] = call
					order = append(order, delta.Index)
				}
				if delta.ID != "" {
					call.ID = delta.ID
				}
				if delta.Type != "" {
					call.Type = delta.Type
				}
				if delta.Function.Name != "" {
					call.Function.Name = delta.Function.Name
				}
				call.Function.Arguments += delta.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return CompletionResponse{}, fmt.Errorf("%w: reading stream: %v", ErrProviderResponse, err)
	}
	if !sawData {
		return CompletionResponse{}, fmt.Errorf("%w: stream contained no events", ErrEmptyCompletion)
	}

	assembled := make([]ToolCall, 0, len(order))
	for _, index := range order {
		call := toolCalls[index]
		if call.ID == "" {
			call.ID = fmt.Sprintf("tc_stream_%d", index)
		}
		if call.Type == "" {
			call.Type = "function"
		}
		assembled = append(assembled, *call)
	}

	message := Message{Role: RoleAssistant, Content: content.String(), ToolCalls: assembled}
	if len(assembled) == 0 && content.Len() == 0 {
		return CompletionResponse{}, fmt.Errorf("%w: stream produced neither content nor tool calls", ErrEmptyCompletion)
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return CompletionResponse{
		Message:      message,
		Usage:        usage,
		Model:        model,
		Provider:     "stepfun",
		FinishReason: finishReason,
	}, nil
}
