package operations

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type ResetAdviceInput struct {
	Provider   string `json:"provider,omitempty" jsonschema:"Provider identifier; omit for all stored credentials"`
	WindowKind string `json:"window_kind,omitempty" jsonschema:"Normalized window kind such as five_hour or weekly; omit for all"`
	AuthIndex  string `json:"auth_index,omitempty" jsonschema:"Credential auth index for console or Agent; read-only MCP callers must omit it"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum rows, 1 to 100; defaults to 100"`
}

type ResetAdviceItem struct {
	CredentialLabel   string   `json:"credential_label"`
	Provider          string   `json:"provider"`
	AuthIndex         string   `json:"auth_index,omitempty"`
	WindowKind        string   `json:"window_kind"`
	WindowID          string   `json:"window_id"`
	RemainingResets   *int     `json:"remaining_resets"`
	EarliestExpiryMS  *int64   `json:"earliest_expiry_ms"`
	UsedPercent       *float64 `json:"used_percent"`
	ResetObservedAtMS int64    `json:"reset_observed_at_ms"`
	QuotaObservedAtMS int64    `json:"quota_observed_at_ms"`
	Verdict           string   `json:"verdict"`
	Evidence          string   `json:"evidence"`
	Reasons           []string `json:"reasons"`
}

type ResetAdviceResult struct {
	GeneratedAtMS int64             `json:"generated_at_ms"`
	Items         []ResetAdviceItem `json:"items"`
	Omitted       int               `json:"omitted"`
}

func (s *Service) registerResetAdvice(registry *capability.Registry) error {
	return read(registry, "reset_advice", "Advise whether stored reset entitlements should be used before expiry or kept for recovery. Returns fact, projection or unknown evidence; never refreshes quota, redeems a reset or changes routing.", s.ResetAdvice)
}

