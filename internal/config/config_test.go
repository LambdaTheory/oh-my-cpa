package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeBasePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "default", in: "", want: "/omc"},
		{name: "plain", in: "omc", want: "/omc"},
		{name: "trailing", in: "/omc/", want: "/omc"},
		{name: "nested", in: "/tools/omc/", want: "/tools/omc"},
		{name: "root", in: "/", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeBasePath(test.in)
			if err != nil {
				t.Fatalf("NormalizeBasePath(%q): %v", test.in, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeBasePath(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestLoadReadsAuthenticationConfiguration(t *testing.T) {
	t.Setenv("OMCPA_BASE_PATH", "/omc")

	t.Setenv("OMCPA_MASTER_KEY", "01234567890123456789012345678901")
	t.Setenv("OMCPA_PUBLIC_URL", "https://example.test/omc")
	for _, name := range []string{"OMCPA_DATA_DIR", "OMCPA_LISTEN_ADDR", "OMCPA_CPA_BASE_URL", "OMCPA_CPA_USAGE_ADDR", "OMCPA_REQUEST_TIMEOUT", "OMCPA_CPA_TLS_SKIP_VERIFY", "OMCPA_CPA_MANAGEMENT_KEY", "OMCPA_VERSION"} {
		_ = os.Unsetenv(name)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://example.test/omc" {
		t.Fatalf("authentication config = %#v", cfg)
	}
}

func TestNormalizeBasePathRejectsURLSyntax(t *testing.T) {
	for _, value := range []string{"/omc?x=1", "/omc#fragment", "/../omc"} {
		if _, err := NormalizeBasePath(value); err == nil {
			t.Fatalf("NormalizeBasePath(%q) unexpectedly succeeded", value)
		}
	}
}

// clearConfigEnv removes every variable Load reads, so the demo assertions below
// observe the defaults under test rather than the machine's own environment.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"OMCPA_BASE_PATH", "OMCPA_DATA_DIR", "OMCPA_LISTEN_ADDR", "OMCPA_MASTER_KEY",
		"OMCPA_PUBLIC_URL", "OMCPA_VERSION", "OMCPA_CPA_BASE_URL", "OMCPA_CPA_USAGE_ADDR",
		"OMCPA_CPA_MANAGEMENT_KEY", "OMCPA_TRUSTED_PROXY_CIDRS", DemoModeEnv, "PORT",
		"OMCPA_UPDATE_CHECK_ENABLED", "OMCPA_OMC_REPO", "OMCPA_CPA_REPO", "OMCPA_MCP_READONLY_KEY",
		"OMCPA_UPDATER_ENABLED", "OMCPA_UPDATER_URL", "OMCPA_GOOGLE_FONTS_URL",
		"OMCPA_QUOTA_SCHEDULER_ENABLED", "OMCPA_QUOTA_WATCH_INTERVAL", "OMCPA_QUOTA_ACTIVE_INTERVAL",
		"OMCPA_QUOTA_NORMAL_INTERVAL", "OMCPA_QUOTA_IDLE_INTERVAL", "OMCPA_QUOTA_WATCH_USED_PERCENT",
		"OMCPA_QUOTA_WATCH_WINDOW", "OMCPA_QUOTA_NORMAL_WINDOW", "OMCPA_QUOTA_WINDOW_END_MARGIN",
		"OMCPA_QUOTA_PASSIVE_FRESH_INTERVAL", "OMCPA_QUOTA_CREDENTIAL_FLOOR", "OMCPA_QUOTA_REFRESH_CONCURRENCY",
		"OMCPA_QUOTA_BACKOFF_BASE", "OMCPA_QUOTA_BACKOFF_MAX", "OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER",
	} {
		t.Setenv(name, "")
	}
}

func TestLoadUpdaterDefaultsOffAndRequiresExplicitOptIn(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Updater.Enabled {
		t.Fatal("updater must stay off unless the operator explicitly enables it")
	}

	t.Setenv("OMCPA_UPDATER_ENABLED", "true")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Updater.Enabled || cfg.Updater.URL != DefaultUpdaterURL {
		t.Fatalf("explicit updater opt-in = %+v", cfg.Updater)
	}
}

func TestLoadLeavesSelfHostDefaultsAlone(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IsDemoMode {
		t.Fatal("demo mode must be off unless it is asked for")
	}
	if cfg.BasePath != "/omc" {
		t.Fatalf("base path = %q, want the documented /omc", cfg.BasePath)
	}
	if cfg.DataDir != "data" {
		t.Fatalf("data dir = %q, want ./data", cfg.DataDir)
	}
	if cfg.DatabasePath != filepath.Join("data", "oh-my-cpa.db") {
		t.Fatalf("database = %q, want the self-hosted file", cfg.DatabasePath)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("listen addr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.MasterKey != "" {
		t.Fatalf("self-hosted start must not invent a master key, got %d chars", len(cfg.MasterKey))
	}
	if !cfg.Usage.Enabled {
		t.Fatal("self-hosted capture must stay enabled")
	}
	if len(cfg.TrustedProxyCIDRs) != 0 {
		t.Fatalf("trusted proxies defaulted to %v, want none", cfg.TrustedProxyCIDRs)
	}
}

func TestLoadParsesTrustedProxyCIDRs(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("OMCPA_TRUSTED_PROXY_CIDRS", "172.18.0.0/16, 2001:db8::/32")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyCIDRs[0] != "172.18.0.0/16" || cfg.TrustedProxyCIDRs[1] != "2001:db8::/32" {
		t.Fatalf("trusted proxies = %v", cfg.TrustedProxyCIDRs)
	}

	t.Setenv("OMCPA_TRUSTED_PROXY_CIDRS", "not-a-cidr")
	if _, err := Load(); err == nil {
		t.Fatal("invalid trusted proxy CIDR was accepted")
	}
}

// A platform export is a convenience for the demo only. A self-hosted process
// that happens to inherit PORT must ignore it.
func TestLoadIgnoresPlatformVariablesOutsideIsDemoMode(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PORT", "4711")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("listen addr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.PublicURL != "" {
		t.Fatalf("public URL = %q, want empty", cfg.PublicURL)
	}
}

func TestLoadIsDemoModeIsSelfContained(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(DemoModeEnv, "true")
	t.Setenv("OMCPA_CPA_BASE_URL", "http://10.0.0.9:8317")
	t.Setenv("OMCPA_CPA_MANAGEMENT_KEY", "inherited-management-key")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsDemoMode {
		t.Fatal("demo mode was not read from the environment")
	}
	if cfg.BasePath != "" {
		t.Fatalf("base path = %q, want the site root", cfg.BasePath)
	}
	if cfg.DataDir != filepath.Join(os.TempDir(), "oh-my-cpa-demo") {
		t.Fatalf("data dir = %q, want a temporary directory", cfg.DataDir)
	}
	if cfg.DatabasePath != filepath.Join(cfg.DataDir, "oh-my-cpa-demo.db") {
		t.Fatalf("demo database = %q, want the demo's own file", cfg.DatabasePath)
	}
	if len(cfg.MasterKey) != 64 {
		t.Fatalf("demo master key = %q, want 32 generated bytes as hex", cfg.MasterKey)
	}
	if cfg.CPA != (CPAConfig{}) {
		t.Fatalf("demo must not carry an inherited CPA endpoint: %#v", cfg.CPA)
	}
	if cfg.Usage.Enabled || cfg.Usage.Mode != "off" {
		t.Fatalf("demo capture must be off, got enabled=%v mode=%q", cfg.Usage.Enabled, cfg.Usage.Mode)
	}
}

func TestLoadIsDemoModeHonoursExplicitOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(DemoModeEnv, "true")
	t.Setenv("OMCPA_BASE_PATH", "/omc")
	t.Setenv("OMCPA_DATA_DIR", "/data")
	t.Setenv("OMCPA_MASTER_KEY", "01234567890123456789012345678901")
	t.Setenv("OMCPA_LISTEN_ADDR", "127.0.0.1:9000")
	t.Setenv("OMCPA_PUBLIC_URL", "https://demo.example.test")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePath != "/omc" || cfg.DataDir != filepath.Clean("/data") || cfg.ListenAddr != "127.0.0.1:9000" {
		t.Fatalf("explicit configuration was overridden: %#v", cfg)
	}
	if cfg.MasterKey != "01234567890123456789012345678901" || cfg.PublicURL != "https://demo.example.test" {
		t.Fatalf("explicit secrets were overridden: %#v", cfg)
	}
}

func TestLoadIsDemoModeAdoptsThePlatformPort(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(DemoModeEnv, "true")
	t.Setenv("PORT", "3000")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":3000" {
		t.Fatalf("listen addr = %q, want the announced :3000", cfg.ListenAddr)
	}
}

// The origin is stated rather than discovered, so a demo deployment records the one it
// was given instead of guessing at the host's.
func TestLoadIsDemoModeUsesTheConfiguredOrigin(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(DemoModeEnv, "true")
	t.Setenv("OMCPA_PUBLIC_URL", "https://demo.example.test")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://demo.example.test" {
		t.Fatalf("public URL = %q, want the configured origin", cfg.PublicURL)
	}
}

func TestLoadRejectsNonBooleanIsDemoMode(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(DemoModeEnv, "yes")
	if _, err := Load(); err == nil {
		t.Fatal("a non-boolean demo switch was accepted")
	}
}

// Demo keys must differ per process: a fixed one would let any visitor decrypt
// another instance's stored rows if the platform ever reused a volume.
func TestRandomDemoMasterKeyIsUnique(t *testing.T) {
	first, err := randomDemoMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	second, err := randomDemoMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two demo master keys were identical")
	}
}

