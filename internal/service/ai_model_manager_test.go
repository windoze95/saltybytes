package service

import (
	"context"
	"errors"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
)

// fakeOptionRepo is an in-memory AIModelOptionRepo for manager tests.
type fakeOptionRepo struct {
	opts   []models.AIModelOption
	nextID uint
	cfg    *models.AIConfig
}

func newFakeOptionRepo() *fakeOptionRepo { return &fakeOptionRepo{nextID: 1} }

func (r *fakeOptionRepo) ListOptions() ([]models.AIModelOption, error) {
	out := make([]models.AIModelOption, len(r.opts))
	copy(out, r.opts)
	return out, nil
}

func (r *fakeOptionRepo) GetOption(id uint) (*models.AIModelOption, error) {
	for i := range r.opts {
		if r.opts[i].ID == id {
			cp := r.opts[i]
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeOptionRepo) CreateOption(opt *models.AIModelOption) error {
	opt.ID = r.nextID
	r.nextID++
	r.opts = append(r.opts, *opt)
	return nil
}

func (r *fakeOptionRepo) UpdateOption(opt *models.AIModelOption) error {
	for i := range r.opts {
		if r.opts[i].ID == opt.ID {
			r.opts[i] = *opt
			return nil
		}
	}
	return errors.New("not found")
}

func (r *fakeOptionRepo) DeleteOption(id uint) error {
	for i := range r.opts {
		if r.opts[i].ID == id {
			r.opts = append(r.opts[:i], r.opts[i+1:]...)
			return nil
		}
	}
	return nil
}

func (r *fakeOptionRepo) GetConfig() (*models.AIConfig, error) { return r.cfg, nil }

func (r *fakeOptionRepo) UpsertConfig(cfg *models.AIConfig) error {
	cfg.ID = 1
	cp := *cfg
	r.cfg = &cp
	return nil
}

// newTestManager builds a manager with all keys present (so any provider builds)
// and an anthropic fallback. The validator is overridden per-test.
func newTestManager(repo AIModelOptionRepo) *AIModelManager {
	keys := ai.LightKeys{
		AnthropicAPIKey: "k", AnthropicLightModel: ai.DefaultAnthropicLightModel,
		OpenAIAPIKey: "k", GeminiAPIKey: "k", DeepSeekAPIKey: "k",
	}
	return NewAIModelManager(repo, keys, &config.Prompts{}, nil, ai.LightProviderSpec{Provider: "anthropic"})
}

func TestAIModelManager_LoadSeedsDefaultsAndActiveConfig(t *testing.T) {
	repo := newFakeOptionRepo()
	m := newTestManager(repo)
	m.Load(context.Background())

	opts, _ := repo.ListOptions()
	if len(opts) != 4 {
		t.Fatalf("expected 4 seeded options, got %d", len(opts))
	}
	if repo.cfg == nil || repo.cfg.ActiveProvider != "anthropic" {
		t.Fatalf("expected active config seeded to anthropic, got %+v", repo.cfg)
	}
}

func TestAIModelManager_ActivateSuccessSwitchesAndPersists(t *testing.T) {
	repo := newFakeOptionRepo()
	m := newTestManager(repo)
	m.validate = func(context.Context, ai.LightProviderSpec) error { return nil } // green probe

	opt := &models.AIModelOption{Provider: "openai", ModelID: "gpt-4o-mini", Label: "GPT-4o mini", Enabled: true}
	_ = repo.CreateOption(opt)

	got, err := m.Activate(context.Background(), opt.ID)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !got.Validated {
		t.Errorf("activated option should be validated")
	}
	if active := m.GetActive(); active.Provider != "openai" || active.Model != "gpt-4o-mini" {
		t.Errorf("active = %+v, want openai/gpt-4o-mini", active)
	}
	if repo.cfg == nil || repo.cfg.ActiveProvider != "openai" || repo.cfg.ActiveModel != "gpt-4o-mini" {
		t.Errorf("persisted config = %+v, want openai/gpt-4o-mini", repo.cfg)
	}
}

func TestAIModelManager_ActivateFailedProbeIsFailClosed(t *testing.T) {
	repo := newFakeOptionRepo()
	m := newTestManager(repo)
	m.validate = func(context.Context, ai.LightProviderSpec) error { return errors.New("model_not_found") }

	before := m.GetActive()

	opt := &models.AIModelOption{Provider: "openai", ModelID: "bogus-model", Enabled: true}
	_ = repo.CreateOption(opt)

	_, err := m.Activate(context.Background(), opt.ID)
	if err == nil {
		t.Fatal("expected Activate to fail on a red probe")
	}
	// Active model must be unchanged.
	if after := m.GetActive(); after != before {
		t.Errorf("active changed on failed probe: before=%+v after=%+v", before, after)
	}
	// Active config must NOT have been written.
	if repo.cfg != nil {
		t.Errorf("active config should not be set after a failed probe, got %+v", repo.cfg)
	}
	// The option should be recorded as invalid with the error.
	stored, _ := repo.GetOption(opt.ID)
	if stored.Validated || stored.ValidationError == "" {
		t.Errorf("failed option should be marked invalid with an error, got %+v", stored)
	}
}

func TestAIModelManager_AddModelSavesProbeOutcome(t *testing.T) {
	repo := newFakeOptionRepo()
	m := newTestManager(repo)
	m.validate = func(context.Context, ai.LightProviderSpec) error { return errors.New("unreachable") }

	opt := &models.AIModelOption{Provider: "gemini", ModelID: "gemini-2.0-flash", Enabled: true}
	if err := m.AddModel(context.Background(), opt); err != nil {
		t.Fatalf("AddModel should still save on a failed probe: %v", err)
	}
	stored, _ := repo.GetOption(opt.ID)
	if stored == nil {
		t.Fatal("option was not saved")
	}
	if stored.Validated || stored.ValidationError == "" {
		t.Errorf("expected unvalidated option with error, got %+v", stored)
	}
}