func (s *Service) ResetAdvice(ctx context.Context, input ResetAdviceInput) (ResetAdviceResult, error) {
	if input.Provider != "" && !quotaObservationToken.MatchString(input.Provider) {
		return ResetAdviceResult{}, invalidParameters{detail: "provider must be a lowercase identifier"}
	}
	if input.WindowKind != "" && !quotaObservationToken.MatchString(input.WindowKind) {
		return ResetAdviceResult{}, invalidParameters{detail: "window_kind must be a lowercase identifier"}
	}
	if capability.IsReadOnly(ctx) && strings.TrimSpace(input.AuthIndex) != "" {
		return ResetAdviceResult{}, invalidParameters{detail: "read-only MCP callers must omit auth_index"}
	}
	limit := input.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 100 {
		return ResetAdviceResult{}, invalidParameters{detail: "limit must be between 1 and 100"}
	}
	if s.Repo == nil {
		return ResetAdviceResult{}, errors.New("capability_unavailable")
	}
	metadata, err := s.Repo.ListFeishuCredentialMetadata(ctx)
	if err != nil {
		return ResetAdviceResult{}, err
	}
	indexes := make([]string, 0, len(metadata))
	for _, entry := range metadata {
		indexes = append(indexes, entry.AuthIndex)
	}
	snapshots, err := s.Repo.GetLatestQuotaSnapshots(ctx, indexes)
	if err != nil {
		return ResetAdviceResult{}, err
	}
	passive, err := s.Repo.LatestStandardPassiveObservations(ctx, indexes)
	if err != nil {
		return ResetAdviceResult{}, err
	}
	result := ResetAdviceResult{GeneratedAtMS: capability.AnchorMS(ctx), Items: []ResetAdviceItem{}}
	for _, entry := range metadata {
		if input.AuthIndex != "" && input.AuthIndex != entry.AuthIndex {
			continue
		}
		snapshot := snapshots[entry.AuthIndex]
		provider := snapshot.Provider
		if provider == "" {
			provider = quota.DetectProvider("", entry.Driver)
		}
		if input.Provider != "" && input.Provider != provider {
			continue
		}
		var windows []quota.QuotaWindow
		if json.Unmarshal([]byte(snapshot.WindowsJSON), &windows) != nil {
			windows = nil
		}
		// Missing snapshots and windows remain visible as unknown evidence.
		if len(windows) == 0 {
			windows = []quota.QuotaWindow{{Kind: input.WindowKind, Scope: "standard"}}
		}
		for _, window := range windows {
			kind := predictionWindowKind(window)
			if input.WindowKind != "" && input.WindowKind != kind {
				continue
			}
			if passiveKind, ok := quota.PassiveWindowKindFor(provider, window); ok {
				observation, exists := passive[entry.AuthIndex][passiveKind]
				if exists && observation.Provider == provider && observation.ObservedAtMS > snapshot.ObservedAtMS {
					window.UsedPercent = &observation.UsedPercent
					window.ResetAtMS = observation.ResetAtMS
					window.ObservedAtMS = &observation.ObservedAtMS
				}
			}
			adviceInput := quota.ResetAdviceInput{Provider: provider, Window: window, Status: snapshot.Status, ObservedAtMS: snapshot.ObservedAtMS, NowMS: result.GeneratedAtMS, ExpiryWindow: s.ResetAdviceExpiryWindow}
			decodeResetAdviceEntitlements(snapshot, &adviceInput)
			advice := quota.AdviseReset(adviceInput)
			observedAtMS := snapshot.ObservedAtMS
			if window.ObservedAtMS != nil {
				observedAtMS = *window.ObservedAtMS
			}
			item := ResetAdviceItem{CredentialLabel: safeLabel(entry.Name), Provider: provider, AuthIndex: entry.AuthIndex, WindowKind: kind, WindowID: window.ID,
				RemainingResets: advice.RemainingResets, EarliestExpiryMS: advice.EarliestExpiryMS, UsedPercent: advice.UsedPercent,
				ResetObservedAtMS: snapshot.ObservedAtMS, QuotaObservedAtMS: observedAtMS, Verdict: advice.Verdict, Evidence: advice.Evidence, Reasons: advice.Reasons}
			result.Items = append(result.Items, item)
		}
	}
	sort.SliceStable(result.Items, func(i, j int) bool {
		a, b := result.Items[i], result.Items[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.AuthIndex != b.AuthIndex {
			return a.AuthIndex < b.AuthIndex
		}
		return a.WindowID < b.WindowID
	})
	if len(result.Items) > limit {
		result.Omitted = len(result.Items) - limit
		result.Items = result.Items[:limit]
	}
	for index := range result.Items {
		item := &result.Items[index]
		item.CredentialLabel = accountLabelForCaller(ctx, item.CredentialLabel)
		if capability.IsReadOnly(ctx) {
			item.AuthIndex = ""
		}
	}
	for {
		encoded, err := json.Marshal(result)
		if err != nil {
			return ResetAdviceResult{}, err
		}
		if len(encoded) <= 32*1024 {
			return result, nil
		}
		result.Items = result.Items[:len(result.Items)-1]
		result.Omitted++
	}
}

func decodeResetAdviceEntitlements(snapshot repository.QuotaSnapshotRecord, input *quota.ResetAdviceInput) {
	// Require a published count; a partial stored JSON object must not become zero.
	if input.Provider == "codex" {
		var presence struct {
			AvailableCount *int `json:"available_count"`
		}
		var credits quota.CodexResetCreditsInfo
		if json.Unmarshal([]byte(snapshot.ResetCreditsJSON), &presence) == nil && presence.AvailableCount != nil && json.Unmarshal([]byte(snapshot.ResetCreditsJSON), &credits) == nil {
			input.ResetCredits = &credits
		}
	}
	if input.Provider == "claude" {
		var grants []quota.ClaudeResetGrant
		var presence []struct {
			Left  *int `json:"resets_left"`
			Total *int `json:"resets_total"`
		}
		if json.Unmarshal([]byte(snapshot.ResetGrantsJSON), &presence) != nil {
			return
		}
		for _, grant := range presence {
			if grant.Left == nil || grant.Total == nil {
				return
			}
		}
		if json.Unmarshal([]byte(snapshot.ResetGrantsJSON), &grants) == nil {
			input.ResetGrants = grants
		}
	}
}
