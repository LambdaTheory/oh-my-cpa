package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BuildVersion is injected by release builds; untagged source builds remain incomparable.
var BuildVersion = "v0.1.0-dev"

// Config contains process configuration. Secrets are read once at startup and
// are never included in API responses.
type Config struct {
	ListenAddr     string
	BasePath       string
	DataDir        string
	DatabasePath   string
	CPA            CPAConfig
	MasterKey      string
	Usage          UsageConfig
	Quota          QuotaConfig
	Automation     AutomationConfig
	Feishu         FeishuConfig
	Ollama         OllamaConfig
	PublicURL      string
	Version        string
	BuildIdentity  BuildIdentity
	RequestTimeout time.Duration
	TLSSkipVerify  bool
	Release        ReleaseConfig
	// MCPReadOnlyKey, when set, is a second bearer credential the MCP endpoint
	// accepts: it reaches read capabilities only and sees account labels masked.
	// Empty disables it. It must be at least 16 characters and differ from the
	// management key.
	MCPReadOnlyKey string
	// TrustedProxyCIDRs are the direct peer networks whose forwarding headers
	// may be used for per-client login throttling. Empty means trust none.
	TrustedProxyCIDRs []string
	// IsDemoMode turns this process into the public demonstration build: the
	// console is served from fixtures, so it needs no CPA, no management key and
	// no provider credential, and the server refuses every operator surface that
	// would touch real secret material. It is off unless OMCPA_DEMO_MODE is set,
	// which is what keeps the self-hosted default byte-for-byte unchanged.
	IsDemoMode bool
}

// ReleaseConfig controls the release-observation surface: which published versions
// the console reports, and whether it looks for them on its own.
type ReleaseConfig struct {
	// Enabled gates the background sweep only. The page's own check and the manual
	// button keep working when it is false, because those are an operator asking a
	// question rather than the process deciding to talk to the internet.
	Enabled bool
	// AutoCheck gates the check the page performs when it is opened, independently of
	// Enabled. It exists because two questions are not one: "may this process reach the
	// internet on its own" and "may opening a page spend a request from a shared budget".
	// A self-hosted deployment wants the first disabled and the second enabled - the page
	// exists to answer "is there a newer version". A test suite wants both disabled, since
	// its page visits are not a reader asking anything. The manual button keeps working in
	// both cases, because that is an operator's explicit request.
	AutoCheck bool
	// OMCRepository and CPARepository name the published release sources as
	// "owner/name". Only the identifier is configurable: the host is fixed, so this
	// cannot become a way to make the server fetch an arbitrary address.
	OMCRepository string
	CPARepository string
	// Interval is the background sweep period.
	Interval time.Duration
}

// Default release sources. The gateway default is the upstream the project is built
// against; the console default is this project's own repository. A deployment that
// runs a fork overrides them rather than editing code.
const (
	DefaultOMCRepository = "WizisCool/oh-my-cpa"
	DefaultCPARepository = "router-for-me/CLIProxyAPI"
	// DefaultReleaseInterval is six hours: often enough to notice a release the same
	// day, rare enough that the unauthenticated GitHub budget of sixty requests per
	// hour is never at risk from the sweep itself.
	DefaultReleaseInterval = 6 * time.Hour
)

// DemoModeEnv is the switch that turns a self-hosted deployment into the public
// demonstration. The default is deliberately off.
const DemoModeEnv = "OMCPA_DEMO_MODE"

// UsageConfig controls the request-record pipeline that backs the dashboard and
// every later analytics feature.
type UsageConfig struct {
	// Enabled gates background capture. When false the dashboard still serves
	// whatever was stored before, but nothing new is collected.
	Enabled bool
	// Mode selects the collection path: auto, subscribe, resp_pull, http_pull or off.
	Mode string
	// IdleInterval is the active polling delay; empty queues back off to MaxIdleInterval.
	IdleInterval    time.Duration
	MaxIdleInterval time.Duration
	// BatchSize caps one pop.
	BatchSize int
	// AggregateInterval bounds how stale rollup-assisted queries can get.
	AggregateInterval time.Duration
	// RetentionDays rolls request records; zero keeps everything. The usage
	// facts the dashboard reads are permanent and do not follow it.
	RetentionDays int
	// InboxRetentionDays rolls captured payloads once they are decoded; zero
	// keeps them as long as request records.
	InboxRetentionDays int
	// CollectErrors also subscribes to CPA's push-only errors channel.
	CollectErrors bool
}

