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

	// SMTP：摘要推送 email 通道；未配置 SMTP_HOST 时 email 发送禁用
	SMTPHost string `env:"SMTP_HOST"`
	SMTPPort int    `env:"SMTP_PORT" envDefault:"587"`
	SMTPUser string `env:"SMTP_USER"`
	SMTPPass string `env:"SMTP_PASS"`
	SMTPFrom string `env:"SMTP_FROM"`
}

// SMTPConfigured email 通道是否可用
func (c Config) SMTPConfigured() bool {
	return c.SMTPHost != "" && c.SMTPFrom != ""
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
