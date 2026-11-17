package quota

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

const openCodeUsageFixture = `{"usage":{
	"rolling":{"percent":0.0,"resetsAt":"2026-11-18T00:00:00.000Z"},
	"weekly":{"percent":42.0,"resetsAt":"2026-11-23T12:00:00Z"},
	"monthly":{"percent":21.0,"resetsAt":"2026-12-06T04:03:37.123456789Z"}}}`

type openCodeQuotaClient struct {
	mockCPAClient
	compatibility func(context.Context) (management.OpenAICompatibilityResponse, error)
}

func (client *openCodeQuotaClient) OpenAICompatibility(ctx context.Context) (management.OpenAICompatibilityResponse, error) {
	return client.compatibility(ctx)
}

func openCodeCompatibilityClient(entry management.OpenAICompatibility) *openCodeQuotaClient {
	return &openCodeQuotaClient{compatibility: func(context.Context) (management.OpenAICompatibilityResponse, error) {
		return management.OpenAICompatibilityResponse{Entries: []management.OpenAICompatibility{
			{Name: "Command Code", BaseURL: "https://api.commandcode.ai/provider/v1"},
			entry,
		}}, nil
	}}
}

func TestParseOpenCodeUsageProjectsTheThreeWindows(t *testing.T) {
	const nowMS int64 = 1794900000000
	windows, err := ParseOpenCodeUsage([]byte(openCodeUsageFixture), nowMS)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 3 {
		t.Fatalf("windows = %+v, want rolling, weekly, monthly", windows)
	}

	rolling := windows[0]
	if rolling.ID != "rolling" || rolling.Kind != "rolling" || rolling.PeriodHours != nil ||
		!percentIs(rolling.UsedPercent, 0) || !percentIs(rolling.RemainingPercent, 100) ||
		rolling.ResetAtMS == nil || *rolling.ResetAtMS != time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC).UnixMilli() || rolling.ResetAccuracy != "exact" {
		t.Fatalf("rolling = %+v", rolling)
	}

	weekly := windows[1]
	if weekly.ID != "weekly" || weekly.Kind != "weekly" || weekly.PeriodHours == nil || *weekly.PeriodHours != 168 ||
		!percentIs(weekly.UsedPercent, 42) || !percentIs(weekly.RemainingPercent, 58) || weekly.ResetAccuracy != "exact" {
		t.Fatalf("weekly = %+v", weekly)
	}

	monthly := windows[2]
	if monthly.ID != "monthly" || monthly.Kind != "monthly" || monthly.PeriodHours == nil || *monthly.PeriodHours != 720 ||
		!percentIs(monthly.UsedPercent, 21) || !percentIs(monthly.RemainingPercent, 79) ||
		monthly.ResetAtMS == nil || *monthly.ResetAtMS != time.Date(2026, 12, 6, 4, 3, 37, 123456789, time.UTC).UnixMilli() {
		t.Fatalf("monthly = %+v", monthly)
	}
}

// The upstream plugin defines percent as a 0–100 share and converts it with
// remainingFraction = 1 - percent/100. A fraction-shaped reading must therefore
// stay a fraction-sized percentage instead of being scaled into 50%.
func TestParseOpenCodeUsagePinsThePercentDimension(t *testing.T) {
	windows, err := ParseOpenCodeUsage([]byte(`{"usage":{"weekly":{"percent":0.5,"resetsAt":"2026-11-23T12:00:00Z"}}}`), 1794900000000)
	if err != nil || len(windows) != 1 {
		t.Fatalf("windows = %+v, err = %v", windows, err)
	}
	if !percentIs(windows[0].UsedPercent, 0.5) || !percentIs(windows[0].RemainingPercent, 99.5) {
		t.Fatalf("percent dimension = %+v", windows[0])
	}
}

func TestParseOpenCodeUsageSkipsUnstatedWindowsAndRefusesEmptyReadings(t *testing.T) {
	windows, err := ParseOpenCodeUsage([]byte(`{"usage":{"rolling":{},"weekly":{"percent":42},"monthly":{"percent":null}}}`), 1794900000000)
	if err != nil || len(windows) != 1 || windows[0].ID != "weekly" {
		t.Fatalf("windows = %+v, err = %v", windows, err)
	}
	if windows[0].ResetAtMS != nil || windows[0].ResetAccuracy != "" {
		t.Fatalf("a missing reset became a cycle: %+v", windows[0])
	}
	for _, raw := range []string{`{"usage":{}}`, `{}`, `null`, `[]`, `not-json`} {
		if _, err := ParseOpenCodeUsage([]byte(raw), 0); err == nil {
			t.Fatalf("payload without readings accepted: %q", raw)
		}
	}
}

