package config

import (
	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	OpenAIAPIKey  string `env:"OPENAI_API_KEY"`
	OpenAIBaseURL string `env:"OPENAI_BASE_URL" envDefault:"https://api.openai.com/v1"`
	OpenAIModel   string `env:"OPENAI_MODEL" envDefault:"gpt-4o"`
	ServerAddr    string `env:"SERVER_ADDR" envDefault:":8787"`
	DatabasePath  string `env:"DATABASE_PATH" envDefault:"./family.db"`
	AuthKey       string `env:"AUTH_KEY"`
}

func Load() (Config, error) {
	_ = godotenv.Load()
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) MaskedAPIKey() string {
	if len(c.OpenAIAPIKey) <= 4 {
		return "****"
	}
	return "****" + c.OpenAIAPIKey[len(c.OpenAIAPIKey)-4:]
}