// QuotaConfig controls the background per-account quota refresh scheduler. It
// decides how often each credential's quota is actively read from its provider
// based on how recently it served traffic; the console and MCP keep working
// without it and refresh on demand.
type QuotaConfig struct {
	// SchedulerEnabled gates the background loop only.
	SchedulerEnabled bool
	// Tier intervals. A credential that served a request in WatchWindow and has
	// a window at or above WatchUsedPercent is watched most often; a credential
	// with recent traffic is active; one seen within NormalWindow is normal; and
	// a quiet credential is idle.
	WatchInterval  time.Duration
	ActiveInterval time.Duration
	NormalInterval time.Duration
	IdleInterval   time.Duration
	// WatchUsedPercent is the window share that promotes an active credential to
	// watched.
	WatchUsedPercent float64
	// WatchWindow is how recently a request counts as recent traffic; the same
	// window decides watch and active.
	WatchWindow time.Duration
	// NormalWindow is how recently a request keeps a credential out of idle.
	NormalWindow time.Duration
	// WindowEndMargin is how close to a window's reset a refresh is forced so the
	// next cycle has a previous-cycle reading to estimate from.
	WindowEndMargin time.Duration
	// PassiveFreshInterval is the floor an interval is widened to when a fresh
	// passive observation already covers the standard window.
	PassiveFreshInterval time.Duration
	// CredentialFloor is the shortest time between two active queries of the same
	// credential, whatever the tier says. It is the fallback for a provider that
	// CredentialFloorByProvider does not name.
	CredentialFloor time.Duration
	// CredentialFloorByProvider overrides CredentialFloor for the providers it
	// names, keyed by the normalized provider key the credential resolution
	// produces. A provider absent from the map uses CredentialFloor.
	CredentialFloorByProvider map[string]time.Duration
	// GlobalConcurrency bounds simultaneous active refreshes; a single provider is
	// always serialized at one.
	GlobalConcurrency int
	// BackoffBase and BackoffMax shape the exponential fallback used when an
	// upstream answers 429 without a Retry-After.
	BackoffBase time.Duration
	BackoffMax  time.Duration
}

// Quota scheduler defaults. The tiers are deliberately slower than the passive
// stream: a credential's own request headers already keep an active window
// fresh, so the active probes exist for quiet credentials and for the cycles the
// passive stream cannot close.
const (
	DefaultQuotaWatchInterval       = 5 * time.Minute
	DefaultQuotaActiveInterval      = 15 * time.Minute
	DefaultQuotaNormalInterval      = 30 * time.Minute
	DefaultQuotaIdleInterval        = 2 * time.Hour
	DefaultQuotaWatchUsedPercent    = 80.0
	DefaultQuotaWatchWindow         = 15 * time.Minute
	DefaultQuotaNormalWindow        = 2 * time.Hour
	DefaultQuotaWindowEndMargin     = 15 * time.Minute
	DefaultQuotaPassiveFresh        = 60 * time.Minute
	DefaultQuotaCredentialFloor     = 10 * time.Minute
	DefaultQuotaGlobalConcurrency   = 4
	DefaultQuotaBackoffBase         = 5 * time.Minute
	DefaultQuotaBackoffMax          = 6 * time.Hour
	DefaultQuotaResetLookbackWindow = time.Hour
)

// DefaultQuotaCredentialFloorByProvider is the documented per-provider floor: the
// providers whose own limits meter tightly are read sooner than the shared
// fallback, so the watched tier's five minutes is actually reachable for them.
// Claude keeps the shared ten minutes; a provider not named here uses
// DefaultQuotaCredentialFloor. OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER overrides
// the entries it names.
var DefaultQuotaCredentialFloorByProvider = map[string]time.Duration{
	"claude":      10 * time.Minute,
	"codex":       5 * time.Minute,
	"antigravity": 5 * time.Minute,
}

// AutomationConfig controls account-level auto-management: disabling a credential
// only on explicit new account-level weekly exhaustion evidence and enabling it at
// the recorded reset time.
//
// The master switch is an environment variable rather than a stored setting, and
// the struct keeps "unset" apart from "explicitly off": under the default (unset)
// an operator can still turn automation on from the console, while
// OMCPA_ACCOUNT_AUTOMATION_ENABLED=false is an out-of-band emergency control the
// web switch must never override. EffectiveEnabled resolves the two.
type AutomationConfig struct {
	// MasterFromEnv is true when the environment variable was set explicitly.
	MasterFromEnv bool
	// MasterEnvValue is the parsed environment value; meaningful only when
	// MasterFromEnv is true.
	MasterEnvValue bool
	// EvaluationInterval is how often the pass runs.
	EvaluationInterval time.Duration
	// BackoffBase and BackoffMax shape the persisted retry schedule after a failed
	// enable.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// MaxEvidenceAge bounds how old the reading that justifies a disable may be.
	MaxEvidenceAge time.Duration
}