func TestParseOpenCodeUsageRefusesInvalidPercentagesAndResets(t *testing.T) {
	for _, raw := range []string{
		`{"usage":{"weekly":{"percent":101,"resetsAt":"2026-11-23T12:00:00Z"}}}`,
		`{"usage":{"weekly":{"percent":-1,"resetsAt":"2026-11-23T12:00:00Z"}}}`,
		`{"usage":{"weekly":{"percent":"NaN"}}}`,
		`{"usage":{"weekly":{"percent":42,"resetsAt":"not-a-time"}}}`,
		`{"usage":{"weekly":{"percent":42,"resetsAt":"2026-13-40T25:61:61Z"}}}`,
	} {
		if _, err := ParseOpenCodeUsage([]byte(raw), 0); err == nil {
			t.Fatalf("invalid usage payload accepted: %s", raw)
		}
	}
}

func TestOpenCodeUsageURLResolvesTheExactEntry(t *testing.T) {
	client := openCodeCompatibilityClient(management.OpenAICompatibility{
		Name:          "opencode",
		BaseURL:       "https://opencode.ai/zen/go/v1/",
		APIKeyEntries: []management.APIKeyEntry{{APIKey: "op-key", AuthIndex: "op-auth"}},
	})
	target, err := NewService(client).openCodeUsageURL(context.Background(), "op-auth")
	if err != nil || target != OpenCodeUsageURL {
		t.Fatalf("target = %q, err = %v; want %q", target, err, OpenCodeUsageURL)
	}
}

func TestOpenCodeUsageURLRequiresUnambiguousExactNameAndOwnedKey(t *testing.T) {
	for name, client := range map[string]*openCodeQuotaClient{
		"no entry": {compatibility: func(context.Context) (management.OpenAICompatibilityResponse, error) {
			return management.OpenAICompatibilityResponse{Entries: []management.OpenAICompatibility{
				{Name: "opencode-go", BaseURL: "https://opencode.ai/zen/go/v1"},
			}}, nil
		}},
		"disabled entry": openCodeCompatibilityClient(management.OpenAICompatibility{
			Name: "opencode", BaseURL: "https://opencode.ai/zen/go/v1", Disabled: true,
		}),
		"two enabled entries": {compatibility: func(context.Context) (management.OpenAICompatibilityResponse, error) {
			return management.OpenAICompatibilityResponse{Entries: []management.OpenAICompatibility{
				{Name: "opencode", BaseURL: "https://opencode.ai/zen/go/v1"},
				{Name: "OpenCode", BaseURL: "https://opencode.ai/zen/go/v1"},
			}}, nil
		}},
		"key belongs elsewhere": openCodeCompatibilityClient(management.OpenAICompatibility{
			Name: "opencode", BaseURL: "https://opencode.ai/zen/go/v1",
			APIKeyEntries: []management.APIKeyEntry{{AuthIndex: "other-auth"}},
		}),
		"no base url": openCodeCompatibilityClient(management.OpenAICompatibility{Name: "opencode"}),
		"non-https base": openCodeCompatibilityClient(management.OpenAICompatibility{
			Name: "opencode", BaseURL: "http://opencode.ai/zen/go/v1",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewService(client).openCodeUsageURL(context.Background(), "op-auth"); err == nil {
				t.Fatal("ambiguous or unusable entry was accepted")
			}
		})
	}
}

func TestOpenCodeUsageURLAcceptsLegacyKeysWithoutAuthIndexes(t *testing.T) {
	client := openCodeCompatibilityClient(management.OpenAICompatibility{
		Name:          "opencode",
		BaseURL:       "https://opencode.ai/zen/go/v1",
		LegacyAPIKeys: []string{"legacy-key"},
	})
	target, err := NewService(client).openCodeUsageURL(context.Background(), "legacy-auth")
	if err != nil || target != OpenCodeUsageURL {
		t.Fatalf("target = %q, err = %v", target, err)
	}
}

func TestOpenCodeQuotaFetchUsesTheResolvedEndpoint(t *testing.T) {
	client := openCodeCompatibilityClient(management.OpenAICompatibility{
		Name: "opencode", BaseURL: "https://opencode.ai/zen/go/v1",
		APIKeyEntries: []management.APIKeyEntry{{AuthIndex: "op-auth"}},
	})
	called := 0
	client.apiCallFunc = func(ctx context.Context, request management.ApiCallRequest) (management.ApiCallResponse, error) {
		called++
		if request.URL != OpenCodeUsageURL || request.Method != "GET" || request.AuthIndex != "op-auth" ||
			request.Header["Authorization"] != management.QuotaTokenPlaceholder || request.Header["Accept"] != "application/json" {
			t.Fatalf("unsafe request: %+v", request)
		}
		return management.ApiCallResponse{StatusCode: 200, Body: json.RawMessage(openCodeUsageFixture)}, nil
	}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex: "op-auth", Name: "opencode fixture", Type: "openai-compatibility", Provider: "opencode",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || len(result.Windows) != 3 || result.Provider != "opencode" || !result.Capabilities.RefreshSupported {
		t.Fatalf("reading = %+v, calls = %d", result, called)
	}
}

