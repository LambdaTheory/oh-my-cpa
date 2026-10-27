package feishu

import "testing"

func TestResolveConfigEnvironmentForceOffWins(t *testing.T) {
	web := Config{Enabled: true, AppID: "cli_x", ChatID: "ou_x"}
	resolved := resolveConfig(web, Overlay{ForceDisabled: true})
	if resolved.Enabled {
		t.Fatal("force-off must disable the module")
	}
	if resolved.Source != SourceEnvDisabled {
		t.Fatalf("source = %q, want %q", resolved.Source, SourceEnvDisabled)
	}
	if resolved.Degraded {
		t.Fatal("an intentional force-off is not degraded")
	}
	if !resolved.ForcedOff {
		t.Fatal("forced off should be reported")
	}
}

func TestResolveConfigEnvironmentOverridesWeb(t *testing.T) {
	enabled := true
	appID := "cli_env"
	secret := "env-secret"
	web := Config{Enabled: false, AppID: "cli_web"} // disabled and incomplete on the web side
	resolved := resolveConfig(web, Overlay{Enabled: &enabled, AppID: &appID, AppSecret: &secret, ChatID: strPtr("ou_env")})
	if !resolved.Enabled || resolved.AppID != "cli_env" {
		t.Fatalf("environment must override the web value, got %+v", resolved)
	}
	if resolved.Source != SourceEnvEnabled {
		t.Fatalf("source = %q, want %q", resolved.Source, SourceEnvEnabled)
	}
	if !resolved.Ready() {
		t.Fatalf("expected a ready configuration, got %+v", resolved)
	}
}

func TestResolveConfigInvalidIsDegradedNotFatal(t *testing.T) {
	web := Config{Enabled: true, AppID: "", ChatID: ""}
	resolved := resolveConfig(web, Overlay{})
	if resolved.Enabled {
		t.Fatal("an invalid enabled configuration must not run")
	}
	if !resolved.Degraded || resolved.Reason == "" {
		t.Fatalf("expected a degraded configuration with a reason, got %+v", resolved)
	}
	if resolved.Ready() {
		t.Fatal("a degraded configuration is not ready")
	}
}

func TestResolveConfigDefaultsSummaryAndQuiet(t *testing.T) {
	resolved := resolveConfig(Config{Enabled: true, AppID: "cli_x", ChatID: "ou_x"}, Overlay{AppSecret: strPtr("secret")})
	if len(resolved.SummaryTimes) != 2 {
		t.Fatalf("expected two default summary times, got %v", resolved.SummaryTimes)
	}
	if resolved.QuietStart.String() != DefaultQuietStart || resolved.QuietEnd.String() != DefaultQuietEnd {
		t.Fatalf("unexpected quiet defaults: %s-%s", resolved.QuietStart, resolved.QuietEnd)
	}
	if !resolved.Ready() {
		t.Fatalf("expected a ready configuration, got %+v", resolved)
	}
}

func TestWithinQuietWrapsMidnight(t *testing.T) {
	resolved := resolveConfig(Config{Enabled: true, AppID: "cli_x", ChatID: "ou_x", Timezone: "UTC"}, Overlay{AppSecret: strPtr("s")})
	if !withinQuiet(resolved, mustTime("2026-10-15T23:30:00Z")) {
		t.Fatal("23:30 should be quiet")
	}
	if !withinQuiet(resolved, mustTime("2026-10-16T02:00:00Z")) {
		t.Fatal("02:00 should be quiet")
	}
	if withinQuiet(resolved, mustTime("2026-10-16T09:00:00Z")) {
		t.Fatal("09:00 should not be quiet")
	}
}

func strPtr(value string) *string { return &value }