// Account automation defaults. The pass is a light read most of the time, and the
// evidence age is a day so a brief scheduler outage does not make every reading
// unusable while still bounding a long one.
const (
	DefaultAutomationInterval    = time.Minute
	DefaultAutomationBackoffBase = 5 * time.Minute
	DefaultAutomationBackoffMax  = 6 * time.Hour
	DefaultAutomationMaxEvidence = 24 * time.Hour
)

// EffectiveEnabled resolves the environment master switch against the stored web
// switch. An explicit environment value always wins; an unset one leaves the web
// choice in force.
func (c AutomationConfig) EffectiveEnabled(webEnabled bool) bool {
	if c.MasterFromEnv {
		return c.MasterEnvValue
	}
	return webEnabled
}

// Source names where the effective switch came from, so the console can show it.
func (c AutomationConfig) Source(webEnabled bool) string {
	if c.MasterFromEnv {
		if c.MasterEnvValue {
			return "env_enabled"
		}
		return "env_disabled"
	}
	if webEnabled {
		return "web"
	}
	return "default_off"
}

// LoopAllowed reports whether the background loop may run at all. An explicit
// environment false forbids it; an unset default still starts it so a later web
// enable takes effect without a restart.
func (c AutomationConfig) LoopAllowed() bool {
	return !(c.MasterFromEnv && !c.MasterEnvValue)
}

type CPAConfig struct {
	BaseURL       string
	UsageAddr     string
	ManagementKey string
}

// FeishuConfig is the environment override for the Feishu bot. The app secret is
// read here and only here; it travels to the bot's resolved configuration and is
// never written to a response or a log by this layer.
//
// Enabled is tri-state: nil leaves the console's web switch in force, true forces
// the bot on and false is an environment off the web cannot override.
// ForceDisabled is the stronger emergency switch. An empty environment string is
// treated as unset, so a compose file that passes a variable through unset cannot
// pin the feature off.
type FeishuConfig struct {
	Enabled       *bool
	ForceDisabled bool
	AppID         *string
	AppSecret     *string
	ChatID        *string
}

// OllamaConfig controls the Ollama quota collector, the one loop that scrapes
// ollama.com with each account's stored cookie.
type OllamaConfig struct {
	// CollectorEnabled gates the background scraper. It is off in demo mode, which
	// must not make an outbound request.
	CollectorEnabled bool
	Interval         time.Duration
	Timeout          time.Duration
	BackoffBase      time.Duration
	BackoffMax       time.Duration
}

// Ollama collector defaults, shared with internal/ollama.
const (
	DefaultOllamaInterval    = 15 * time.Minute
	DefaultOllamaTimeout     = 20 * time.Second
	DefaultOllamaBackoffBase = 5 * time.Minute
	DefaultOllamaBackoffMax  = 6 * time.Hour
)

// NormalizeBasePath returns a clean URL path without a trailing slash. An
// empty environment value means the documented default (/omc); callers that
// explicitly need the site root should pass "/".
func NormalizeBasePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/omc", nil
	}
	if raw == "/" {
		return "", nil
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	if strings.ContainsAny(raw, "?#") {
		return "", fmt.Errorf("base path must not contain query or fragment")
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == ".." {
			return "", fmt.Errorf("base path must not contain parent traversal")
		}
	}
	clean := path.Clean(raw)
	if clean == "." || clean == "/" {
		return "", nil
	}
	return clean, nil
}

