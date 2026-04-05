package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// PromptPair holds a system and user prompt template.
// SystemPrefix is an optional static preamble that is split from the dynamic
// System template so that Anthropic prompt caching can cache the stable prefix
// without being invalidated by per-request template variables.
type PromptPair struct {
	SystemPrefix string `yaml:"system_prefix"`
	System       string `yaml:"system"`
	User         string `yaml:"user"`
}

// SummarizePrompts holds summarization prompt templates.
type SummarizePrompts struct {
	Recipe  string `yaml:"recipe"`
	Changes string `yaml:"changes"`
}

// ImportPrompts holds import-related prompt templates.
type ImportPrompts struct {
	Vision PromptPair `yaml:"vision"`
	Text   PromptPair `yaml:"text"`
	URL    PromptPair `yaml:"url"`
}

// RecipePrompts holds all recipe-related prompt templates.
type RecipePrompts struct {
	Generate   PromptPair       `yaml:"generate"`
	Fork       PromptPair       `yaml:"fork"`
	Regenerate PromptPair       `yaml:"regenerate"`
	Summarize  SummarizePrompts `yaml:"summarize"`
	Import     ImportPrompts    `yaml:"import"`
}

// AllergenPrompts holds allergen-related prompt templates.
type AllergenPrompts struct {
	Analyze PromptPair `yaml:"analyze"`
}

// VoicePrompts holds voice-related prompt templates.
type VoicePrompts struct {
	Intent PromptPair `yaml:"intent"`
}

// SinglePrompt holds a single system prompt (no user template).
// SystemPrefix is an optional static preamble (see PromptPair).
type SinglePrompt struct {
	SystemPrefix string `yaml:"system_prefix"`
	System       string `yaml:"system"`
}

// Prompts is the top-level prompt configuration loaded from YAML.
type Prompts struct {
	Recipe           RecipePrompts   `yaml:"recipe"`
	Allergen         AllergenPrompts `yaml:"allergen"`
	Voice            VoicePrompts    `yaml:"voice"`
	CookingQA        SinglePrompt    `yaml:"cooking_qa"`
	DietaryInterview SinglePrompt    `yaml:"dietary_interview"`
	Import           ImportPrompts   `yaml:"import"`
}

// LoadPrompts reads and parses a YAML prompt configuration file.
func LoadPrompts(path string) (*Prompts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read prompts file: %w", err)
	}

	var prompts Prompts
	if err := yaml.Unmarshal(data, &prompts); err != nil {
		return nil, fmt.Errorf("failed to parse prompts YAML: %w", err)
	}

	return &prompts, nil
}

// RenderPrompt executes Go template interpolation on a prompt string.
// The data map provides values for template placeholders like {{.Prompt}},
// {{.UnitSystem}}, and {{.Requirements}}.
func RenderPrompt(tmpl string, data map[string]interface{}) (string, error) {
	t, err := template.New("prompt").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse prompt template: %w", err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render prompt template: %w", err)
	}

	return strings.TrimSpace(buf.String()), nil
}
