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
	// A prediction card spends one element on metadata and one on its footer.
	// Keeping 178 findings therefore fits Feishu's 180-element ceiling, including
	// the footer's omitted-count disclosure.
	quotaPredictionFindingsLimit = 178
	quotaPredictionByteLimit     = 32 * 1024
	// The cap counts five-minute buckets, not raw observations, so a busy
	// weekly window (about 2,016 buckets) never becomes incomplete history.
	quotaPredictionBucketLimit = 5000

	quotaPredictionResetTolerancePercent = 1.0
	quotaPredictionResetToleranceMS      = 1000
)

type QuotaPredictionInput struct {
	IncludeRecovery bool   `json:"-" jsonschema:"-"`
	Provider        string `json:"provider,omitempty" jsonschema:"Provider such as codex, claude or antigravity; omit for all"`
	WindowKind      string `json:"window_kind,omitempty" jsonschema:"five_hour, weekly, model_scoped or another normalized kind; omit for all"`
	AuthIndex       string `json:"auth_index,omitempty" jsonschema:"Credential auth index for console or Agent callers; read-only MCP callers must omit it"`
	Limit           int    `json:"limit,omitempty" jsonschema:"Maximum predictions to return, 1 to 100; defaults to 100"`
	// FindingsOnly is an internal digest projection. It is deliberately absent
	// from the capability schema so callers cannot change the public contract.
	FindingsOnly bool `json:"-" jsonschema:"-"`
}