func Load() (Config, error) {
	demoMode, err := parseBoolEnv(DemoModeEnv, false)
	if err != nil {
		return Config{}, err
	}

	basePathEnv := strings.TrimSpace(os.Getenv("OMCPA_BASE_PATH"))
	if demoMode && basePathEnv == "" {
		// A pasted demo URL has to be the console itself. Only the demo default
		// moves; an explicit OMCPA_BASE_PATH still wins, so a self-hosted
		// deployment that turned the demo on keeps its own sub-path.
		basePathEnv = "/"
	}
	basePath, err := NormalizeBasePath(basePathEnv)
	if err != nil {
		return Config{}, fmt.Errorf("OMCPA_BASE_PATH: %w", err)
	}

	dataDir := strings.TrimSpace(os.Getenv("OMCPA_DATA_DIR"))
	if dataDir == "" {
		dataDir = "./data"
		if demoMode {
			// Demo data is disposable by definition, and the platform that runs the
			// public demo mounts no writable volume: /tmp is the only directory a
			// container image may write to there.
			dataDir = filepath.Join(os.TempDir(), "oh-my-cpa-demo")
		}
	}
	dataDir = filepath.Clean(dataDir)

	listenAddr := strings.TrimSpace(os.Getenv("OMCPA_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = ":8080"
		if demoMode {
			listenAddr = demoListenAddr()
		}
	}

	baseURL := strings.TrimSpace(os.Getenv("OMCPA_CPA_BASE_URL"))
	usageAddr := strings.TrimSpace(os.Getenv("OMCPA_CPA_USAGE_ADDR"))
	if usageAddr == "" && baseURL != "" {
		usageAddr = deriveUsageAddr(baseURL)
	}

	timeout := 15 * time.Second
	if raw := strings.TrimSpace(os.Getenv("OMCPA_REQUEST_TIMEOUT")); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("OMCPA_REQUEST_TIMEOUT must be a positive duration: %q", raw)
		}
		timeout = parsed
	}

	tlsSkipVerify, err := parseBoolEnv("OMCPA_CPA_TLS_SKIP_VERIFY", false)
	if err != nil {
		return Config{}, err
	}
	trustedProxyCIDRs, err := parseTrustedProxyCIDRs()
	if err != nil {
		return Config{}, err
	}

	masterKey := strings.TrimSpace(os.Getenv("OMCPA_MASTER_KEY"))
	publicURL := strings.TrimSpace(os.Getenv("OMCPA_PUBLIC_URL"))
	if demoMode {
		if masterKey == "" {
			// A demo holds no operator secret to protect, and demanding one would make
			// the deployment refuse to boot over a value nobody needs. Rotating it per
			// process is what makes a restarted demo start from a clean fixture again.
			masterKey, err = randomDemoMasterKey()
			if err != nil {
				return Config{}, err
			}
		}
		// The origin is stated by OMCPA_PUBLIC_URL rather than discovered, because the
		// demonstration no longer runs on a platform that announces one. A demo without
		// it keeps a session cookie usable over plain HTTP, which is what a local run
		// wants; a deployment sets it.
	}

	readOnlyKey := strings.TrimSpace(os.Getenv("OMCPA_MCP_READONLY_KEY"))
	if readOnlyKey != "" {
		// A read-only key that a typo made identical to the management key, or
		// that is short enough to guess, would silently become a full-authority
		// credential. Refusing to start is the only safe reading of "optional
		// but set": the deployment asked for it, so it must mean what it says.
		if len(readOnlyKey) < 16 {
			return Config{}, fmt.Errorf("OMCPA_MCP_READONLY_KEY must be at least 16 characters")
		}
		if readOnlyKey == strings.TrimSpace(os.Getenv("OMCPA_CPA_MANAGEMENT_KEY")) {
			return Config{}, fmt.Errorf("OMCPA_MCP_READONLY_KEY must differ from the CPA management key")
		}
	}

	usage, err := loadUsageConfig()
	if err != nil {
		return Config{}, err
	}
	quotaConfig, err := loadQuotaConfig()
	if err != nil {
		return Config{}, err
	}
	automationConfig, err := loadAutomationConfig()
	if err != nil {
		return Config{}, err
	}
	feishuConfig, err := loadFeishuConfig()
	if err != nil {
		return Config{}, err
	}
	ollamaConfig, err := loadOllamaConfig()
	if err != nil {
		return Config{}, err
	}
	releaseConfig, err := loadReleaseConfig()
	if err != nil {
		return Config{}, err
	}
	if demoMode {
		// The fixture database is the whole history, and there is no CPA queue to
		// drain: leaving capture on would only poll an upstream that does not
		// exist and overwrite the fixture with nothing.
		usage.Enabled = false
		usage.Mode = "off"
		// A demonstration must not reach any upstream. The scheduler is a network
		// loop, so it is switched off here rather than left to the fixture to absorb.
		quotaConfig.SchedulerEnabled = false
		// The Ollama collector scrapes ollama.com and the Feishu bot opens a long
		// connection and sends messages: neither may happen in a demonstration.
		ollamaConfig.CollectorEnabled = false
		feishuConfig.ForceDisabled = true
	}

	cpa := CPAConfig{
		BaseURL:       baseURL,
		UsageAddr:     usageAddr,
		ManagementKey: strings.TrimSpace(os.Getenv("OMCPA_CPA_MANAGEMENT_KEY")),
	}
	if demoMode {
		// The real endpoint is replaced by the in-process fixture once it is
		// listening; see internal/demo. Clearing it here keeps the fixture the only
		// credential a demo can carry, even if the deployment inherits a stale one.
		cpa = CPAConfig{}
	}

	databasePath := filepath.Join(dataDir, "oh-my-cpa.db")
	if demoMode {
		// A demo database is per-boot: it is deleted and rebuilt so the history always
		// ends at the moment the visitor arrives. The file has its own name so that a
		// deployment which pointed the demo at a directory holding real data cannot
		// have that data deleted with it.
		databasePath = filepath.Join(dataDir, "oh-my-cpa-demo.db")
	}

	return Config{
		ListenAddr:        listenAddr,
		Usage:             usage,
		Quota:             quotaConfig,
		Automation:        automationConfig,
		Feishu:            feishuConfig,
		Ollama:            ollamaConfig,
		BasePath:          basePath,
		DataDir:           dataDir,
		DatabasePath:      databasePath,
		MasterKey:         masterKey,
		MCPReadOnlyKey:    readOnlyKey,
		PublicURL:         publicURL,
		Version:           envOr("OMCPA_VERSION", BuildVersion),
		BuildIdentity:     readBuildIdentity(),
		RequestTimeout:    timeout,
		TLSSkipVerify:     tlsSkipVerify,
		TrustedProxyCIDRs: trustedProxyCIDRs,
		Release:           releaseConfig,
		CPA:               cpa,
		IsDemoMode:        demoMode,
	}, nil
}