// The allowlist is the boundary that stops a renamed or repointed entry from
// sending the credential somewhere unconfirmed: the configured base-url may
// differ, but the request must fail before it leaves OMC.
func TestOpenCodeQuotaFetchRefusesAnUnlistedBaseURL(t *testing.T) {
	client := openCodeCompatibilityClient(management.OpenAICompatibility{
		Name: "opencode", BaseURL: "https://opencode.ai/zen/v1",
	})
	called := 0
	client.apiCallFunc = func(context.Context, management.ApiCallRequest) (management.ApiCallResponse, error) {
		called++
		return management.ApiCallResponse{}, nil
	}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex: "op-auth", Type: "openai-compatibility", Provider: "opencode",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Error, "allowlist") {
		t.Fatalf("result error = %q, want an allowlist refusal", result.Error)
	}
	if called != 0 {
		t.Fatal("a request left OMC for an unconfirmed base URL")
	}
}

func TestOpenCodeQuotaRateLimitRetainsTheTypedBackoff(t *testing.T) {
	client := openCodeCompatibilityClient(management.OpenAICompatibility{
		Name: "opencode", BaseURL: "https://opencode.ai/zen/go/v1",
	})
	client.apiCallFunc = func(context.Context, management.ApiCallRequest) (management.ApiCallResponse, error) {
		return management.ApiCallResponse{
			StatusCode: 429,
			Header:     map[string][]string{"Retry-After": {"60"}},
			Body:       json.RawMessage(`{"error":{"message":"synthetic-private"}}`),
		}, nil
	}
	used := 10.0
	remaining := 90.0
	prior := &NormalizedQuota{
		AuthIndex:    "op-auth",
		ObservedAtMS: 1794800000000,
		Windows:      []QuotaWindow{{ID: "weekly", UsedPercent: &used, RemainingPercent: &remaining}},
	}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex: "op-auth", Type: "openai-compatibility", Provider: "opencode",
	}, prior)
	var rate *RateLimitError
	if !errors.As(err, &rate) || rate.RetryAfter.Seconds() != 60 || rate.Provider != "opencode" {
		t.Fatalf("err = %v, want the typed backoff", err)
	}
	if result.Status != "stale" || len(result.Windows) != 1 {
		t.Fatalf("prior windows not retained: %+v", result)
	}
}

func TestOpenCodeProviderKeyDoesNotCapturePluginCredentials(t *testing.T) {
	if got := DetectProvider("openai-compatible-opencode", "openai-compatible-opencode"); got != "openai-compatible-opencode" {
		t.Fatalf("plugin credential provider = %q; want its own key", got)
	}
	if got := DetectProvider("openai-compatibility", "opencode"); got != "opencode" {
		t.Fatalf("compatibility provider = %q, want opencode", got)
	}
	pluginFile := management.AuthFile{Type: "openai-compatible-opencode", Provider: "openai-compatible-opencode", SupportsQuota: true}
	if !isPluginQuotaFile(pluginFile) {
		t.Fatal("plugin credential was taken over by the built-in reader")
	}
	synthetic := management.AuthFile{Type: "openai-compatibility", Provider: "opencode", AuthIndex: "op-auth"}
	if isPluginQuotaFile(synthetic) || !CapabilitiesForFile(synthetic).RefreshSupported {
		t.Fatal("the opencode entry is not served by its built-in reader")
	}
}

// A window the upstream itself names rolling has no cycle to derive, even when
// it publishes no period. Recognising it by kind keeps it out of the boundary
// arithmetic instead of reporting a boundary that never exists.
func TestWindowCycleRangeRecognisesAStatedRollingKind(t *testing.T) {
	for _, kind := range []string{"rolling", "Rolling", " rolling "} {
		window := QuotaWindow{ID: "rolling", Kind: kind}
		fromMS, toMS, reason := WindowCycleRange(window, 7*hourMS, 7*hourMS)
		if reason != CapacityReasonRollingWindow || fromMS != 0 || toMS != 0 {
			t.Fatalf("kind %q: range = [%d, %d) reason %q, want the rolling reason", kind, fromMS, toMS, reason)
		}
	}
	// A kind the service does not state as rolling still needs a period.
	window := QuotaWindow{ID: "weekly", Kind: "weekly"}
	if _, _, reason := WindowCycleRange(window, 7*hourMS, 7*hourMS); reason != CapacityReasonBoundaryUnknown {
		t.Fatalf("period-less weekly window reason = %q, want boundary_unknown", reason)
	}
}