func TestUsageIdleIntervals(t *testing.T) {
	for _, tc := range []struct {
		name, base, maximum string
		wantBase, wantMax   time.Duration
		invalid             bool
	}{
		{"defaults", "", "", time.Second, 10 * time.Second, false},
		{"custom", "2s", "5s", 2 * time.Second, 5 * time.Second, false},
		{"fixed", "1s", "1s", time.Second, time.Second, false},
		{"slow base", "30s", "", 30 * time.Second, 30 * time.Second, false},
		{"too small", "2s", "1s", 0, 0, true},
		{"zero", "1s", "0s", 0, 0, true},
		{"invalid", "1s", "abc", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMCPA_USAGE_IDLE_INTERVAL", tc.base)
			t.Setenv("OMCPA_USAGE_MAX_IDLE_INTERVAL", tc.maximum)
			cfg, err := loadUsageConfig()
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid interval accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.IdleInterval != tc.wantBase || cfg.MaxIdleInterval != tc.wantMax {
				t.Fatalf("intervals %v / %v", cfg.IdleInterval, cfg.MaxIdleInterval)
			}
		})
	}
}

// TestReleaseCheckConfigReportsAMalformedSwitch holds the release settings to the same rule as
// every other boolean in this file.
//
// Ignoring an unparsable value would be worse here than elsewhere: an operator who typed
// `OMCPA_UPDATE_CHECK_ENABLED=flase` asking to stop the process reaching the internet would get
// a deployment that keeps reaching it, with nothing to say the setting had not been understood.
func TestReleaseCheckConfigReportsAMalformedSwitch(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("OMCPA_UPDATE_CHECK_ENABLED", "flase")
	if _, err := Load(); err == nil {
		t.Fatal("a malformed OMCPA_UPDATE_CHECK_ENABLED was accepted")
	}

	// The defaults and the fork overrides still resolve.
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Release.Enabled {
		t.Fatal("checking must be ON when the switch is unset, which is the documented default")
	}
	if cfg.Release.Interval != DefaultReleaseInterval {
		t.Fatalf("interval = %v, want %v", cfg.Release.Interval, DefaultReleaseInterval)
	}
	if cfg.Release.OMCRepository != DefaultOMCRepository || cfg.Release.CPARepository != DefaultCPARepository {
		t.Fatalf("sources = %q / %q, want the documented defaults", cfg.Release.OMCRepository, cfg.Release.CPARepository)
	}

	clearConfigEnv(t)
	t.Setenv("OMCPA_UPDATE_CHECK_ENABLED", "false")
	t.Setenv("OMCPA_CPA_REPO", "someone/fork")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Release.Enabled {
		t.Fatal("an explicit false did not stop the sweep")
	}
	if cfg.Release.CPARepository != "someone/fork" {
		t.Fatalf("fork override = %q, want someone/fork", cfg.Release.CPARepository)
	}
}