// loadReleaseConfig reads the release-observation settings.
//
// A malformed boolean is reported rather than ignored, matching every other switch in this
// file. Ignoring it would leave a deployment that asked to stop reaching the internet doing
// exactly that, with nothing to indicate the setting had not been understood.
func loadReleaseConfig() (ReleaseConfig, error) {
	enabled, err := parseBoolEnv("OMCPA_UPDATE_CHECK_ENABLED", true)
	if err != nil {
		return ReleaseConfig{}, err
	}
	autoCheck, err := parseBoolEnv("OMCPA_UPDATE_CHECK_ON_PAGE_LOAD", true)
	if err != nil {
		return ReleaseConfig{}, err
	}
	return ReleaseConfig{
		Enabled:       enabled,
		AutoCheck:     autoCheck,
		OMCRepository: envOr("OMCPA_OMC_REPO", DefaultOMCRepository),
		CPARepository: envOr("OMCPA_CPA_REPO", DefaultCPARepository),
		Interval:      DefaultReleaseInterval,
	}, nil
}

func parseTrustedProxyCIDRs() ([]string, error) {
	raw := strings.TrimSpace(os.Getenv("OMCPA_TRUSTED_PROXY_CIDRS"))
	if raw == "" {
		return nil, nil
	}
	networks := make([]string, 0)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("OMCPA_TRUSTED_PROXY_CIDRS contains %q: %w", entry, err)
		}
		networks = append(networks, network.String())
	}
	return networks, nil
}

// demoListenAddr resolves the port the public demo serves on. A container
// platform routes to a port it chooses and announces it through PORT, while a
// local run keeps the documented default.
func demoListenAddr() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return ":" + port
	}
	return ":8080"
}

// randomDemoMasterKey mints the per-process key that encrypts the demo's
// disposable local state. It is never displayed, exported or persisted.
func randomDemoMasterKey() (string, error) {
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return "", fmt.Errorf("generate demo master key: %w", err)
	}
	return hex.EncodeToString(material), nil
}

func deriveUsageAddr(rawBaseURL string) string {
	parsed, err := url.Parse(rawBaseURL)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	port := parsed.Port()
	if port == "" {
		port = "8317"
	}
	return parsed.Hostname() + ":" + port
}

func parseBoolEnv(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be boolean: %q", name, raw)
	}
	return value, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// loadUsageConfig reads the request-record pipeline settings.