type QuotaPredictionItem struct {
	ResetAccuracy             string   `json:"-" jsonschema:"-"`
	CredentialLabel           string   `json:"credential_label"`
	Provider                  string   `json:"provider"`
	WindowKind                string   `json:"window_kind"`
	WindowID                  string   `json:"window_id"`
	WindowScope               string   `json:"window_scope"`
	WindowModel               string   `json:"window_model,omitempty"`
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
	SampleCount               int      `json:"sample_count"`
	CoverageSeconds           *int64   `json:"coverage_seconds"`
	MaxGapSeconds             *int64   `json:"max_gap_seconds"`
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
			if input.FindingsOnly && !input.IncludeRecovery && item.Verdict != quota.PredictionVerdictWillExhaust && item.Verdict != quota.PredictionVerdictUnderUsed {
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
		if input.FindingsOnly {
			aRank, bRank := predictionFindingRank(a.item), predictionFindingRank(b.item)
			if aRank != bRank {
				return aRank < bRank
			}
			if a.item.ExhaustionETAMS != nil && b.item.ExhaustionETAMS != nil && *a.item.ExhaustionETAMS != *b.item.ExhaustionETAMS {
				return *a.item.ExhaustionETAMS < *b.item.ExhaustionETAMS
			}
		}
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
	if input.FindingsOnly {
		if err := boundQuotaPredictionFindings(&result); err != nil {
			return QuotaPredictionResult{}, err
		}
	} else {
		if len(result.Items) > limit {
			result.Omitted += len(result.Items) - limit
			result.Items = result.Items[:limit]
		}
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
			if observation.ResetAtMS != nil {
				window.ResetAtMS = observation.ResetAtMS
			}
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

func predictionFindingRank(item QuotaPredictionItem) int {
	if item.Verdict == quota.PredictionVerdictWillExhaust {
		return 0
	}
	return 1
}

func floatPointer(value float64) *float64 { return &value }

// predictionObservationsFromBuckets uses the newest reading of each bucket as
// the fitted sample. The bucket containing the current reading is represented by
// that reading inside PredictWindow, so the same five-minute slot cannot
// contribute twice when the snapshot and the stored observation differ.
func predictionObservationsFromBuckets(buckets []repository.QuotaObservationBucket, observedAtMS int64) []quota.PredictionObservation {
	observations := make([]quota.PredictionObservation, 0, len(buckets)+1)
	currentBucketStart := (observedAtMS / quota.PredictionBucketMS) * quota.PredictionBucketMS
	for index, bucket := range buckets {
		if index == len(buckets)-1 && bucket.StartMS == currentBucketStart {
			continue
		}
		observations = append(observations, quota.PredictionObservation{
			ObservedAtMS: bucket.LastObservedAtMS,
			UsedPercent:  bucket.LastUsedPercent,
			ResetAtMS:    bucket.LastResetAtMS,
			Source:       bucket.LastSource,
		})
	}
	return observations
}

// predictionBucketsHaveReset combines the ordered within-bucket evidence with
// adjacent-bucket minima. The bucket flag preserves a drop that recovered before
// the bucket's last reading; the cross-bucket comparison catches a drop between
// the previous bucket's end and this bucket's low point.
func predictionBucketsHaveReset(buckets []repository.QuotaObservationBucket) bool {
	for index := 1; index < len(buckets); index++ {
		previous, current := buckets[index-1], buckets[index]
		if previous.LastUsedPercent > current.MinUsedPercent+quotaPredictionResetTolerancePercent {
			return true
		}
		if previous.LastResetAtMS != nil && current.MinResetAtMS != nil &&
			float64(*previous.LastResetAtMS)-float64(*current.MinResetAtMS) > float64(quotaPredictionResetToleranceMS) {
			return true
		}
	}
	for _, bucket := range buckets {
		if bucket.ResetDetected {
			return true
		}
		if bucket.MinResetAtMS != nil && bucket.MaxResetAtMS != nil &&
			float64(*bucket.MaxResetAtMS)-float64(*bucket.MinResetAtMS) > float64(quotaPredictionResetToleranceMS) {
			return true
		}
	}
	return false
}

// predictionCurrentReadingHasReset checks the reading that replaces the current
// bucket. That bucket is omitted from the fitted sample, so a drop from its last
// stored reading to the current snapshot would otherwise be invisible to both
// the ordered bucket flag and the adjacent-bucket comparison. The bucket's last
// reading is sufficient because an earlier higher reading necessarily produced a
// within-bucket drop and already set ResetDetected.
func predictionCurrentReadingHasReset(buckets []repository.QuotaObservationBucket, window quota.QuotaWindow, observedAtMS int64) bool {
	if window.UsedPercent == nil || len(buckets) == 0 {
		return false
	}
	currentBucketStart := (observedAtMS / quota.PredictionBucketMS) * quota.PredictionBucketMS
	last := buckets[len(buckets)-1]
	if last.StartMS != currentBucketStart {
		return false
	}
	if last.LastUsedPercent > *window.UsedPercent+quotaPredictionResetTolerancePercent {
		return true
	}
	if last.LastResetAtMS != nil && window.ResetAtMS != nil &&
		float64(*last.LastResetAtMS)-float64(*window.ResetAtMS) > float64(quotaPredictionResetToleranceMS) {
		return true
	}
	return false
}

func (s *Service) predictQuotaWindow(ctx context.Context, reading Quota, window quota.QuotaWindow, reportedKind, storedKind string, nowMS int64) QuotaPredictionItem {
	observedAtMS := reading.ObservedAtMS
	if window.ObservedAtMS != nil {
		observedAtMS = *window.ObservedAtMS
	}
	predictionInput := quota.PredictionInput{Window: window, ObservedAtMS: observedAtMS, Status: reading.Status, NowMS: nowMS}
	fromMS, toMS, boundaryReason := quota.WindowCycleRange(window, observedAtMS, nowMS)
	if boundaryReason == "" {
		buckets, hasMore, err := s.Repo.ListQuotaObservationBuckets(ctx, repository.QuotaObservationBucketFilter{
			AuthIndex: reading.AuthIndex, Provider: reading.Provider, WindowKind: storedKind,
			Scope: window.Scope, Model: window.Model, FromMS: fromMS, ToMS: toMS,
		}, quotaPredictionBucketLimit)
		if err == nil && !hasMore {
			predictionInput.Observations = predictionObservationsFromBuckets(buckets, observedAtMS)
			if predictionBucketsHaveReset(buckets) || predictionCurrentReadingHasReset(buckets, window, observedAtMS) {
				predictionInput.Window.HasMidCycleReset = true
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
		ResetAccuracy: window.ResetAccuracy, WindowID: window.ID, WindowScope: window.Scope, WindowModel: window.Model,
		UsedPercent: predicted.UsedPercent, ResetAtMS: predicted.ResetAtMS, ObservedAtMS: predicted.ObservedAtMS,
		DataAgeSeconds: predicted.DataAgeSeconds, BurnRatePerHour: predicted.BurnRatePerHour,
		ProjectedPercentAtReset: predicted.ProjectedPercentAtReset, ProjectedTokensAtReset: predicted.ProjectedTokensAtReset,
		ProjectedCostNanosAtReset: predicted.ProjectedCostNanosAtReset, ExhaustionETAMS: predicted.ExhaustionETAMS,
		Verdict: predicted.Verdict, Evidence: predicted.Evidence, Reasons: predicted.Reasons,
		SampleCount: predicted.SampleCount, CoverageSeconds: predicted.CoverageSeconds, MaxGapSeconds: predicted.MaxGapSeconds,
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

func boundQuotaPredictionFindings(result *QuotaPredictionResult) error {
	if len(result.Items) > quotaPredictionFindingsLimit {
		result.Omitted += len(result.Items) - quotaPredictionFindingsLimit
		result.Items = result.Items[:quotaPredictionFindingsLimit]
	}
	return boundQuotaPrediction(result)
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
