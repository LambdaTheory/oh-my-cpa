package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

const (
	quotaPredictionDefaultLimit = 100
	quotaPredictionMaxLimit     = 100
	quotaPredictionByteLimit    = 32 * 1024
	quotaPredictionHistoryLimit = 5000
)

type QuotaPredictionInput struct {
	Provider   string `json:"provider,omitempty" jsonschema:"Provider such as codex, claude or antigravity; omit for all"`
	WindowKind string `json:"window_kind,omitempty" jsonschema:"five_hour, weekly, model_scoped or another normalized kind; omit for all"`
	AuthIndex  string `json:"auth_index,omitempty" jsonschema:"Credential auth index for console or Agent callers; read-only MCP callers must omit it"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum predictions to return, 1 to 100; defaults to 100"`
	// FindingsOnly is an internal digest projection. It is deliberately absent
	// from the capability schema so callers cannot change the public contract.
	FindingsOnly bool `json:"-" jsonschema:"-"`
}

type QuotaPredictionItem struct {
	CredentialLabel           string   `json:"credential_label"`
	Provider                  string   `json:"provider"`
	WindowKind                string   `json:"window_kind"`
	AuthIndex                 string   `json:"auth_index,omitempty"`
	UsedPercent               *float64 `json:"used_percent"`
	ResetAtMS                 *int64   `json:"reset_at_ms"`
	ObservedAtMS              int64    `json:"observed_at_ms"`
	DataAgeSeconds            *int64   `json:"data_age_seconds"`
	BurnRatePerHour           *float64 `json:"burn_rate_per_hour"`
	ProjectedPercentAtReset   *float64 `json:"projected_percent_at_reset"`
	ProjectedTokensAtReset    *int64   `json:"projected_tokens_at_reset"`
	ProjectedCostNanosAtReset *int64   `json:"projected_cost_nanos_at_reset"`
	ExhaustionETAMS           *int64   `json:"exhaustion_eta_ms"`
	Verdict                   string   `json:"verdict"`
	Evidence                  string   `json:"evidence"`
	Reasons                   []string `json:"reasons"`
}

type QuotaPredictionResult struct {
	GeneratedAtMS int64                 `json:"generated_at_ms"`
	Items         []QuotaPredictionItem `json:"items"`
	Omitted       int                   `json:"omitted"`
}

func (s *Service) registerQuotaPrediction(registry *capability.Registry) error {
	return read(registry, "quota_prediction", "Project each stored quota window to its reset from passive and active observations plus recorded usage. Separates fact, projection and unknown evidence; never refreshes quota or changes routing.", s.QuotaPrediction)
}

