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
	if s.Quotas == nil || s.Repo == nil {
		return QuotaPredictionResult{}, errors.New("capability_unavailable")
	}
	nowMS := capability.AnchorMS(ctx)
	readings, err := s.Quotas(ctx)
	if err != nil {
		return QuotaPredictionResult{}, err
	}
	result := QuotaPredictionResult{GeneratedAtMS: nowMS, Items: []QuotaPredictionItem{}}
	for _, reading := range readings {
		if input.Provider != "" && reading.Provider != input.Provider {
			continue
		}
		if input.AuthIndex != "" && reading.AuthIndex != input.AuthIndex {
			continue
		}
		for _, window := range reading.Windows {
			kind := predictionWindowKind(window)
			if input.WindowKind != "" && kind != input.WindowKind {
				continue
			}
			item := s.predictQuotaWindow(ctx, reading, window, kind, nowMS)
			item.CredentialLabel = accountLabelForCaller(ctx, item.CredentialLabel)
			if capability.IsReadOnly(ctx) {
				item.AuthIndex = ""
			}
			result.Items = append(result.Items, item)
		}
	}
	sort.SliceStable(result.Items, func(first, second int) bool {
		a, b := result.Items[first], result.Items[second]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.CredentialLabel != b.CredentialLabel {
			return a.CredentialLabel < b.CredentialLabel
		}
		return a.WindowKind < b.WindowKind
	})
	if len(result.Items) > limit {
		result.Omitted += len(result.Items) - limit
		result.Items = result.Items[:limit]
	}
	boundQuotaPrediction(&result)
	return result, nil
}

func (s *Service) predictQuotaWindow(ctx context.Context, reading Quota, window quota.QuotaWindow, kind string, nowMS int64) QuotaPredictionItem {
	observedAtMS := reading.ObservedAtMS
	if window.ObservedAtMS != nil {
		observedAtMS = *window.ObservedAtMS
	}
	predictionInput := quota.PredictionInput{Window: window, ObservedAtMS: observedAtMS, Status: reading.Status, NowMS: nowMS}
	fromMS, toMS, boundaryReason := quota.WindowCycleRange(window, observedAtMS, nowMS)
	if boundaryReason == "" {
		rows, hasMore, err := s.Repo.ListQuotaObservations(ctx, repository.QuotaObservationFilter{
			AuthIndex: reading.AuthIndex, Provider: reading.Provider, WindowKind: kind,
			FromMS: fromMS, ToMS: toMS,
		}, quotaPredictionHistoryLimit)
		if err == nil && !hasMore {
			for _, row := range rows {
				if row.Scope != window.Scope || row.Model != window.Model {
					continue
				}
				predictionInput.Observations = append(predictionInput.Observations, quota.PredictionObservation{
					ObservedAtMS: row.ObservedAtMS, UsedPercent: row.UsedPercent, ResetAtMS: row.ResetAtMS,
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
		CredentialLabel: reading.Name, Provider: reading.Provider, WindowKind: kind, AuthIndex: reading.AuthIndex,
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
	if kind := inferAdviceWindowKind(window); kind != "" {
		return kind
	}
	if window.Scope == "model" || window.Scope == "group" {
		return "model_scoped"
	}
	if strings.TrimSpace(window.ID) != "" {
		return strings.TrimSpace(window.ID)
	}
	return "unknown"
}

func boundQuotaPrediction(result *QuotaPredictionResult) {
	for len(result.Items) > 0 {
		encoded, _ := json.Marshal(result)
		if len(encoded) <= quotaPredictionByteLimit {
			return
		}
		result.Items = result.Items[:len(result.Items)-1]
		result.Omitted++
	}
}