func loadUsageConfig() (UsageConfig, error) {
	enabled, err := parseBoolEnv("OMCPA_USAGE_INGEST_ENABLED", true)
	if err != nil {
		return UsageConfig{}, err
	}
	collectErrors, err := parseBoolEnv("OMCPA_USAGE_COLLECT_ERRORS", true)
	if err != nil {
		return UsageConfig{}, err
	}
	idleInterval := time.Second
	if raw := strings.TrimSpace(os.Getenv("OMCPA_USAGE_IDLE_INTERVAL")); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 {
			return UsageConfig{}, fmt.Errorf("OMCPA_USAGE_IDLE_INTERVAL must be a positive duration: %q", raw)
		}
		idleInterval = parsed
	}
	maxIdleInterval := max(idleInterval, 10*time.Second)
	if raw := strings.TrimSpace(os.Getenv("OMCPA_USAGE_MAX_IDLE_INTERVAL")); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed < idleInterval {
			return UsageConfig{}, fmt.Errorf("OMCPA_USAGE_MAX_IDLE_INTERVAL must be at least OMCPA_USAGE_IDLE_INTERVAL: %q", raw)
		}
		maxIdleInterval = parsed
	}
	aggregateInterval := 15 * time.Second
	if raw := strings.TrimSpace(os.Getenv("OMCPA_USAGE_AGGREGATE_INTERVAL")); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 {
			return UsageConfig{}, fmt.Errorf("OMCPA_USAGE_AGGREGATE_INTERVAL must be a positive duration: %q", raw)
		}
		aggregateInterval = parsed
	}
	batchSize := 1000
	if raw := strings.TrimSpace(os.Getenv("OMCPA_USAGE_BATCH_SIZE")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed <= 0 || parsed > 10000 {
			return UsageConfig{}, fmt.Errorf("OMCPA_USAGE_BATCH_SIZE must be between 1 and 10000: %q", raw)
		}
		batchSize = parsed
	}
	// Request records are what the request list, the quota estimates and the price
	// previews read. The dashboard does not depend on them: its history lives in
	// the permanent usage facts. Ninety days matches the longest window the request
	// list offers, so every preset it shows is fully backed by stored records.
	retentionDays := 90
	if raw := strings.TrimSpace(os.Getenv("OMCPA_USAGE_RETENTION_DAYS")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 {
			return UsageConfig{}, fmt.Errorf("OMCPA_USAGE_RETENTION_DAYS must be zero or positive: %q", raw)
		}
		retentionDays = parsed
	}
	// A decoded payload is only read again to replay a record after a decoder fix,
	// so a week is a replay window, not an archive. Payloads are several times the
	// size of the records they produce, which is why they do not share the long
	// horizon.
	inboxRetentionDays := 7
	if raw := strings.TrimSpace(os.Getenv("OMCPA_USAGE_INBOX_RETENTION_DAYS")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 {
			return UsageConfig{}, fmt.Errorf("OMCPA_USAGE_INBOX_RETENTION_DAYS must be zero or positive: %q", raw)
		}
		inboxRetentionDays = parsed
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("OMCPA_USAGE_INGEST_MODE")))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "subscribe", "resp_pull", "http_pull", "off":
	default:
		return UsageConfig{}, fmt.Errorf("OMCPA_USAGE_INGEST_MODE must be auto, subscribe, resp_pull, http_pull or off")
	}
	return UsageConfig{
		Enabled:            enabled,
		Mode:               mode,
		IdleInterval:       idleInterval,
		MaxIdleInterval:    maxIdleInterval,
		BatchSize:          batchSize,
		AggregateInterval:  aggregateInterval,
		RetentionDays:      retentionDays,
		InboxRetentionDays: inboxRetentionDays,
		CollectErrors:      collectErrors,
	}, nil
}

// loadQuotaConfig reads the background quota scheduler settings. Every interval
// and threshold the tier rules use is overridable, because the right cadence
// depends on how many credentials a deployment holds and how tightly its
// providers meter.
func loadQuotaConfig() (QuotaConfig, error) {
	enabled, err := parseBoolEnv("OMCPA_QUOTA_SCHEDULER_ENABLED", true)
	if err != nil {
		return QuotaConfig{}, err
	}
	watchInterval, err := parseDurationEnv("OMCPA_QUOTA_WATCH_INTERVAL", DefaultQuotaWatchInterval)
	if err != nil {
		return QuotaConfig{}, err
	}
	activeInterval, err := parseDurationEnv("OMCPA_QUOTA_ACTIVE_INTERVAL", DefaultQuotaActiveInterval)
	if err != nil {
		return QuotaConfig{}, err
	}
	normalInterval, err := parseDurationEnv("OMCPA_QUOTA_NORMAL_INTERVAL", DefaultQuotaNormalInterval)
	if err != nil {
		return QuotaConfig{}, err
	}
	idleInterval, err := parseDurationEnv("OMCPA_QUOTA_IDLE_INTERVAL", DefaultQuotaIdleInterval)
	if err != nil {
		return QuotaConfig{}, err
	}
	watchWindow, err := parseDurationEnv("OMCPA_QUOTA_WATCH_WINDOW", DefaultQuotaWatchWindow)
	if err != nil {
		return QuotaConfig{}, err
	}
	normalWindow, err := parseDurationEnv("OMCPA_QUOTA_NORMAL_WINDOW", DefaultQuotaNormalWindow)
	if err != nil {
		return QuotaConfig{}, err
	}
	windowEndMargin, err := parseDurationEnv("OMCPA_QUOTA_WINDOW_END_MARGIN", DefaultQuotaWindowEndMargin)
	if err != nil {
		return QuotaConfig{}, err
	}
	passiveFresh, err := parseDurationEnv("OMCPA_QUOTA_PASSIVE_FRESH_INTERVAL", DefaultQuotaPassiveFresh)
	if err != nil {
		return QuotaConfig{}, err
	}
	credentialFloor, err := parseDurationEnv("OMCPA_QUOTA_CREDENTIAL_FLOOR", DefaultQuotaCredentialFloor)
	if err != nil {
		return QuotaConfig{}, err
	}
	credentialFloors, err := parseCredentialFloorByProvider()
	if err != nil {
		return QuotaConfig{}, err
	}
	backoffBase, err := parseDurationEnv("OMCPA_QUOTA_BACKOFF_BASE", DefaultQuotaBackoffBase)
	if err != nil {
		return QuotaConfig{}, err
	}
	backoffMax, err := parseDurationEnv("OMCPA_QUOTA_BACKOFF_MAX", DefaultQuotaBackoffMax)
	if err != nil {
		return QuotaConfig{}, err
	}
	if backoffMax < backoffBase {
		return QuotaConfig{}, fmt.Errorf("OMCPA_QUOTA_BACKOFF_MAX must be at least OMCPA_QUOTA_BACKOFF_BASE: %q < %q", backoffMax, backoffBase)
	}
	watchUsedPercent := DefaultQuotaWatchUsedPercent
	if raw := strings.TrimSpace(os.Getenv("OMCPA_QUOTA_WATCH_USED_PERCENT")); raw != "" {
		parsed, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr != nil || parsed < 0 || parsed > 100 {
			return QuotaConfig{}, fmt.Errorf("OMCPA_QUOTA_WATCH_USED_PERCENT must be between 0 and 100: %q", raw)
		}
		watchUsedPercent = parsed
	}
	concurrency := DefaultQuotaGlobalConcurrency
	if raw := strings.TrimSpace(os.Getenv("OMCPA_QUOTA_REFRESH_CONCURRENCY")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > 32 {
			return QuotaConfig{}, fmt.Errorf("OMCPA_QUOTA_REFRESH_CONCURRENCY must be between 1 and 32: %q", raw)
		}
		concurrency = parsed
	}
	return QuotaConfig{
		SchedulerEnabled:          enabled,
		WatchInterval:             watchInterval,
		ActiveInterval:            activeInterval,
		NormalInterval:            normalInterval,
		IdleInterval:              idleInterval,
		WatchUsedPercent:          watchUsedPercent,
		WatchWindow:               watchWindow,
		NormalWindow:              normalWindow,
		WindowEndMargin:           windowEndMargin,
		PassiveFreshInterval:      passiveFresh,
		CredentialFloor:           credentialFloor,
		CredentialFloorByProvider: credentialFloors,
		GlobalConcurrency:         concurrency,
		BackoffBase:               backoffBase,
		BackoffMax:                backoffMax,
	}, nil
}

