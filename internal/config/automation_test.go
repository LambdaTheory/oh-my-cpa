package config

import (
	"os"
	"testing"
	"time"
)

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	original, had := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(name, original)
			return
		}
		_ = os.Unsetenv(name)
	})
}

// TestAutomationMasterSwitchTriState pins the difference the console needs: an
// unset variable leaves the web switch in force, while an explicit false is an
// emergency off the web can never override.
func TestAutomationMasterSwitchTriState(t *testing.T) {
	unsetEnv(t, "OMCPA_ACCOUNT_AUTOMATION_ENABLED")
	unsetEnv(t, "OMCPA_ACCOUNT_AUTOMATION_INTERVAL")
	unsetEnv(t, "OMCPA_ACCOUNT_AUTOMATION_BACKOFF_BASE")
	unsetEnv(t, "OMCPA_ACCOUNT_AUTOMATION_BACKOFF_MAX")
	unsetEnv(t, "OMCPA_ACCOUNT_AUTOMATION_MAX_EVIDENCE_AGE")

	cfg, err := loadAutomationConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MasterFromEnv {
		t.Fatal("an unset master switch must be distinguishable from an explicit one")
	}
	if !cfg.EffectiveEnabled(true) || cfg.EffectiveEnabled(false) {
		t.Fatal("an unset master switch must follow the web switch")
	}
	if cfg.Source(true) != "web" || cfg.Source(false) != "default_off" {
		t.Fatalf("source = %q / %q", cfg.Source(true), cfg.Source(false))
	}
	if !cfg.LoopAllowed() {
		t.Fatal("an unset default must still allow the loop so a web enable takes effect")
	}
	if cfg.EvaluationInterval != DefaultAutomationInterval || cfg.BackoffBase != DefaultAutomationBackoffBase ||
		cfg.BackoffMax != DefaultAutomationBackoffMax || cfg.MaxEvidenceAge != DefaultAutomationMaxEvidence {
		t.Fatalf("defaults = %+v", cfg)
	}

	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_ENABLED", "false")
	cfg, err = loadAutomationConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MasterFromEnv || cfg.MasterEnvValue {
		t.Fatalf("explicit false = %+v", cfg)
	}
	if cfg.EffectiveEnabled(true) {
		t.Fatal("an explicit false must not be overridden by the web switch")
	}
	if cfg.Source(true) != "env_disabled" || cfg.LoopAllowed() {
		t.Fatalf("explicit false source/loop = %q / %v", cfg.Source(true), cfg.LoopAllowed())
	}

	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_ENABLED", "true")
	cfg, err = loadAutomationConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EffectiveEnabled(false) {
		t.Fatal("an explicit true must enable automation regardless of the web switch")
	}
	if cfg.Source(false) != "env_enabled" || !cfg.LoopAllowed() {
		t.Fatalf("explicit true source/loop = %q / %v", cfg.Source(false), cfg.LoopAllowed())
	}
}

func TestAutomationConfigOverridesAndRejections(t *testing.T) {
	unsetEnv(t, "OMCPA_ACCOUNT_AUTOMATION_ENABLED")
	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_INTERVAL", "30s")
	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_BACKOFF_BASE", "2m")
	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_BACKOFF_MAX", "1h")
	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_MAX_EVIDENCE_AGE", "12h")
	cfg, err := loadAutomationConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EvaluationInterval != 30*time.Second || cfg.BackoffBase != 2*time.Minute ||
		cfg.BackoffMax != time.Hour || cfg.MaxEvidenceAge != 12*time.Hour {
		t.Fatalf("overrides = %+v", cfg)
	}

	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_ENABLED", "not-a-bool")
	if _, err := loadAutomationConfig(); err == nil {
		t.Fatal("a malformed boolean must be reported")
	}
	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_ENABLED", "true")
	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_BACKOFF_BASE", "2h")
	t.Setenv("OMCPA_ACCOUNT_AUTOMATION_BACKOFF_MAX", "1h")
	if _, err := loadAutomationConfig(); err == nil {
		t.Fatal("a backoff max below the base must be reported")
	}
}
