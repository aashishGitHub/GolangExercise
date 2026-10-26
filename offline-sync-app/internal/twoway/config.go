package twoway

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	System           string
	ExternalBaseURL  string
	WebhookAddr      string
	WebhookSecret    string
	WebhookTolerance time.Duration
	MaxBodyBytes     int64
	MaxAttempts      int
	ExtractBudget    time.Duration
	ExtractOverlap   time.Duration
	ExtractPageSize  int
}

// LoadConfig reads the environment; DATABASE_URL stays in internal/config.
// WEBHOOK_SECRET has no default: a receiver with a guessable secret is worse
// than one that refuses to start.
func LoadConfig() (Config, error) {
	cfg := Config{
		System:          getenv("EXTERNAL_SYSTEM", "mockjira"),
		ExternalBaseURL: getenv("EXTERNAL_BASE_URL", "http://localhost:8090"),
		WebhookAddr:     getenv("WEBHOOK_ADDR", ":8081"),
		WebhookSecret:   os.Getenv("WEBHOOK_SECRET"),
	}
	if cfg.WebhookSecret == "" {
		return Config{}, fmt.Errorf("WEBHOOK_SECRET is required")
	}

	var err error
	if cfg.WebhookTolerance, err = durationEnv("WEBHOOK_TOLERANCE", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.ExtractBudget, err = durationEnv("EXTRACT_BUDGET", 10*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.ExtractOverlap, err = durationEnv("EXTRACT_OVERLAP", 5*time.Minute); err != nil {
		return Config{}, err
	}
	maxBody, err := intEnv("WEBHOOK_MAX_BODY_BYTES", 1<<20)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxBodyBytes = int64(maxBody)
	if cfg.MaxAttempts, err = intEnv("SYNC_MAX_ATTEMPTS", 5); err != nil {
		return Config{}, err
	}
	if cfg.ExtractPageSize, err = intEnv("EXTRACT_PAGE_SIZE", 100); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := getenv(key, "")
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func intEnv(key string, fallback int) (int, error) {
	v := getenv(key, "")
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, v)
	}
	return n, nil
}