// loadAutomationConfig reads the account auto-management settings. The master
// switch keeps unset and explicit-false apart: the former leaves the web choice
// in force, the latter is an emergency off the web cannot override.
func loadAutomationConfig() (AutomationConfig, error) {
	cfg := AutomationConfig{}
	if raw, present := os.LookupEnv("OMCPA_ACCOUNT_AUTOMATION_ENABLED"); present && strings.TrimSpace(raw) != "" {
		value, err := parseBoolString("OMCPA_ACCOUNT_AUTOMATION_ENABLED", raw)
		if err != nil {
			return AutomationConfig{}, err
		}
		cfg.MasterFromEnv = true
		cfg.MasterEnvValue = value
	}
	interval, err := parseDurationEnv("OMCPA_ACCOUNT_AUTOMATION_INTERVAL", DefaultAutomationInterval)
	if err != nil {
		return AutomationConfig{}, err
	}
	backoffBase, err := parseDurationEnv("OMCPA_ACCOUNT_AUTOMATION_BACKOFF_BASE", DefaultAutomationBackoffBase)
	if err != nil {
		return AutomationConfig{}, err
	}
	backoffMax, err := parseDurationEnv("OMCPA_ACCOUNT_AUTOMATION_BACKOFF_MAX", DefaultAutomationBackoffMax)
	if err != nil {
		return AutomationConfig{}, err
	}
	if backoffMax < backoffBase {
		return AutomationConfig{}, fmt.Errorf("OMCPA_ACCOUNT_AUTOMATION_BACKOFF_MAX must be at least OMCPA_ACCOUNT_AUTOMATION_BACKOFF_BASE: %q < %q", backoffMax, backoffBase)
	}
	maxEvidence, err := parseDurationEnv("OMCPA_ACCOUNT_AUTOMATION_MAX_EVIDENCE_AGE", DefaultAutomationMaxEvidence)
	if err != nil {
		return AutomationConfig{}, err
	}
	cfg.EvaluationInterval = interval
	cfg.BackoffBase = backoffBase
	cfg.BackoffMax = backoffMax
	cfg.MaxEvidenceAge = maxEvidence
	return cfg, nil
}

