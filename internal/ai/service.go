package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/ai/aidb"
	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// providerSetting is the settings key of the provider configuration.
const providerSetting = "ai.provider"

// ErrDisabled is the answer of AI operations without a provider (REQ-AI-002).
var ErrDisabled = httpx.Errorf(http.StatusConflict, "ai_disabled", "no AI provider is configured")

// Secrets resolves global secrets. internal/app adapts the secret service.
type Secrets interface {
	Global(ctx context.Context, key string) (string, error)
}

// ProviderConfig is the one AI provider (REQ-AI-001). The API key is a global secret key,
// never a value.
type ProviderConfig struct {
	Type            string `json:"type"`
	BaseURL         string `json:"base_url"`
	Model           string `json:"model"`
	APIKeySecretKey string `json:"api_key_secret_key"`
	AutoTriage      bool   `json:"auto_triage"`
}

// Service is the AI feature.
type Service struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Audit   *audit.Writer
	Log     *slog.Logger
	Secrets Secrets
	// HTTP is the client of provider calls. Nil uses a client without timeout.
	HTTP *http.Client
	// MaxContextChars limits the model context (REQ-AI-008).
	MaxContextChars int
	// Data reads and changes Sluice objects for the tools and triage.
	Data Data
	// Version is the server version that MCP reports.
	Version string
}

var secretKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// validBaseURL allows https, and plain http only for loopback hosts (DI-33, DI-38).
func validBaseURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	return false
}

// Provider returns the provider configuration, or nil when none is configured.
func (s *Service) Provider(ctx context.Context) (*ProviderConfig, error) {
	b, err := aidb.New(s.Pool).GetSetting(ctx, providerSetting)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg ProviderConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// PutProvider validates and stores the provider configuration.
func (s *Service) PutProvider(ctx context.Context, cfg ProviderConfig) (ProviderConfig, error) {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	var fields []httpx.FieldError
	if cfg.Type != TypeAnthropic && cfg.Type != TypeOpenAICompatible {
		fields = append(fields, httpx.FieldError{Field: "type", Message: "use anthropic or openai_compatible"})
	}
	if !validBaseURL(cfg.BaseURL) {
		fields = append(fields, httpx.FieldError{Field: "base_url", Message: "use an https URL, or http only for a loopback host"})
	}
	if cfg.Model == "" {
		fields = append(fields, httpx.FieldError{Field: "model", Message: "is required"})
	}
	if !secretKeyRe.MatchString(cfg.APIKeySecretKey) {
		fields = append(fields, httpx.FieldError{Field: "api_key_secret_key", Message: "use a global secret key"})
	}
	if len(fields) > 0 {
		return ProviderConfig{}, httpx.Validation(fields...)
	}
	b, _ := json.Marshal(cfg)
	by := ""
	if p := kernel.FromContext(ctx); p != nil {
		by = p.Email
	}
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := aidb.New(tx).PutSetting(ctx, aidb.PutSettingParams{Key: providerSetting, Value: b, UpdatedBy: by, UpdatedAt: s.Clock.Now()}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "ai.provider.update", TargetType: "setting", TargetID: providerSetting,
			Details: map[string]any{"type": cfg.Type, "model": cfg.Model, "base_url": cfg.BaseURL, "auto_triage": cfg.AutoTriage}})
	})
	return cfg, err
}

// DeleteProvider removes the provider. AI operations then return ai_disabled.
func (s *Service) DeleteProvider(ctx context.Context) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		n, err := aidb.New(tx).DeleteSetting(ctx, providerSetting)
		if err != nil || n == 0 {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "ai.provider.delete", TargetType: "setting", TargetID: providerSetting})
	})
}

// Model returns the adapter of the configured provider, or ErrDisabled.
func (s *Service) Model(ctx context.Context) (Model, *ProviderConfig, error) {
	cfg, err := s.Provider(ctx)
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil {
		return nil, nil, ErrDisabled
	}
	key, err := s.Secrets.Global(ctx, cfg.APIKeySecretKey)
	if err != nil {
		return nil, nil, httpx.Errorf(http.StatusConflict, "ai_key_unavailable", "the API key secret %s does not resolve: %v", cfg.APIKeySecretKey, err)
	}
	m, err := NewModel(cfg.Type, cfg.BaseURL, cfg.Model, key, s.HTTP)
	if err != nil {
		return nil, nil, err
	}
	return m, cfg, nil
}

// TestProvider sends a short request to the provider. It returns ok, or failed with the
// error message.
func (s *Service) TestProvider(ctx context.Context) (string, string, error) {
	m, _, err := s.Model(ctx)
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Code == "ai_key_unavailable" {
			return "failed", he.Message, nil
		}
		return "", "", err
	}
	resp, cerr := m.Complete(ctx, Request{MaxTokens: 16, Messages: []Message{{Role: "user", Content: []Block{{Type: BlockText, Text: "Reply with the word OK."}}}}}, nil)
	if cerr != nil {
		return testFailed(cerr)
	}
	return "ok", strings.TrimSpace(resp.Text), nil
}

// testFailed is the result of a failed provider test. The failure is the test result, not
// an error of the operation.
func testFailed(err error) (string, string, error) { return "failed", err.Error(), nil }