func (s *Service) QuotaPrediction(ctx context.Context, input QuotaPredictionInput) (QuotaPredictionResult, error) {
	if input.Provider != "" && !quotaObservationToken.MatchString(input.Provider) {
		return QuotaPredictionResult{}, invalidParameters{detail: "provider must be a lowercase identifier"}
	}
	if input.WindowKind != "" && !quotaObservationToken.MatchString(input.WindowKind) {
		return QuotaPredictionResult{}, invalidParameters{detail: "window_kind must be a lowercase identifier"}
	}
	if capability.IsReadOnly(ctx) && strings.TrimSpace(input.AuthIndex) != "" {
		return QuotaPredictionResult{}, invalidParameters{detail: "read-only MCP callers must omit auth_index"}
	}
	limit := input.Limit
	if limit == 0 {
		limit = quotaPredictionDefaultLimit
	}
	if limit < 1 || limit > quotaPredictionMaxLimit {
		return QuotaPredictionResult{}, invalidParameters{detail: "limit must be between 1 and 100"}
	}
	if s.Repo == nil {
		return QuotaPredictionResult{}, errors.New("capability_unavailable")
	}
	nowMS := capability.AnchorMS(ctx)
	readings, err := s.storedPredictionQuotas(ctx)
	if err != nil {
		return QuotaPredictionResult{}, err
	}
	result := QuotaPredictionResult{GeneratedAtMS: nowMS, Items: []QuotaPredictionItem{}}
	type predictionCandidate struct {
		item           QuotaPredictionItem
		authIndex      string
		windowIdentity string
	}
	candidates := make([]predictionCandidate, 0)
	for _, reading := range readings {
		if input.Provider != "" && reading.Provider != input.Provider {
			continue
		}
		if input.AuthIndex != "" && reading.AuthIndex != input.AuthIndex {
			continue
		}
		for _, window := range reading.Windows {
			reportedKind := predictionWindowKind(window)
			if input.WindowKind != "" && reportedKind != input.WindowKind {
				continue
			}
			storedKind := quota.WindowObservationKind(reading.Provider, window)
			item := s.predictQuotaWindow(ctx, reading, window, reportedKind, storedKind, nowMS)
			if input.FindingsOnly && item.Verdict != quota.PredictionVerdictWillExhaust && item.Verdict != quota.PredictionVerdictUnderUsed {
				continue
			}
			candidates = append(candidates, predictionCandidate{
				item: item, authIndex: reading.AuthIndex,
				windowIdentity: strings.Join([]string{window.ID, window.Kind, window.Scope, window.Model, strings.Join(window.ModelFamilies, "\x00"), storedKind}, "\x00"),
			})
		}
	}
	sort.Slice(candidates, func(first, second int) bool {
		a, b := candidates[first], candidates[second]
		if a.item.Provider != b.item.Provider {
			return a.item.Provider < b.item.Provider
		}
		if a.item.CredentialLabel != b.item.CredentialLabel {
			return a.item.CredentialLabel < b.item.CredentialLabel
		}
		if a.item.WindowKind != b.item.WindowKind {
			return a.item.WindowKind < b.item.WindowKind
		}
		if a.authIndex != b.authIndex {
			return a.authIndex < b.authIndex
		}
		return a.windowIdentity < b.windowIdentity
	})
	for _, candidate := range candidates {
		item := candidate.item
		item.CredentialLabel = accountLabelForCaller(ctx, item.CredentialLabel)
		if capability.IsReadOnly(ctx) {
			item.AuthIndex = ""
		}
		result.Items = append(result.Items, item)
	}
	if !input.FindingsOnly && len(result.Items) > limit {
		result.Omitted += len(result.Items) - limit
		result.Items = result.Items[:limit]
	}
	if !input.FindingsOnly {
		if err := boundQuotaPrediction(&result); err != nil {
			return QuotaPredictionResult{}, err
		}
	}
	return result, nil
}

// storedPredictionQuotas assembles the prediction roster from OMC persistence.
// It never asks CPA which credentials exist or refreshes a quota window.
func (s *Service) storedPredictionQuotas(ctx context.Context) ([]Quota, error) {
	metadata, err := s.Repo.ListFeishuCredentialMetadata(ctx)
	if err != nil {
		return nil, err
	}
	if len(metadata) == 0 {
		return []Quota{}, nil
	}
	authIndexes := make([]string, 0, len(metadata))
	for _, entry := range metadata {
		authIndexes = append(authIndexes, entry.AuthIndex)
	}
	snapshots, err := s.Repo.GetLatestQuotaSnapshots(ctx, authIndexes)
	if err != nil {
		return nil, err
	}
	passive, err := s.Repo.LatestStandardPassiveObservations(ctx, authIndexes)
	if err != nil {
		return nil, err
	}
	result := make([]Quota, 0, len(metadata))
	for _, entry := range metadata {
		snapshot, ok := snapshots[entry.AuthIndex]
		if !ok {
			continue
		}
		var windows []quota.QuotaWindow
		if err := json.Unmarshal([]byte(snapshot.WindowsJSON), &windows); err != nil {
			continue
		}
		provider := strings.TrimSpace(snapshot.Provider)
		if provider == "" {
			provider = quota.DetectProvider("", entry.Driver)
		}
		for index := range windows {
			window := &windows[index]
			kind, sharesPassiveStream := quota.PassiveWindowKindFor(provider, *window)
			if !sharesPassiveStream {
				continue
			}
			observation, found := passive[entry.AuthIndex][kind]
			if !found || observation.ObservedAtMS <= snapshot.ObservedAtMS {
				continue
			}
			usedPercent := observation.UsedPercent
			window.UsedPercent = &usedPercent
			window.RemainingPercent = floatPointer(max(0, 100-usedPercent))
			window.ResetAtMS = observation.ResetAtMS
			observedAtMS := observation.ObservedAtMS
			window.ObservedAtMS = &observedAtMS
			window.Source = observation.Source
		}
		result = append(result, Quota{
			AuthIndex: entry.AuthIndex, Name: safeLabel(entry.Name), Provider: provider,
			Status: snapshot.Status, ObservedAtMS: snapshot.ObservedAtMS, Windows: windows,
		})
	}
	return result, nil
}

func floatPointer(value float64) *float64 { return &value }

