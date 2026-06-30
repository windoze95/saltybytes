package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
)

// AIModelOptionRepo is the persistence surface the model manager needs. Backed
// by repository.AIModelOptionRepository in production; faked in tests.
type AIModelOptionRepo interface {
	ListOptions() ([]models.AIModelOption, error)
	GetOption(id uint) (*models.AIModelOption, error)
	CreateOption(opt *models.AIModelOption) error
	UpdateOption(opt *models.AIModelOption) error
	DeleteOption(id uint) error
	GetConfig() (*models.AIConfig, error)
	UpsertConfig(cfg *models.AIConfig) error
}

// AIModelManager owns the live light-tier model selection. It exposes a single
// SwitchableTextProvider (handed to the import/normalize services) and drives
// what that provider points at:
//
//   - Load: on boot, seed the registry + active config from the env default,
//     then apply whatever the DB says is active.
//   - StartRefresh: poll the DB so a switch made on one instance propagates to
//     the others (the API runs multiple ECS tasks).
//   - Activate: validate a candidate with a live probe and, only on success,
//     persist it as active and switch the running provider (fail-closed).
//
// API keys live in LightKeys (from env/SSM), never in the DB.
type AIModelManager struct {
	repo     AIModelOptionRepo
	keys     ai.LightKeys
	prompts  *config.Prompts
	mw       ai.AIMiddleware
	sw       *ai.SwitchableTextProvider
	fallback ai.LightProviderSpec

	// validate runs the live probe; overridable in tests to avoid network.
	validate func(ctx context.Context, spec ai.LightProviderSpec) error

	mu     sync.RWMutex
	active ai.LightProviderSpec
}

// NewAIModelManager builds the manager and its initial provider from the env
// default spec. If that spec can't be built (e.g. a non-anthropic provider was
// selected without its key), it falls back to the Anthropic Haiku default so
// the app always boots with a working light tier.
func NewAIModelManager(repo AIModelOptionRepo, keys ai.LightKeys, prompts *config.Prompts, mw ai.AIMiddleware, fallback ai.LightProviderSpec) *AIModelManager {
	m := &AIModelManager{
		repo:     repo,
		keys:     keys,
		prompts:  prompts,
		mw:       mw,
		fallback: fallback,
	}
	m.validate = func(ctx context.Context, spec ai.LightProviderSpec) error {
		return ai.ValidateModel(ctx, spec, m.keys, m.prompts)
	}

	provider, err := ai.BuildLightProvider(fallback, keys, prompts, mw)
	if err != nil {
		logger.Get().Warn("ai model manager: env light provider unbuildable, falling back to anthropic",
			zap.String("provider", fallback.Provider), zap.Error(err))
		fallback = ai.LightProviderSpec{Provider: "anthropic"}
		m.fallback = fallback
		provider, _ = ai.BuildLightProvider(fallback, keys, prompts, mw)
	}
	m.sw = ai.NewSwitchableTextProvider(provider)
	m.active = fallback
	return m
}

// Provider returns the switchable TextProvider to hand to dependent services.
func (m *AIModelManager) Provider() ai.TextProvider { return m.sw }

// GetActive returns the spec currently driving the light tier.
func (m *AIModelManager) GetActive() ai.LightProviderSpec {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// Load seeds the registry/active-config from the env default on first boot and
// then applies whatever the DB marks active. Best-effort: any DB error is
// logged and leaves the env-default provider running.
func (m *AIModelManager) Load(ctx context.Context) {
	if opts, err := m.repo.ListOptions(); err != nil {
		logger.Get().Warn("ai model manager: list options failed during load", zap.Error(err))
	} else if len(opts) == 0 {
		m.seedDefaults()
	}

	cfg, err := m.repo.GetConfig()
	if err != nil {
		logger.Get().Warn("ai model manager: get config failed during load", zap.Error(err))
		return
	}
	if cfg == nil || cfg.ActiveProvider == "" {
		// No active selection yet — persist the env default as active.
		if err := m.repo.UpsertConfig(&models.AIConfig{
			ActiveProvider: m.fallback.Provider,
			ActiveModel:    m.fallback.Model,
			ActiveBaseURL:  m.fallback.BaseURL,
		}); err != nil {
			logger.Get().Warn("ai model manager: seed active config failed", zap.Error(err))
		}
		return
	}

	spec := ai.LightProviderSpec{Provider: cfg.ActiveProvider, Model: cfg.ActiveModel, BaseURL: cfg.ActiveBaseURL}
	if err := m.applySpec(spec); err != nil {
		logger.Get().Warn("ai model manager: DB-active spec unbuildable, keeping env default",
			zap.String("provider", spec.Provider), zap.String("model", spec.Model), zap.Error(err))
	}
}

// StartRefresh polls the active config every interval and applies a change made
// elsewhere (another instance's live switch). It does NOT re-probe — the spec
// was already validated when written — it only rebuilds the provider.
func (m *AIModelManager) StartRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.Refresh()
			}
		}
	}()
}

