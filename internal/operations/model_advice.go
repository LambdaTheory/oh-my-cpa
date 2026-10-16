package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/gateway"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

const (
	modelAdviceInputLimit  = 20
	modelAdviceOutputLimit = 50
	modelAdviceByteLimit   = 32 * 1024
)

type ModelAdviceInput struct {
	Models []string `json:"models,omitempty" jsonschema:"Client-requested model names, at most 20; omit to rank configured models"`
}

type AdviceCredential struct {
	AuthIndex      string
	Provider       string
	ProviderID     string
	IsDisabled     bool
	Models         []string
	ExcludedModels []string
}

type AdviceWindow struct {
	Label       string  `json:"label"`
	Kind        string  `json:"kind"`
	UsedPercent float64 `json:"used_percent"`
	ResetAtMS   *int64  `json:"reset_at_ms"`
}

type ModelAdviceItem struct {
	Model             string        `json:"model"`
	Verdict           string        `json:"verdict"`
	Rank              *int          `json:"rank"`
	Reasons           []string      `json:"reasons"`
	TightestWindow    *AdviceWindow `json:"tightest_window"`
	CredentialsTotal  int           `json:"credentials_total"`
	CredentialsUsable int           `json:"credentials_usable"`
	DataAgeSeconds    *int64        `json:"data_age_seconds"`
}

type ModelAdviceResult struct {
	Recommended   *string           `json:"recommended"`
	Models        []ModelAdviceItem `json:"models"`
	GeneratedAtMS int64             `json:"generated_at_ms"`
	Omitted       int               `json:"omitted"`
}

