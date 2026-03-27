package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	// Database
	DatabaseURL       string
	DatabaseDirectURL string

	// Security
	EncryptionKey string

	// Shopify
	ShopifyClientID      string
	ShopifySecretKey     string
	ShopifyWebhookSecret string
	ShopifyAppHandle     string

	// AI platforms
	OpenAIKey      string
	PerplexityKey  string
	GeminiKey      string
	AnthropicKey   string

	// Stripe (optional)
	StripeSecretKey      string
	StripeWebhookSecret  string

	// App
	Port            string
	Environment     string
	AppURL          string
	ScanWorkerCount int
	ScanBatchSize   int
}

// Load reads the environment (and optional .env file) into a Config.
// Returns an error if any required variable is missing.
func Load() (*Config, error) {
	// Load .env if present; ignore error (file optional in production)
	_ = godotenv.Load()

	cfg := &Config{
		// Required
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		DatabaseDirectURL:    os.Getenv("DATABASE_DIRECT_URL"),
		EncryptionKey:        os.Getenv("ENCRYPTION_KEY"),
		ShopifyClientID:      os.Getenv("SHOPIFY_CLIENT_ID"),
		ShopifySecretKey:     os.Getenv("SHOPIFY_SECRET_KEY"),
		ShopifyWebhookSecret: os.Getenv("SHOPIFY_WEBHOOK_SECRET"),
		OpenAIKey:            os.Getenv("OPENAI_KEY"),
		PerplexityKey:        os.Getenv("PERPLEXITY_KEY"),
		GeminiKey:            os.Getenv("GEMINI_KEY"),
		AnthropicKey:         os.Getenv("ANTHROPIC_KEY"),

		// Optional with defaults
		Port:             envOrDefault("PORT", "8081"),
		Environment:      envOrDefault("ENVIRONMENT", "development"),
		AppURL:           envOrDefault("APP_URL", "https://geo-visibility-eight.vercel.app"),
		ShopifyAppHandle: envOrDefault("SHOPIFY_APP_HANDLE", "geo-visibility"),

		// Optional billing
		StripeSecretKey:     os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
	}

	cfg.ScanWorkerCount = envInt("SCAN_WORKER_COUNT", 25)
	cfg.ScanBatchSize = envInt("SCAN_BATCH_SIZE", 20)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	required := map[string]string{
		"DATABASE_URL":           c.DatabaseURL,
		"DATABASE_DIRECT_URL":    c.DatabaseDirectURL,
		"ENCRYPTION_KEY":         c.EncryptionKey,
		"SHOPIFY_CLIENT_ID":      c.ShopifyClientID,
		"SHOPIFY_SECRET_KEY":     c.ShopifySecretKey,
		"SHOPIFY_WEBHOOK_SECRET": c.ShopifyWebhookSecret,
		"OPENAI_KEY":             c.OpenAIKey,
		"PERPLEXITY_KEY":         c.PerplexityKey,
		"GEMINI_KEY":             c.GeminiKey,
		"ANTHROPIC_KEY":          c.AnthropicKey,
	}
	for name, val := range required {
		if val == "" {
			return fmt.Errorf("config: required env var %s is not set", name)
		}
	}
	if len(c.EncryptionKey) != 32 {
		return fmt.Errorf("config: ENCRYPTION_KEY must be exactly 32 characters, got %d", len(c.EncryptionKey))
	}
	return nil
}

// ShutdownTimeout returns the graceful shutdown deadline.
func (c *Config) ShutdownTimeout() time.Duration {
	return 30 * time.Second
}

func (c *Config) IsProd() bool {
	return c.Environment == "production"
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