// Refresh reconciles the running provider with the DB's active config.
func (m *AIModelManager) Refresh() {
	cfg, err := m.repo.GetConfig()
	if err != nil || cfg == nil || cfg.ActiveProvider == "" {
		return
	}
	spec := ai.LightProviderSpec{Provider: cfg.ActiveProvider, Model: cfg.ActiveModel, BaseURL: cfg.ActiveBaseURL}

	m.mu.RLock()
	unchanged := spec == m.active
	m.mu.RUnlock()
	if unchanged {
		return
	}

	if err := m.applySpec(spec); err != nil {
		logger.Get().Warn("ai model manager: refresh could not apply active spec, keeping current",
			zap.String("provider", spec.Provider), zap.String("model", spec.Model), zap.Error(err))
		return
	}
	logger.Get().Info("ai model manager: light tier switched via DB config",
		zap.String("provider", spec.Provider), zap.String("model", spec.Model))
}

// applySpec builds the provider for spec and atomically swaps it in.
func (m *AIModelManager) applySpec(spec ai.LightProviderSpec) error {
	provider, err := ai.BuildLightProvider(spec, m.keys, m.prompts, m.mw)
	if err != nil {
		return err
	}
	m.sw.Set(provider)
	m.mu.Lock()
	m.active = spec
	m.mu.Unlock()
	return nil
}

// ListModels returns the registry.
func (m *AIModelManager) ListModels() ([]models.AIModelOption, error) {
	return m.repo.ListOptions()
}

// AddModel validates a candidate with a live probe, then persists it with the
// probe outcome recorded. The option is saved even when the probe fails (so the
// operator sees it in the list, marked invalid with the error) — only a real
// DB write failure returns an error here.
func (m *AIModelManager) AddModel(ctx context.Context, opt *models.AIModelOption) error {
	spec := ai.LightProviderSpec{Provider: opt.Provider, Model: opt.ModelID, BaseURL: opt.BaseURL}
	now := time.Now()
	opt.LastValidatedAt = &now
	if err := m.validate(ctx, spec); err != nil {
		opt.Validated = false
		opt.ValidationError = err.Error()
	} else {
		opt.Validated = true
		opt.ValidationError = ""
	}
	return m.repo.CreateOption(opt)
}

