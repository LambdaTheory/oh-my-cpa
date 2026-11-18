package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

// OpenCodeCompatibilityName is the exact name of the openai-compatibility entry
// whose upstream publishes per-plan usage. The name is matched exactly (case
// and outer whitespace aside): other compatibility entries share this vendor's
// models, and a substring match would send one of their keys to this host.
const OpenCodeCompatibilityName = "opencode"

// OpenCodeUsageURL is the usage endpoint of the opencode entry. The reader
// derives the URL from the entry's own base-url at run time; this constant is
// the allowlist's exact endpoint, so a configuration that names a different
// upstream fails closed instead of sending the credential somewhere unverified.
//
// The host was confirmed through the providers catalogue (endpoint_host
// opencode.ai); the path is the one recorded for this same integration in
// docs/delivery/2026-11-12-omc-three-credential-quota/impl-plan.md.
const OpenCodeUsageURL = "https://opencode.ai/zen/go/v1/usage"

// openAICompatibilityReader is the management read the OpenCode reader needs
// beyond the core quota client. *management.Client implements it; keeping it
// separate means a bare CPAClient double does not have to grow the method.
type openAICompatibilityReader interface {
	OpenAICompatibility(context.Context) (management.OpenAICompatibilityResponse, error)
}

// openCodeUsageURL resolves the configured usage endpoint. The credential's
// auth index must belong to the entry when CPA lists the entry's keys: the
// api-call would otherwise attach another provider's credential to a request
// sent to opencode.ai. Legacy api-keys carry no auth index in the v0 view, so
// those entries are accepted as they are.
func (s *Service) openCodeUsageURL(ctx context.Context, authIndex string) (string, error) {
	if s.client == nil {
		return "", errors.New("CPA client is not configured")
	}
	reader, ok := s.client.(openAICompatibilityReader)
	if !ok {
		return "", errors.New("CPA client cannot read openai-compatibility entries")
	}
	response, err := reader.OpenAICompatibility(ctx)
	if err != nil {
		return "", fmt.Errorf("read openai-compatibility entries: %w", err)
	}
	matches := make([]management.OpenAICompatibility, 0, 1)
	for _, entry := range response.Entries {
		if entry.Disabled || !strings.EqualFold(strings.TrimSpace(entry.Name), OpenCodeCompatibilityName) {
			continue
		}
		matches = append(matches, entry)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no enabled %q openai-compatibility entry", OpenCodeCompatibilityName)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("%d enabled %q openai-compatibility entries; refusing to guess which one serves this credential", len(matches), OpenCodeCompatibilityName)
	}
	entry := matches[0]
	base := strings.TrimRight(strings.TrimSpace(entry.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("%q openai-compatibility entry has no base-url", OpenCodeCompatibilityName)
	}
	if parsed, err := url.Parse(base); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("%q openai-compatibility entry has an unusable base-url", OpenCodeCompatibilityName)
	}
	if authIndex = strings.TrimSpace(authIndex); authIndex != "" && len(entry.APIKeyEntries) > 0 {
		belongs := false
		for _, key := range entry.APIKeyEntries {
			if strings.TrimSpace(key.AuthIndex) == authIndex {
				belongs = true
				break
			}
		}
		if !belongs {
			return "", fmt.Errorf("auth_index %q is not a key of the %q openai-compatibility entry", authIndex, OpenCodeCompatibilityName)
		}
	}
	return base + "/usage", nil
}

// fetchOpenCodeQuota reads the plan usage document through CPA's api-call with
// the credential under observation.
func (s *Service) fetchOpenCodeQuota(ctx context.Context, file management.AuthFile, nowMS int64) (*QuotaPlan, []QuotaWindow, error) {
	target, err := s.openCodeUsageURL(ctx, file.AuthIndex)
	if err != nil {
		return nil, nil, err
	}
	headers := management.WithQuotaCredential(map[string]string{
		"Accept": "application/json",
	})
	resp, err := s.SafeApiCall(ctx, file.AuthIndex, http.MethodGet, target, headers, "")
	if err != nil {
		return nil, nil, err
	}
	body, err := resp.NormalizedBody()
	if err != nil {
		return nil, nil, err
	}
	windows, err := ParseOpenCodeUsage(body, nowMS)
	if err != nil {
		return nil, nil, err
	}
	return nil, windows, nil
}

type rawOpenCodeWindow struct {
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resetsAt"`
}

// ParseOpenCodeUsage projects the usage document onto the shared window shape.
// percent is a 0–100 share: the upstream plugin rejects anything outside that
// range and converts with remainingFraction = 1 - percent/100, so a value such
// as 0.5 is half a percent, not fifty. The plugin also treats an unparseable
// resetsAt or an out-of-range percentage as a failed reading; OMC does the same
// so a malformed document cannot half-update a snapshot.
func ParseOpenCodeUsage(raw []byte, nowMS int64) ([]QuotaWindow, error) {
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return nil, errors.New("invalid OpenCode usage payload")
	}
	var payload struct {
		Usage map[string]rawOpenCodeWindow `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid OpenCode usage payload: %w", err)
	}
	if len(payload.Usage) == 0 {
		return nil, errors.New("OpenCode usage payload has no windows")
	}

	specs := []struct {
		name        string
		label       string
		kind        string
		periodHours *float64
	}{
		{"rolling", "滚动用量 (Rolling)", "rolling", nil},
		{"weekly", "每周限制 (Weekly)", "weekly", floatPointer(168)},
		{"monthly", "每月限制 (Monthly)", "monthly", floatPointer(720)},
	}
	windows := make([]QuotaWindow, 0, len(specs))
	for _, spec := range specs {
		rawWindow, exists := payload.Usage[spec.name]
		if !exists || rawWindow.Percent == nil {
			continue
		}
		percent := *rawWindow.Percent
		if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
			return nil, fmt.Errorf("invalid OpenCode %s percentage", spec.name)
		}
		window := QuotaWindow{
			ID:               spec.name,
			Label:            spec.label,
			Kind:             spec.kind,
			Scope:            "standard",
			UsedPercent:      floatPointer(percent),
			RemainingPercent: floatPointer(100 - percent),
			PeriodHours:      spec.periodHours,
		}
		if rawWindow.ResetsAt != "" {
			resetAtMS, ok := parseOpenCodeReset(rawWindow.ResetsAt)
			if !ok {
				return nil, fmt.Errorf("invalid OpenCode %s reset time", spec.name)
			}
			window.ResetAtMS = &resetAtMS
			window.ResetLabel = formatResetInstant(resetAtMS, nowMS)
			window.ResetAccuracy = "exact"
		}
		windows = append(windows, window)
	}
	if len(windows) == 0 {
		return nil, errors.New("OpenCode usage payload has no readings")
	}
	return windows, nil
}

func parseOpenCodeReset(raw string) (int64, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if instant, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return instant.UnixMilli(), true
		}
	}
	return 0, false
}
