package ai

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// AIProvider is the provider configuration of the settings page.
type AIProvider struct {
	Configured      bool   `json:"configured"`
	Type            string `json:"type,omitempty" enum:"anthropic,openai_compatible"`
	BaseURL         string `json:"base_url,omitempty" doc:"Empty uses the default URL of the type."`
	Model           string `json:"model,omitempty"`
	APIKeySecretKey string `json:"api_key_secret_key,omitempty" doc:"Global secret key that holds the API key."`
	AutoTriage      bool   `json:"auto_triage"`
}

// AIProviderPut is the body of a provider update.
type AIProviderPut struct {
	Type            string `json:"type" enum:"anthropic,openai_compatible"`
	BaseURL         string `json:"base_url,omitempty" maxLength:"512"`
	Model           string `json:"model" minLength:"1" maxLength:"200"`
	APIKeySecretKey string `json:"api_key_secret_key" pattern:"^[A-Za-z_][A-Za-z0-9_]{0,127}$"`
	AutoTriage      bool   `json:"auto_triage,omitempty"`
}

// AIStatus tells the UI whether AI actions show (REQ-AI-002).
type AIStatus struct {
	Enabled    bool `json:"enabled"`
	AutoTriage bool `json:"auto_triage"`
}

// InsightList is the list of insights of an execution, newest first.
type InsightList struct {
	Items []Insight `json:"items"`
}

// enabled returns ErrDisabled without a provider (REQ-AI-002).
func (s *Service) enabled(ctx context.Context) error {
	cfg, err := s.Provider(ctx)
	if err != nil {
		return err
	}
	if cfg == nil {
		return ErrDisabled
	}
	return nil
}

// AITestResult is the result of a provider test.
type AITestResult struct {
	Status  string `json:"status" enum:"ok,failed"`
	Message string `json:"message,omitempty"`
}

func providerOut(cfg *ProviderConfig) AIProvider {
	if cfg == nil {
		return AIProvider{}
	}
	return AIProvider{Configured: true, Type: cfg.Type, BaseURL: cfg.BaseURL, Model: cfg.Model, APIKeySecretKey: cfg.APIKeySecretKey, AutoTriage: cfg.AutoTriage}
}

// Routes registers the AI operations. Registration does not use s: `sluice openapi`
// passes nil.
func Routes(api huma.API, r chi.Router, s *Service) {
	viewer := httpx.MinRole(kernel.Viewer)
	admin := httpx.MinRole(kernel.Admin)
	r.Handle(MCPPath, mcpHandler(s))
	registerAssistant(api, r, s)

	huma.Register(api, httpx.Op("getAIStatus", http.MethodGet, "/api/v1/ai/status", viewer),
		func(ctx context.Context, _ *struct{}) (*struct{ Body AIStatus }, error) {
			cfg, err := s.Provider(ctx)
			if err != nil {
				return nil, err
			}
			out := AIStatus{Enabled: cfg != nil}
			if cfg != nil {
				out.AutoTriage = cfg.AutoTriage
			}
			return &struct{ Body AIStatus }{Body: out}, nil
		})

	huma.Register(api, httpx.Op("getAIProvider", http.MethodGet, "/api/v1/ai/provider", admin),
		func(ctx context.Context, _ *struct{}) (*struct{ Body AIProvider }, error) {
			cfg, err := s.Provider(ctx)
			if err != nil {
				return nil, err
			}
			return &struct{ Body AIProvider }{Body: providerOut(cfg)}, nil
		})

	huma.Register(api, httpx.Op("putAIProvider", http.MethodPut, "/api/v1/ai/provider", admin),
		func(ctx context.Context, in *struct{ Body AIProviderPut }) (*struct{ Body AIProvider }, error) {
			cfg, err := s.PutProvider(ctx, ProviderConfig{Type: in.Body.Type, BaseURL: in.Body.BaseURL, Model: in.Body.Model,
				APIKeySecretKey: in.Body.APIKeySecretKey, AutoTriage: in.Body.AutoTriage})
			if err != nil {
				return nil, err
			}
			return &struct{ Body AIProvider }{Body: providerOut(&cfg)}, nil
		})

	del := httpx.Op("deleteAIProvider", http.MethodDelete, "/api/v1/ai/provider", admin)
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		return nil, s.DeleteProvider(ctx)
	})

	type executionIn struct {
		ExecutionID uuid.UUID `path:"executionId"`
	}
	huma.Register(api, httpx.Op("listExecutionInsights", http.MethodGet, "/api/v1/executions/{executionId}/insights", viewer),
		func(ctx context.Context, in *executionIn) (*struct{ Body InsightList }, error) {
			if err := s.enabled(ctx); err != nil {
				return nil, err
			}
			if _, err := s.Data.ExecutionState(ctx, in.ExecutionID); err != nil {
				return nil, err
			}
			list, err := s.Insights(ctx, in.ExecutionID)
			if err != nil {
				return nil, err
			}
			return &struct{ Body InsightList }{Body: InsightList{Items: list}}, nil
		})

	request := httpx.Op("requestTriage", http.MethodPost, "/api/v1/executions/{executionId}/insights", httpx.MinRole(kernel.Operator))
	request.DefaultStatus = http.StatusAccepted
	huma.Register(api, request, func(ctx context.Context, in *executionIn) (*struct{ Body InsightList }, error) {
		state, err := s.Data.ExecutionState(ctx, in.ExecutionID)
		if err != nil {
			return nil, err
		}
		if err := s.RequestTriage(ctx, in.ExecutionID, state); err != nil {
			return nil, err
		}
		list, err := s.Insights(ctx, in.ExecutionID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body InsightList }{Body: InsightList{Items: list}}, nil
	})

	huma.Register(api, httpx.Op("testAIProvider", http.MethodPost, "/api/v1/ai/provider/test", admin),
		func(ctx context.Context, _ *struct{}) (*struct{ Body AITestResult }, error) {
			st, msg, err := s.TestProvider(ctx)
			if err != nil {
				return nil, err
			}
			return &struct{ Body AITestResult }{Body: AITestResult{Status: st, Message: msg}}, nil
		})
}