// loadFeishuConfig reads the Feishu environment override. The app secret is read
// only here, never echoed by an error or a log.
//
// A switch is tri-state: OMCPA_FEISHU_ENABLED unset leaves the web setting in
// force; OMCPA_FEISHU_FORCE_DISABLED is the emergency off. An empty string is
// treated as unset so a compose file that passes a variable through unset does
// not become a hard value.
func loadFeishuConfig() (FeishuConfig, error) {
	cfg := FeishuConfig{}
	if raw, present := os.LookupEnv("OMCPA_FEISHU_ENABLED"); present && strings.TrimSpace(raw) != "" {
		value, err := parseBoolString("OMCPA_FEISHU_ENABLED", raw)
		if err != nil {
			return FeishuConfig{}, err
		}
		cfg.Enabled = &value
	}
	forceDisabled, err := parseBoolEnv("OMCPA_FEISHU_FORCE_DISABLED", false)
	if err != nil {
		return FeishuConfig{}, err
	}
	cfg.ForceDisabled = forceDisabled
	cfg.AppID = optionalEnvString("OMCPA_FEISHU_APP_ID")
	cfg.AppSecret = optionalEnvString("OMCPA_FEISHU_APP_SECRET")
	cfg.ChatID = optionalEnvString("OMCPA_FEISHU_CHAT_ID")
	return cfg, nil
}

// loadOllamaConfig reads the Ollama collector settings.
func loadOllamaConfig() (OllamaConfig, error) {
	enabled, err := parseBoolEnv("OMCPA_OLLAMA_COLLECTOR_ENABLED", true)
	if err != nil {
		return OllamaConfig{}, err
	}
	interval, err := parseDurationEnv("OMCPA_OLLAMA_COLLECTOR_INTERVAL", DefaultOllamaInterval)
	if err != nil {
		return OllamaConfig{}, err
	}
	timeout, err := parseDurationEnv("OMCPA_OLLAMA_TIMEOUT", DefaultOllamaTimeout)
	if err != nil {
		return OllamaConfig{}, err
	}
	backoffBase, err := parseDurationEnv("OMCPA_OLLAMA_BACKOFF_BASE", DefaultOllamaBackoffBase)
	if err != nil {
		return OllamaConfig{}, err
	}
	backoffMax, err := parseDurationEnv("OMCPA_OLLAMA_BACKOFF_MAX", DefaultOllamaBackoffMax)
	if err != nil {
		return OllamaConfig{}, err
	}
	if backoffMax < backoffBase {
		return OllamaConfig{}, fmt.Errorf("OMCPA_OLLAMA_BACKOFF_MAX must be at least OMCPA_OLLAMA_BACKOFF_BASE: %q < %q", backoffMax, backoffBase)
	}
	return OllamaConfig{
		CollectorEnabled: enabled,
		Interval:         interval,
		Timeout:          timeout,
		BackoffBase:      backoffBase,
		BackoffMax:       backoffMax,
	}, nil
}

// optionalEnvString returns a pointer to a non-empty environment value, or nil
// when it is unset or empty. An empty value is not an override: it cannot erase a
// value the operator saved in the console.
func optionalEnvString(name string) *string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	return &raw
}

// parseBoolString reads one explicit boolean, reporting the variable name in the
// error. parseBoolEnv handles the unset case; this is the set case it shares.
func parseBoolString(name, raw string) (bool, error) {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %q", name, raw)
	}
	return value, nil
}

// parseDurationEnv reads a positive duration, or the fallback when it is unset.
func parseDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration: %q", name, raw)
	}
	return parsed, nil
}

// parseCredentialFloorByProvider reads the per-provider floor overrides as a
// comma-separated list of provider=duration entries. The documented defaults are
// the starting map and an entry overrides the provider it names, so naming one
// provider leaves every other default in place. A malformed entry is reported
// rather than ignored, matching every other setting in this file.
func parseCredentialFloorByProvider() (map[string]time.Duration, error) {
	floors := make(map[string]time.Duration, len(DefaultQuotaCredentialFloorByProvider))
	for provider, floor := range DefaultQuotaCredentialFloorByProvider {
		floors[provider] = floor
	}
	raw := strings.TrimSpace(os.Getenv("OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER"))
	if raw == "" {
		return floors, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		provider, value, found := strings.Cut(entry, "=")
		if !found {
			return nil, fmt.Errorf("OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER entry %q must be provider=duration", entry)
		}
		provider = normalizeProviderKey(provider)
		if provider == "" {
			return nil, fmt.Errorf("OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER entry %q must name a provider", entry)
		}
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER entry %q must be a positive duration", entry)
		}
		floors[provider] = parsed
	}
	return floors, nil
}

// normalizeProviderKey lowercases and trims a provider name so a configured
// override matches the key credential resolution produces. It mirrors
// quota.DetectProvider's normalization without importing that package.
func normalizeProviderKey(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