func (s *Service) predictQuotaWindow(ctx context.Context, reading Quota, window quota.QuotaWindow, reportedKind, storedKind string, nowMS int64) QuotaPredictionItem {
	observedAtMS := reading.ObservedAtMS
	if window.ObservedAtMS != nil {
		observedAtMS = *window.ObservedAtMS
	}
	predictionInput := quota.PredictionInput{Window: window, ObservedAtMS: observedAtMS, Status: reading.Status, NowMS: nowMS}
	fromMS, toMS, boundaryReason := quota.WindowCycleRange(window, observedAtMS, nowMS)
	if boundaryReason == "" {
		rows, hasMore, err := s.Repo.ListQuotaObservations(ctx, repository.QuotaObservationFilter{
			AuthIndex: reading.AuthIndex, Provider: reading.Provider, WindowKind: storedKind,
			FromMS: fromMS, ToMS: toMS,
		}, quotaPredictionHistoryLimit)
		if err == nil && !hasMore {
			for _, row := range rows {
				if row.Scope != window.Scope || row.Model != window.Model {
					continue
				}
				predictionInput.Observations = append(predictionInput.Observations, quota.PredictionObservation{
					ObservedAtMS: row.ObservedAtMS, UsedPercent: row.UsedPercent, ResetAtMS: row.ResetAtMS, Source: row.Source,
				})
			}
		} else {
			predictionInput.Window.HasIncompleteHistory = true
		}
		usage, reason := s.predictionUsage(ctx, reading.AuthIndex, window, fromMS, toMS)
		predictionInput.Usage, predictionInput.UsageReason = usage, reason
	}
	predicted := quota.PredictWindow(predictionInput)
	return QuotaPredictionItem{
		CredentialLabel: reading.Name, Provider: reading.Provider, WindowKind: reportedKind, AuthIndex: reading.AuthIndex,
		UsedPercent: predicted.UsedPercent, ResetAtMS: predicted.ResetAtMS, ObservedAtMS: predicted.ObservedAtMS,
		DataAgeSeconds: predicted.DataAgeSeconds, BurnRatePerHour: predicted.BurnRatePerHour,
		ProjectedPercentAtReset: predicted.ProjectedPercentAtReset, ProjectedTokensAtReset: predicted.ProjectedTokensAtReset,
		ProjectedCostNanosAtReset: predicted.ProjectedCostNanosAtReset, ExhaustionETAMS: predicted.ExhaustionETAMS,
		Verdict: predicted.Verdict, Evidence: predicted.Evidence, Reasons: predicted.Reasons,
	}
}

func (s *Service) predictionUsage(ctx context.Context, authIndex string, window quota.QuotaWindow, fromMS, toMS int64) (*quota.WindowUsage, string) {
	if window.Scope == "model" || window.Scope == "group" {
		rows, err := s.Repo.QueryCredentialModelWindowUsage(ctx, authIndex, fromMS, toMS)
		if err != nil {
			return nil, quota.PredictionReasonUsageUnavailable
		}
		groups := make([]quota.ModelWindowUsage, 0, len(rows))
		for _, row := range rows {
			groups = append(groups, quota.ModelWindowUsage{Model: row.Model, Usage: quota.WindowUsage{Requests: row.Requests, PricedRequests: row.PricedRequests, Tokens: row.Tokens, CostNanos: row.CostNanos}})
		}
		usage, reason := quota.SelectWindowUsage(window, groups)
		if reason != "" {
			return nil, reason
		}
		usage.FromMS, usage.ToMS = fromMS, toMS
		return &usage, ""
	}
	row, err := s.Repo.QueryCredentialWindowUsage(ctx, authIndex, fromMS, toMS)
	if err != nil {
		return nil, quota.PredictionReasonUsageUnavailable
	}
	return &quota.WindowUsage{FromMS: fromMS, ToMS: toMS, Requests: row.Requests, PricedRequests: row.PricedRequests, Tokens: row.Tokens, CostNanos: row.CostNanos}, ""
}

func predictionWindowKind(window quota.QuotaWindow) string {
	if window.Scope == "model" || window.Scope == "group" {
		return "model_scoped"
	}
	if kind := inferAdviceWindowKind(window); kind != "" {
		return kind
	}
	if strings.TrimSpace(window.ID) != "" {
		return strings.TrimSpace(window.ID)
	}
	return "unknown"
}

func boundQuotaPrediction(result *QuotaPredictionResult) error {
	for len(result.Items) > 0 {
		encoded, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal quota prediction: %w", err)
		}
		if len(encoded) <= quotaPredictionByteLimit {
			return nil
		}
		result.Items = result.Items[:len(result.Items)-1]
		result.Omitted++
	}
	_, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal quota prediction: %w", err)
	}
	return nil
}
