package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/model"
)

// ModelGateway is the provider-facing surface of the API. M03 exposes
// configuration and a live probe; runs drive it from M06 onward.
type ModelGateway struct {
	config   config.ModelConfig
	provider model.Provider
}

// NewModelGateway builds the gateway from configuration. A missing StepFun key
// disables the live provider but keeps the gateway usable for reporting.
func NewModelGateway(cfg config.ModelConfig) (*ModelGateway, error) {
	gateway := &ModelGateway{config: cfg}
	if strings.TrimSpace(cfg.StepFunAPIKey) == "" {
		return gateway, nil
	}
	provider, err := model.NewStepFunProvider(model.StepFunConfig{
		Endpoint: cfg.StepFunBaseURL,
		APIKey:   cfg.StepFunAPIKey,
		Model:    cfg.DefaultModelID,
		Timeout:  60 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	gateway.provider = provider
	return gateway, nil
}

// statusResponse reports provider readiness without exposing any secret.
type modelStatusResponse struct {
	Configured      bool   `json:"configured"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	BaseURL         string `json:"base_url"`
	ReasoningEffort string `json:"reasoning_effort"`
	KeyConfigured   bool   `json:"key_configured"`
}

// handleModelStatus reports the configured provider and whether a live key is
// present. It never returns the key itself.
func (s *Server) handleModelStatus(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "credential.admin") {
		return
	}
	gateway := s.gateway
	if gateway == nil {
		WriteJSON(w, http.StatusOK, modelStatusResponse{Configured: false, Provider: "none"})
		return
	}
	WriteJSON(w, http.StatusOK, modelStatusResponse{
		Configured:      gateway.provider != nil,
		Provider:        "stepfun",
		Model:           gateway.config.DefaultModelID,
		BaseURL:         gateway.config.StepFunBaseURL,
		ReasoningEffort: gateway.config.ReasoningEffort,
		KeyConfigured:   strings.TrimSpace(gateway.config.StepFunAPIKey) != "",
	})
}

// handleModelProbe sends a minimal completion to the configured provider and
// reports the measured latency and token usage. It is the only endpoint that
// spends provider budget, so it requires credential.admin.
func (s *Server) handleModelProbe(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "credential.admin") {
		return
	}
	gateway := s.gateway
	if gateway == nil || gateway.provider == nil {
		s.audit(r, session.Subject, domain.AuditEvent{
			OrganizationID: session.OrganizationID,
			Action:         "model.probe_failed",
			EntityType:     "provider",
			EntityID:       "stepfun",
			Payload:        map[string]any{"error": "provider_not_configured"},
		})
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "provider_not_configured",
			Message: "STEPFUN_API_KEY is not configured on this server",
		}, nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	response, err := gateway.provider.Complete(ctx, model.CompletionRequest{
		Model:           gateway.config.DefaultModelID,
		Messages:        []model.Message{{Role: model.RoleUser, Content: "Reply with the single word: ready"}},
		ReasoningEffort: model.ReasoningEffort(gateway.config.ReasoningEffort),
		MaxTokens:       16,
	})
	if err != nil {
		s.audit(r, session.Subject, domain.AuditEvent{
			OrganizationID: session.OrganizationID,
			Action:         "model.probe_failed",
			EntityType:     "provider",
			EntityID:       "stepfun",
			Payload:        map[string]any{"error": redactedProviderError(err)},
		})
		writeDomainError(w, r, err)
		return
	}

	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "model.probe",
		EntityType:     "provider",
		EntityID:       "stepfun",
		Payload: map[string]any{
			"model":             response.Model,
			"latency_ms":        response.LatencyMS,
			"prompt_tokens":     response.Usage.PromptTokens,
			"cached_tokens":     response.Usage.CachedPromptTokens,
			"completion_tokens": response.Usage.CompletionTokens,
		},
	})
	WriteJSON(w, http.StatusOK, map[string]any{
		"provider":   response.Provider,
		"model":      response.Model,
		"latency_ms": response.LatencyMS,
		"usage": map[string]any{
			"prompt_tokens":     response.Usage.PromptTokens,
			"cached_tokens":     response.Usage.CachedPromptTokens,
			"completion_tokens": response.Usage.CompletionTokens,
			"total_tokens":      response.Usage.TotalTokens,
		},
		"finish_reason": response.FinishReason,
	})
}

// redactedProviderError keeps provider failure detail safe for the audit log.
func redactedProviderError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, model.ErrProviderRateLimit) {
		return "provider_rate_limit"
	}
	if errors.Is(err, model.ErrProviderServer) {
		return "provider_server_error"
	}
	if errors.Is(err, model.ErrProviderTimeout) {
		return "provider_timeout"
	}
	return "provider_error"
}