func TestLoadUsesBuildVersionUnlessOperatorOverridesIt(t *testing.T) {
	clearConfigEnv(t)
	previousVersion := BuildVersion
	BuildVersion = "v0.1.0"
	t.Cleanup(func() { BuildVersion = previousVersion })
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != "v0.1.0" {
		t.Fatalf("version = %q, want embedded release", loaded.Version)
	}
	t.Setenv("OMCPA_VERSION", "v0.2.0-fork")
	loaded, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != "v0.2.0-fork" {
		t.Fatalf("version = %q, want operator override", loaded.Version)
	}
}

func TestLoadValidatesTheReadOnlyMCPKey(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("OMCPA_CPA_MANAGEMENT_KEY", "cpa-management-key-value")
	// An absent key disables the feature.
	cfg, err := Load()
	if err != nil || cfg.MCPReadOnlyKey != "" {
		t.Fatalf("absent read-only key = %q (%v), want disabled", cfg.MCPReadOnlyKey, err)
	}
	t.Setenv("OMCPA_MCP_READONLY_KEY", "too-short")
	if _, err := Load(); err == nil {
		t.Fatal("a short read-only key was accepted")
	}
	t.Setenv("OMCPA_MCP_READONLY_KEY", "cpa-management-key-value")
	if _, err := Load(); err == nil {
		t.Fatal("a read-only key equal to the management key was accepted")
	}
	t.Setenv("OMCPA_MCP_READONLY_KEY", "read-only-key-value-16")
	cfg, err = Load()
	if err != nil || cfg.MCPReadOnlyKey != "read-only-key-value-16" {
		t.Fatalf("valid read-only key = %q (%v)", cfg.MCPReadOnlyKey, err)
	}
}