// ProjectModelAdvice ranks evidence, never credential identities. In particular an
// ambiguous route cannot inherit the quota of a convenient provider.
func ProjectModelAdvice(input ModelAdviceInput, directory ModelSquareDirectory, quotas []Quota, credentials []AdviceCredential, nowMS int64, isBlocked func(string, string) bool) ModelAdviceResult {
	result := ModelAdviceResult{Models: []ModelAdviceItem{}, GeneratedAtMS: nowMS}
	quotaByIndex := make(map[string]Quota, len(quotas))
	for _, reading := range quotas {
		quotaByIndex[reading.AuthIndex] = reading
	}
	routes := map[string][]ModelSquareRoute{}
	configured := map[string]bool{}
	for _, model := range directory.Models {
		configured[model.ID] = true
	}
	for _, route := range directory.Routes {
		routes[route.CallPoint] = append(routes[route.CallPoint], route)
	}
	providers := map[string]ModelSquareProvider{}
	for _, provider := range directory.Providers {
		providers[provider.ID] = provider
	}
	names := input.Models
	if names == nil {
		for model := range configured {
			names = append(names, model)
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		item := ModelAdviceItem{Model: name, Verdict: "unknown", Reasons: []string{}}
		matches := routes[name]
		targets := map[string]bool{}
		for _, route := range matches {
			targets[route.UpstreamModel] = true
		}
		switch {
		case !configured[name]:
			item.Reasons = append(item.Reasons, "model_not_configured")
		case len(matches) == 0 || targets[""] || len(targets) != 1:
			item.Reasons = append(item.Reasons, "route_ambiguous")
		default:
			var blocked, unmeasured, unrelated int
			best := 4
			for _, credential := range credentials {
				if credential.IsDisabled {
					continue
				}
				upstream := matches[0].UpstreamModel
				hasRoute := false
				for _, route := range matches {
					provider := providers[route.ProviderID]
					if route.ProviderID == "" || provider.ID == "" {
						continue
					}
					if credential.ProviderID != "" {
						hasRoute = credential.ProviderID == route.ProviderID
					} else {
						hasRoute = provider.IsOAuth && credential.Provider == provider.Family
					}
					if hasRoute {
						break
					}
				}
				if !hasRoute || isModelExcluded(upstream, credential.ExcludedModels) {
					continue
				}
				hasModel := false
				for _, served := range credential.Models {
					if served == name || served == upstream {
						hasModel = true
						break
					}
				}
				if !hasModel && len(credential.Models) > 0 {
					continue
				}
				item.CredentialsTotal++
				if !hasModel {
					unmeasured++
					continue
				}
				reading, ok := quotaByIndex[credential.AuthIndex]
				if !ok || credential.AuthIndex == "" || reading.Provider != "" && reading.Provider != credential.Provider {
					unmeasured++
					continue
				}
				if reading.ObservedAtMS <= 0 || reading.Status == "idle" || reading.Status == quota.QuotaStatusUnpublished || reading.Status == "stale" || reading.Status == "error" || reading.Recommendation.Status == "needs_reauth" {
					unmeasured++
					continue
				}
				relevant := []quota.QuotaWindow{}
				for _, window := range reading.Windows {
					if quota.IsWindowRelevant(window, upstream) {
						relevant = append(relevant, window)
					} else {
						unrelated++
					}
				}
				if len(relevant) == 0 {
					unmeasured++
					continue
				}
				evaluated := &quota.NormalizedQuota{Status: reading.Status, Windows: relevant}
				quota.EvaluateStatusAndRecommendation(evaluated, nowMS)
				if evaluated.Status != "healthy" && evaluated.Status != "warning" && evaluated.Status != "exhausted" {
					unmeasured++
					continue
				}
				isCooling := reading.Status == "cooldown" || reading.Recommendation.Status == "cooldown"
				if isBlocked != nil && isBlocked(credential.Provider, credential.AuthIndex) {
					isCooling = true
				}
				if isCooling || evaluated.Status == "exhausted" {
					blocked++
					tightest := tightestAdviceWindow(relevant)
					if best == 4 && (item.TightestWindow == nil || tightest != nil && tightest.UsedPercent < item.TightestWindow.UsedPercent) {
						item.TightestWindow = tightest
						age := max(int64(0), (nowMS-reading.ObservedAtMS)/1000)
						item.DataAgeSeconds = &age
					}
					continue
				}
				item.CredentialsUsable++
				priority := 0
				if evaluated.Status == "warning" {
					priority = 1
				}
				tightest := tightestAdviceWindow(relevant)
				if priority < best || priority == best && (item.TightestWindow == nil || tightest != nil && tightest.UsedPercent < item.TightestWindow.UsedPercent) {
					best = priority
					item.TightestWindow = tightest
					age := max(int64(0), (nowMS-reading.ObservedAtMS)/1000)
					item.DataAgeSeconds = &age
				}
			}
			if unrelated > 0 {
				item.Reasons = append(item.Reasons, "unrelated_windows_ignored")
			}
			if best == 0 {
				item.Verdict = "ok"
			} else if best == 1 {
				item.Verdict = "caution"
			} else if blocked > 0 && unmeasured == 0 && blocked == item.CredentialsTotal {
				item.Verdict = "avoid"
			}
			if item.CredentialsTotal == 0 {
				item.Reasons = append(item.Reasons, "no_matching_credentials")
			} else {
				item.Reasons = append(item.Reasons, fmt.Sprintf("%d blocked, %d without valid readings", blocked, unmeasured))
			}
		}
		result.Models = append(result.Models, item)
	}
	sort.Slice(result.Models, func(left, right int) bool {
		first, second := result.Models[left], result.Models[right]
		order := map[string]int{"ok": 0, "caution": 1, "avoid": 2, "unknown": 3}
		if order[first.Verdict] != order[second.Verdict] {
			return order[first.Verdict] < order[second.Verdict]
		}
		firstUsed, secondUsed := math.Inf(1), math.Inf(1)
		if first.TightestWindow != nil {
			firstUsed = first.TightestWindow.UsedPercent
		}
		if second.TightestWindow != nil {
			secondUsed = second.TightestWindow.UsedPercent
		}
		if firstUsed != secondUsed {
			return firstUsed < secondUsed
		}
		return first.Model < second.Model
	})
	rank := 0
	for index := range result.Models {
		if result.Models[index].Verdict != "unknown" {
			rank++
			position := rank
			result.Models[index].Rank = &position
		}
	}
	if rank > 0 {
		recommended := result.Models[0].Model
		if result.Models[0].Verdict == "ok" || result.Models[0].Verdict == "caution" {
			result.Recommended = &recommended
		}
	}
	boundModelAdvice(&result)
	return result
}

func boundModelAdvice(result *ModelAdviceResult) {
	for len(result.Models) > modelAdviceOutputLimit {
		result.Models = result.Models[:len(result.Models)-1]
		result.Omitted++
	}
	for len(result.Models) > 0 {
		encoded, _ := json.Marshal(result)
		if len(encoded) <= modelAdviceByteLimit {
			break
		}
		result.Models = result.Models[:len(result.Models)-1]
		result.Omitted++
	}
	if len(result.Models) == 0 {
		result.Recommended = nil
	}
}

func tightestAdviceWindow(windows []quota.QuotaWindow) *AdviceWindow {
	labels := map[string]string{
		"five_hour": "Five-hour", "weekly": "Weekly", "daily": "Daily",
		"monthly": "Monthly", "model_scoped": "Model-scoped",
		"credit_usage": "Credit usage", "custom": "Custom window",
	}
	var selected *AdviceWindow
	for _, window := range windows {
		if window.RemainingPercent == nil {
			continue
		}
		used := 100 - *window.RemainingPercent
		if window.UsedPercent != nil {
			used = *window.UsedPercent
		}
		if selected == nil || used > selected.UsedPercent {
			kind := window.Kind
			label := labels[kind]
			if label == "" {
				kind, label = "unknown", "Quota window"
			}
			selected = &AdviceWindow{Label: label, Kind: kind, UsedPercent: used, ResetAtMS: window.ResetAtMS}
		}
	}
	return selected
}

func (s *Service) registerModelAdvice(registry *capability.Registry) error {
	return read(registry, "model_advice", "Rank client-requested models using stored quota readings and the model directory. Recommend the first ok/caution model, otherwise null. Advisory only; no quota refresh or credential disclosure.", func(ctx context.Context, input ModelAdviceInput) (ModelAdviceResult, error) {
		if len(input.Models) > modelAdviceInputLimit {
			return ModelAdviceResult{}, invalidParameters{"models accepts at most 20 names"}
		}
		for _, model := range input.Models {
			if strings.TrimSpace(model) == "" {
				return ModelAdviceResult{}, invalidParameters{"models must contain nonempty client-requested names"}
			}
		}
		if s.AdviceDirectory == nil || s.AdviceCredentials == nil || s.Quotas == nil {
			return ModelAdviceResult{}, errors.New("capability_unavailable")
		}
		directory, err := s.AdviceDirectory(ctx)
		nowMS := capability.AnchorMS(ctx)
		if err != nil {
			directory = ModelSquareDirectory{Models: []gateway.Model{}}
			for _, model := range input.Models {
				directory.Models = append(directory.Models, gateway.Model{ID: model})
			}
			result := ProjectModelAdvice(input, directory, nil, nil, nowMS, nil)
			for index := range result.Models {
				result.Models[index].Reasons = []string{"model_directory_unavailable"}
			}
			boundModelAdvice(&result)
			return result, nil
		}
		credentials, err := s.AdviceCredentials(ctx)
		if err != nil {
			return ModelAdviceResult{}, err
		}
		quotas, err := s.readAdviceQuotas(ctx)
		if err != nil {
			return ModelAdviceResult{}, err
		}
		result := ProjectModelAdvice(input, directory, quotas, credentials, nowMS, s.AdviceBlocked)
		return result, nil
	})
}

// Failed refreshes deliberately retain the last successful snapshot. Its newer
// classified outcome must still prevent advice from calling that snapshot fresh.
func (s *Service) readAdviceQuotas(ctx context.Context) ([]Quota, error) {
	readings, err := s.Quotas(ctx)
	if err != nil || s.Repo == nil {
		return readings, err
	}
	readings = append([]Quota(nil), readings...)
	indexes := make([]string, 0, len(readings))
	for _, reading := range readings {
		indexes = append(indexes, reading.AuthIndex)
	}
	signals, err := s.Repo.GetFeishuAccountSignals(ctx, indexes)
	if err != nil {
		return nil, err
	}
	for index := range readings {
		reading := &readings[index]
		signal, exists := signals[reading.AuthIndex]
		if exists && signal.Provider == reading.Provider && signal.ObservedAt.UnixMilli() >= reading.ObservedAtMS && signal.State != repository.FeishuSignalHealthy {
			reading.Status = "stale"
		}
	}
	return readings, nil
}
