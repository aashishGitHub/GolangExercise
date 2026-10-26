package twoway

import (
	"testing"
	"time"
)

func TestLoadConfig_RequiresSecret(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "")

	if _, err := LoadConfig(); err == nil {
		t.Error("LoadConfig() error = nil, want an error when WEBHOOK_SECRET is unset")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "s")

	cfg, err := LoadConfig()

	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.System != "mockjira" || cfg.WebhookTolerance != 5*time.Minute || cfg.MaxAttempts != 5 || cfg.MaxBodyBytes != 1<<20 {
		t.Errorf("cfg = %+v, want the documented defaults", cfg)
	}
}

func TestLoadConfig_RejectsMalformedValues(t *testing.T) {
	for _, tt := range []struct{ key, value string }{
		{"WEBHOOK_TOLERANCE", "five minutes"},
		{"SYNC_MAX_ATTEMPTS", "0"},
		{"EXTRACT_PAGE_SIZE", "many"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			t.Setenv("WEBHOOK_SECRET", "s")
			t.Setenv(tt.key, tt.value)

			if _, err := LoadConfig(); err == nil {
				t.Errorf("LoadConfig() with %s=%q: error = nil, want an error", tt.key, tt.value)
			}
		})
	}
}