// UpdateModel applies editable fields to an option. If the model identity
// (provider/model/base URL) changed it re-runs the validation probe.
func (m *AIModelManager) UpdateModel(ctx context.Context, id uint, in *models.AIModelOption) (*models.AIModelOption, error) {
	existing, err := m.repo.GetOption(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("model option %d not found", id)
	}

	identityChanged := existing.Provider != in.Provider || existing.ModelID != in.ModelID || existing.BaseURL != in.BaseURL

	existing.Provider = in.Provider
	existing.ModelID = in.ModelID
	existing.BaseURL = in.BaseURL
	existing.Label = in.Label
	existing.InputPricePerMTok = in.InputPricePerMTok
	existing.OutputPricePerMTok = in.OutputPricePerMTok
	existing.Enabled = in.Enabled

	if identityChanged {
		spec := ai.LightProviderSpec{Provider: existing.Provider, Model: existing.ModelID, BaseURL: existing.BaseURL}
		now := time.Now()
		existing.LastValidatedAt = &now
		if verr := m.validate(ctx, spec); verr != nil {
			existing.Validated = false
			existing.ValidationError = verr.Error()
		} else {
			existing.Validated = true
			existing.ValidationError = ""
		}
	}

	if err := m.repo.UpdateOption(existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// DeleteModel removes an option. Safe even if it is the active one — the active
// config stores the resolved spec independently, so the running provider is
// unaffected.
func (m *AIModelManager) DeleteModel(id uint) error {
	return m.repo.DeleteOption(id)
}

// Activate switches the live light tier to the given option — probe FIRST, and
// only on a green probe persist it as active and swap the running provider. A
// failed probe records the failure on the option and returns an error WITHOUT
// touching the active selection (fail-closed: a bad model can never go live).
func (m *AIModelManager) Activate(ctx context.Context, id uint) (*models.AIModelOption, error) {
	opt, err := m.repo.GetOption(id)
	if err != nil {
		return nil, err
	}
	if opt == nil {
		return nil, fmt.Errorf("model option %d not found", id)
	}

	spec := ai.LightProviderSpec{Provider: opt.Provider, Model: opt.ModelID, BaseURL: opt.BaseURL}
	now := time.Now()
	opt.LastValidatedAt = &now

	if verr := m.validate(ctx, spec); verr != nil {
		opt.Validated = false
		opt.ValidationError = verr.Error()
		_ = m.repo.UpdateOption(opt)
		return nil, fmt.Errorf("validation failed, active model unchanged: %w", verr)
	}

	opt.Validated = true
	opt.ValidationError = ""
	_ = m.repo.UpdateOption(opt)

	if err := m.applySpec(spec); err != nil {
		return nil, fmt.Errorf("model validated but provider build failed: %w", err)
	}
	if err := m.repo.UpsertConfig(&models.AIConfig{
		ActiveProvider: spec.Provider,
		ActiveModel:    spec.Model,
		ActiveBaseURL:  spec.BaseURL,
	}); err != nil {
		logger.Get().Warn("ai model manager: activated provider but failed to persist active config",
			zap.String("provider", spec.Provider), zap.String("model", spec.Model), zap.Error(err))
	}
	logger.Get().Info("ai model manager: light tier activated",
		zap.String("provider", spec.Provider), zap.String("model", spec.Model))
	return opt, nil
}

// seedDefaults populates an empty registry with a curated set of light-tier
// candidates so the dashboard has something to compare/switch out of the box.
// The entry matching the env default is marked validated (it is what's running);
// the rest are left unvalidated until probed via the dashboard.
func (m *AIModelManager) seedDefaults() {
	type seed struct{ provider, model, label string }
	anthropicModel := m.keys.AnthropicLightModel
	if anthropicModel == "" {
		anthropicModel = ai.DefaultAnthropicLightModel
	}
	seeds := []seed{
		{"anthropic", anthropicModel, "Claude Haiku 4.5"},
		{"openai", "gpt-4o-mini", "GPT-4o mini"},
		{"gemini", "gemini-2.0-flash", "Gemini 2.0 Flash"},
		{"deepseek", "deepseek-chat", "DeepSeek Chat"},
	}
	for _, s := range seeds {
		opt := &models.AIModelOption{
			Provider: s.provider,
			ModelID:  s.model,
			Label:    s.label,
			Enabled:  true,
		}
		if mp, ok := ai.DefaultPricing.Lookup(s.model); ok {
			opt.InputPricePerMTok = mp.InputPerM
			opt.OutputPricePerMTok = mp.OutputPerM
		}
		// Reflect the running default as already-validated.
		if s.provider == m.fallback.Provider && (m.fallback.Model == "" || m.fallback.Model == s.model) {
			opt.Validated = true
		}
		if err := m.repo.CreateOption(opt); err != nil {
			logger.Get().Warn("ai model manager: seed option failed",
				zap.String("provider", s.provider), zap.String("model", s.model), zap.Error(err))
		}
	}
}
