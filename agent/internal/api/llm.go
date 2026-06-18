package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/autosre/agent/internal/audit"
	"github.com/autosre/agent/internal/diagnosis"
	"github.com/autosre/agent/internal/settings"
	"github.com/autosre/agent/internal/uid"
)

// LLMConfigPusher is the subset of *diagnosis.Client the API needs to apply
// and test LLM provider configuration live, without restarting the diagnoser.
type LLMConfigPusher interface {
	PushConfig(ctx context.Context, cfg diagnosis.LLMConfig) error
	TestConfig(ctx context.Context, cfg diagnosis.LLMConfig) (diagnosis.LLMTestResult, error)
}

// ---------------------------------------------------------------------------
// GET /api/v1/integrations/llm
// ---------------------------------------------------------------------------

type llmIntegrationResponse struct {
	Configured     bool   `json:"configured"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	HasAPIKey      bool   `json:"has_api_key"`
}

func (s *Server) handleGetLLMIntegration(w http.ResponseWriter, r *http.Request) error {
	var resp llmIntegrationResponse
	if s.settings != nil {
		if saved, ok, err := s.settings.LoadLLMSettings(r.Context()); err == nil && ok {
			resp.Configured = saved.Provider != ""
			resp.Provider = saved.Provider
			resp.Model = saved.Model
			resp.TimeoutSeconds = saved.TimeoutSeconds
			resp.HasAPIKey = saved.APIKey != ""
		}
	}
	return jsonOK(w, resp)
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/llm
// ---------------------------------------------------------------------------

// llmRequest is shared by the save and test-connection endpoints.
// APIKey is a pointer so the save endpoint can distinguish "field omitted —
// keep the existing saved value" from "field present and empty — clear it".
type llmRequest struct {
	Provider       string  `json:"provider"`
	APIKey         *string `json:"api_key"`
	Model          string  `json:"model"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}

func (s *Server) handleSaveLLMIntegration(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	var req llmRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}
	if req.Provider != "" && req.Provider != "nim" && req.Provider != "gemini" {
		return &apiError{"provider must be 'nim', 'gemini', or '' (disable)", http.StatusBadRequest}
	}

	if s.settings == nil {
		return &apiError{"persistence unavailable; cannot save settings", http.StatusServiceUnavailable}
	}

	apiKey := ""
	if req.APIKey != nil {
		apiKey = *req.APIKey
	} else if existing, ok, _ := s.settings.LoadLLMSettings(r.Context()); ok {
		apiKey = existing.APIKey
	}
	if req.Provider != "" && apiKey == "" {
		return &apiError{"api_key is required for provider " + req.Provider, http.StatusBadRequest}
	}

	timeoutSeconds := req.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	saved := settings.LLMSettings{
		Provider:       req.Provider,
		APIKey:         apiKey,
		Model:          req.Model,
		TimeoutSeconds: timeoutSeconds,
	}
	if err := s.settings.SaveLLMSettings(r.Context(), saved); err != nil {
		s.log.Warn("api: save llm settings failed", "error", err)
		return &apiError{"failed to save settings", http.StatusInternalServerError}
	}

	if s.llm != nil {
		if err := s.llm.PushConfig(r.Context(), diagnosis.LLMConfig{
			Provider:       req.Provider,
			APIKey:         apiKey,
			Model:          req.Model,
			TimeoutSeconds: timeoutSeconds,
		}); err != nil {
			s.log.Warn("api: push llm config to diagnoser failed", "error", err)
			return &apiError{"settings saved, but failed to apply live: " + err.Error(), http.StatusInternalServerError}
		}
	}

	s.record(r.Context(), uid.New(), "integrations", audit.Stage("IntegrationConfigured"), "llm-saved", map[string]string{
		"provider": req.Provider,
		"model":    req.Model,
		"source":   "web-api",
	})

	return jsonOK(w, map[string]any{"saved": true, "provider": req.Provider})
}

// ---------------------------------------------------------------------------
// POST /api/v1/integrations/llm/test
// ---------------------------------------------------------------------------

func (s *Server) handleTestLLMIntegration(w http.ResponseWriter, r *http.Request) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	var req llmRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return &apiError{"invalid JSON", http.StatusBadRequest}
	}
	if req.Provider != "nim" && req.Provider != "gemini" {
		return &apiError{"provider must be 'nim' or 'gemini'", http.StatusBadRequest}
	}
	apiKey := ""
	if req.APIKey != nil {
		apiKey = *req.APIKey
	}
	if apiKey == "" {
		return &apiError{"api_key is required", http.StatusBadRequest}
	}

	if s.llm == nil {
		return &apiError{"diagnoser unavailable; cannot test LLM connectivity", http.StatusServiceUnavailable}
	}

	timeoutSeconds := req.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	result, err := s.llm.TestConfig(r.Context(), diagnosis.LLMConfig{
		Provider:       req.Provider,
		APIKey:         apiKey,
		Model:          req.Model,
		TimeoutSeconds: timeoutSeconds,
	})
	if err != nil {
		return jsonOK(w, diagnosis.LLMTestResult{OK: false, Message: "diagnoser unreachable: " + err.Error()})
	}
	return jsonOK(w, result)
}
