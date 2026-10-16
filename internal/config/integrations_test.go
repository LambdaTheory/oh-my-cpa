package config

import (
	"testing"
	"time"
)

// TestFeishuEnvOverlayIsTriState pins the override contract: an unset switch leaves
// the web setting in force, an explicit false is an environment off, and an empty
// string is unset rather than a value that pins the feature.
func TestFeishuEnvOverlayIsTriState(t *testing.T) {
	clearConfigEnv(t)
	unsetEnv(t, "OMCPA_FEISHU_ENABLED")
	unsetEnv(t, "OMCPA_FEISHU_FORCE_DISABLED")
	unsetEnv(t, "OMCPA_FEISHU_APP_ID")
	unsetEnv(t, "OMCPA_FEISHU_APP_SECRET")
	unsetEnv(t, "OMCPA_FEISHU_CHAT_ID")

	cfg, err := loadFeishuConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled != nil || cfg.ForceDisabled || cfg.AppID != nil || cfg.AppSecret != nil || cfg.ChatID != nil {
		t.Fatalf("unset environment = %+v", cfg)
	}

	t.Setenv("OMCPA_FEISHU_ENABLED", "false")
	cfg, err = loadFeishuConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled == nil || *cfg.Enabled {
		t.Fatalf("explicit false = %+v", cfg)
	}

	t.Setenv("OMCPA_FEISHU_ENABLED", "")
	t.Setenv("OMCPA_FEISHU_FORCE_DISABLED", "true")
	t.Setenv("OMCPA_FEISHU_APP_ID", "cli_app")
	t.Setenv("OMCPA_FEISHU_APP_SECRET", "super-secret-value")
	t.Setenv("OMCPA_FEISHU_CHAT_ID", "oc_group")
	cfg, err = loadFeishuConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled != nil {
		t.Fatalf("an empty switch must be unset, got %+v", cfg.Enabled)
	}
	if !cfg.ForceDisabled {
		t.Fatal("force_disabled must be read")
	}
	if cfg.AppID == nil || *cfg.AppID != "cli_app" || cfg.ChatID == nil || *cfg.ChatID != "oc_group" {
		t.Fatalf("connection overrides = %+v", cfg)
	}
	if cfg.AppSecret == nil || *cfg.AppSecret != "super-secret-value" {
		t.Fatal("the app secret must reach the overlay")
	}

	t.Setenv("OMCPA_FEISHU_ENABLED", "not-a-bool")
	if _, err := loadFeishuConfig(); err == nil {
		t.Fatal("a malformed switch must be reported")
	}
}

func TestOllamaCollectorConfig(t *testing.T) {
	clearConfigEnv(t)
	unsetEnv(t, "OMCPA_OLLAMA_COLLECTOR_ENABLED")
	unsetEnv(t, "OMCPA_OLLAMA_COLLECTOR_INTERVAL")
	unsetEnv(t, "OMCPA_OLLAMA_TIMEOUT")
	unsetEnv(t, "OMCPA_OLLAMA_BACKOFF_BASE")
	unsetEnv(t, "OMCPA_OLLAMA_BACKOFF_MAX")

	cfg, err := loadOllamaConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CollectorEnabled || cfg.Interval != DefaultOllamaInterval || cfg.Timeout != DefaultOllamaTimeout {
		t.Fatalf("defaults = %+v", cfg)
	}

	t.Setenv("OMCPA_OLLAMA_COLLECTOR_ENABLED", "false")
	t.Setenv("OMCPA_OLLAMA_COLLECTOR_INTERVAL", "30m")
	t.Setenv("OMCPA_OLLAMA_TIMEOUT", "5s")
	cfg, err = loadOllamaConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CollectorEnabled || cfg.Interval != 30*time.Minute || cfg.Timeout != 5*time.Second {
		t.Fatalf("overrides = %+v", cfg)
	}

	t.Setenv("OMCPA_OLLAMA_BACKOFF_BASE", "2h")
	t.Setenv("OMCPA_OLLAMA_BACKOFF_MAX", "1h")
	if _, err := loadOllamaConfig(); err == nil {
		t.Fatal("a backoff max below the base must be reported")
	}
}

// TestDemoDisablesExternalModules proves a demonstration builds neither the Ollama
// scraper nor an enabled Feishu bot, and that nothing in the environment can turn
// them on there.
func TestDemoDisablesExternalModules(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(DemoModeEnv, "true")
	t.Setenv("OMCPA_OLLAMA_COLLECTOR_ENABLED", "true")
	t.Setenv("OMCPA_FEISHU_ENABLED", "true")
	t.Setenv("OMCPA_FEISHU_FORCE_DISABLED", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Ollama.CollectorEnabled {
		t.Fatal("a demo must not scrape ollama.com")
	}
	if !cfg.Feishu.ForceDisabled {
		t.Fatal("a demo must force the Feishu bot off")
	}
}
