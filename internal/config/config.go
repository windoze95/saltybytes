package config

import (
	"fmt"
	"reflect"

	"github.com/caarlos0/env/v11"
)

// Config holds the application configuration.
type Config struct {
	EnvVars EnvVars  `json:"env"`
	Prompts *Prompts `json:"-"`
}

// EnvVars holds environment variables required by the application.
// Fields tagged `optional:"true"` are skipped by CheckConfigEnvFields.
type EnvVars struct {
	Port               string `env:"PORT" envDefault:"8080"`
	DatabaseUrl        string `env:"DATABASE_URL"`
	JwtSecretKey       string `env:"JWT_SECRET_KEY"`
	AWSRegion          string `env:"AWS_REGION"`
	AWSAccessKeyID     string `env:"AWS_ACCESS_KEY_ID" optional:"true"`
	AWSSecretAccessKey string `env:"AWS_SECRET_ACCESS_KEY" optional:"true"`
	S3Bucket           string `env:"S3_BUCKET"`
	IDHeader           string `env:"ID_HEADER"`
	AnthropicAPIKey    string `env:"ANTHROPIC_API_KEY"`
	OpenAIAPIKey       string `env:"OPENAI_API_KEY"`
	GoogleSearchKey    string `env:"GOOGLE_SEARCH_KEY" optional:"true"`
	GoogleSearchCX     string `env:"GOOGLE_SEARCH_CX" optional:"true"`
	BraveSearchKey     string `env:"BRAVE_SEARCH_KEY" optional:"true"`
}

// LoadConfig parses environment variables into the Config struct.
func LoadConfig() (*Config, error) {
	var config Config
	if err := env.Parse(&config.EnvVars); err != nil {
		return nil, err
	}
	return &config, nil
}

// CheckConfigEnvFields validates that all required EnvVars fields are set.
func (c *Config) CheckConfigEnvFields() error {
	return checkFieldsRecursive(reflect.ValueOf(c.EnvVars))
}

func checkFieldsRecursive(v reflect.Value) error {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := v.Type().Field(i)
		if fieldType.Tag.Get("optional") == "true" {
			continue
		}
		if isZeroValue(field) {
			return fmt.Errorf("$%s must be set", fieldType.Name)
		}
		if field.Kind() == reflect.Struct {
			if err := checkFieldsRecursive(field); err != nil {
				return err
			}
		}
	}
	return nil
}

func isZeroValue(v reflect.Value) bool {
	return v.Interface() == reflect.Zero(v.Type()).Interface()
}