func TestLoadQuotaSchedulerDefaultsAndOverrides(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Quota.SchedulerEnabled || cfg.Quota.WatchInterval != DefaultQuotaWatchInterval || cfg.Quota.IdleInterval != DefaultQuotaIdleInterval || cfg.Quota.GlobalConcurrency != DefaultQuotaGlobalConcurrency {
		t.Fatalf("defaults = %+v", cfg.Quota)
	}
	t.Setenv("OMCPA_QUOTA_SCHEDULER_ENABLED", "false")
	t.Setenv("OMCPA_QUOTA_WATCH_INTERVAL", "90s")
	t.Setenv("OMCPA_QUOTA_WATCH_USED_PERCENT", "75")
	t.Setenv("OMCPA_QUOTA_REFRESH_CONCURRENCY", "2")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quota.SchedulerEnabled || cfg.Quota.WatchInterval != 90*time.Second || cfg.Quota.WatchUsedPercent != 75 || cfg.Quota.GlobalConcurrency != 2 {
		t.Fatalf("overrides = %+v", cfg.Quota)
	}
}

func TestLoadRejectsBadQuotaSchedulerSettings(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("OMCPA_QUOTA_WATCH_INTERVAL", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("a malformed tier interval was accepted")
	}
	clearConfigEnv(t)
	t.Setenv("OMCPA_QUOTA_REFRESH_CONCURRENCY", "0")
	if _, err := Load(); err == nil {
		t.Fatal("a zero concurrency was accepted")
	}
	clearConfigEnv(t)
	t.Setenv("OMCPA_QUOTA_WATCH_USED_PERCENT", "150")
	if _, err := Load(); err == nil {
		t.Fatal("an out-of-range watch percent was accepted")
	}
	clearConfigEnv(t)
	t.Setenv("OMCPA_QUOTA_BACKOFF_BASE", "1h")
	t.Setenv("OMCPA_QUOTA_BACKOFF_MAX", "5m")
	if _, err := Load(); err == nil {
		t.Fatal("a backoff maximum below its base was accepted")
	}
}

func TestDemoModeDisablesTheQuotaScheduler(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(DemoModeEnv, "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quota.SchedulerEnabled {
		t.Fatal("demo mode left the quota scheduler on")
	}
}

// TestLoadCredentialFloorByProvider pins the per-provider floor map: the
// documented defaults stand, an explicit map overrides only the providers it
// names, and the shared fallback stays the floor for everyone else.
func TestLoadCredentialFloorByProvider(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quota.CredentialFloor != DefaultQuotaCredentialFloor {
		t.Fatalf("fallback floor = %v, want %v", cfg.Quota.CredentialFloor, DefaultQuotaCredentialFloor)
	}
	if cfg.Quota.CredentialFloorByProvider["claude"] != 10*time.Minute ||
		cfg.Quota.CredentialFloorByProvider["codex"] != 5*time.Minute ||
		cfg.Quota.CredentialFloorByProvider["antigravity"] != 5*time.Minute {
		t.Fatalf("default per-provider floors = %+v", cfg.Quota.CredentialFloorByProvider)
	}

	clearConfigEnv(t)
	t.Setenv("OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER", "codex=7m,claude=20m")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quota.CredentialFloorByProvider["codex"] != 7*time.Minute ||
		cfg.Quota.CredentialFloorByProvider["claude"] != 20*time.Minute {
		t.Fatalf("overridden per-provider floors = %+v", cfg.Quota.CredentialFloorByProvider)
	}
	// A provider the map does not name keeps its default rather than being dropped.
	if cfg.Quota.CredentialFloorByProvider["antigravity"] != 5*time.Minute {
		t.Fatalf("untouched provider floor = %v, want the default 5m", cfg.Quota.CredentialFloorByProvider["antigravity"])
	}
}

func TestLoadRejectsBadCredentialFloorByProvider(t *testing.T) {
	for _, raw := range []string{
		"codex",           // no separator
		"codex=",          // empty value
		"codex=soon",      // unparsable duration
		"codex=-5m",       // non-positive duration
		"=5m",             // empty provider
		"codex=5m,claude", // one malformed entry poisons the map
	} {
		clearConfigEnv(t)
		t.Setenv("OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER", raw)
		if _, err := Load(); err == nil {
			t.Fatalf("OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER=%q was accepted", raw)
		}
	}
}
