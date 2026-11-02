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

// Model groups are the operator's ordered answer to "which models are for this
// kind of work". The console writes them; these two capabilities are the only
// reader an external agent has, and they are deliberately read-only: a group is
// a human judgement, not something a caller should be able to reshape mid-run.
//
// No second availability rule lives here: each member takes the verdict
// ProjectModelAdvice produces from the same directory, credentials and quota
// readings, so the same model cannot be `available` in a group and `avoid` in
// model advice. Only the ordering differs: advice sorts by burn
// rate, a group sorts by availability band and then by the operator's order.

const (
	// modelGroupsGroupLimit and modelGroupsMemberLimit bound one query. A group
	// is a hand-curated list, so a hundred of either is already far past a
	// useful one, and the capability's result budget is shared by every group
	// query.
	modelGroupsGroupLimit  = 100
	modelGroupsMemberLimit = 100
	modelGroupsByteLimit   = 32 * 1024

	// modelGroupsUsageDays is the averaging window the PRD fixed for average
	// usage: seven days smooths a single burst and spans both a daily and a
	// weekly quota cycle.
	modelGroupsUsageDays int64 = 7 * 24 * 60 * 60 * 1000
	// modelGroupsUsageMinimumCoverage keeps a handful of readings in one
	// afternoon from reading as a daily rate.
	modelGroupsUsageMinimumCoverage int64 = 24 * 60 * 60 * 1000
)

const (
	// ModelGroupAvailabilityAvailable is a model model_advice would call ok.
	ModelGroupAvailabilityAvailable = "available"
	// ModelGroupAvailabilityTight is usable but nearly spent (advice: caution).
	ModelGroupAvailabilityTight = "tight"
	// ModelGroupAvailabilityUnknown is no usable evidence (advice: unknown).
	ModelGroupAvailabilityUnknown = "unknown"
	// ModelGroupAvailabilityUnavailable is spent or blocked for every account
	// that serves it (advice: avoid). It is still returned, last.
	ModelGroupAvailabilityUnavailable = "unavailable"
)

const (
	modelGroupNotFoundReason = "model_group_not_found"
	modelGroupEmptyReason    = "model_group_empty"
)

// modelGroupAvailabilityRanking orders the bands the PRD fixed: usable, nearly
// spent, unknown, unusable. "unknown" sits above "unavailable" because an
// unreadable quota may still be usable, while an exhausted one certainly is not.
var modelGroupAvailabilityRanking = map[string]int{
	ModelGroupAvailabilityAvailable:   0,
	ModelGroupAvailabilityTight:       1,
	ModelGroupAvailabilityUnknown:     2,
	ModelGroupAvailabilityUnavailable: 3,
}

type ModelGroupsListInput struct{}

// ModelGroupSummary is the whole of what listing groups reveals: a name and a
// size. Member names and quota detail belong to the per-group read.
type ModelGroupSummary struct {
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
}

type ModelGroupsListResult struct {
	Groups  []ModelGroupSummary `json:"groups"`
	Omitted int                 `json:"omitted"`
}

type ModelGroupsModelsInput struct {
	Group string `json:"group" jsonschema:"Model group name exactly as the operator named it; after a rename the old name is unknown"`
}

// ModelGroupModelItem is one member as a caller needs it: the client-requested
// name, the availability band, the seven-day daily usage, and where it sat in
// the operator's order. No account, credential or provider identity is carried.
type ModelGroupModelItem struct {
	Model               string   `json:"model"`
	Availability        string   `json:"availability"`
	AverageUsagePercent *float64 `json:"average_usage_percent"`
	Position            int      `json:"position"`
	IsPreferred         bool     `json:"is_preferred"`
}

type ModelGroupsModelsResult struct {
	Group   string                `json:"group"`
	IsFound bool                  `json:"is_found"`
	Reason  string                `json:"reason,omitempty"`
	Models  []ModelGroupModelItem `json:"models"`
	Omitted int                   `json:"omitted"`
}

func (s *Service) registerModelGroups(registry *capability.Registry) error {
	if err := read(registry, "model_groups_list", "List the operator's model groups with each group's name and member count. Read-only; group names are what the per-group read accepts. Member names and quota detail come from model_groups_models.", func(ctx context.Context, _ ModelGroupsListInput) (ModelGroupsListResult, error) {
		if s.Repo == nil {
			return ModelGroupsListResult{}, errors.New("capability_unavailable")
		}
		groups, err := s.Repo.ListModelGroups(ctx)
		if err != nil {
			return ModelGroupsListResult{}, err
		}
		result := ModelGroupsListResult{Groups: []ModelGroupSummary{}}
		for _, group := range groups {
			if len(result.Groups) == modelGroupsGroupLimit {
				result.Omitted++
				continue
			}
			result.Groups = append(result.Groups, ModelGroupSummary{Name: group.Name, MemberCount: len(group.Members)})
		}
		return result, nil
	}); err != nil {
		return err
	}
	return read(registry, "model_groups_models", "Read one operator-defined model group: every member in availability order (available, tight, unknown, unavailable) and then in the operator's order, each with its availability, seven-day average daily quota usage and group position. Availability uses model_advice's verdicts. Unusable models are returned last, never filtered out. Read-only; use the name from model_groups_list.", s.modelGroupModels)
}

func (s *Service) modelGroupModels(ctx context.Context, input ModelGroupsModelsInput) (ModelGroupsModelsResult, error) {
	if s.Repo == nil {
		return ModelGroupsModelsResult{}, errors.New("capability_unavailable")
	}
	group, err := s.Repo.GetModelGroupByName(ctx, input.Group)
	if errors.Is(err, repository.ErrModelGroupNotFound) {
		return ModelGroupsModelsResult{IsFound: false, Reason: modelGroupNotFoundReason, Models: []ModelGroupModelItem{}}, nil
	}
	if err != nil {
		return ModelGroupsModelsResult{}, err
	}
	result := ModelGroupsModelsResult{Group: group.Name, IsFound: true, Models: []ModelGroupModelItem{}}
	if len(group.Members) == 0 {
		result.Reason = modelGroupEmptyReason
		return result, nil
	}

	members := group.Members
	if len(members) > modelGroupsMemberLimit {
		result.Omitted = len(members) - modelGroupsMemberLimit
		members = members[:modelGroupsMemberLimit]
	}
	names := make([]string, 0, len(members))
	for _, member := range members {
		names = append(names, member.Model)
	}
	nowMS := capability.AnchorMS(ctx)
	directory, credentials, quotas := s.modelGroupAdviceInputs(ctx)
	advice := ProjectModelAdvice(ModelAdviceInput{Models: names}, directory, quotas, credentials, nowMS, s.AdviceBlocked)
	usage := s.modelGroupAverageUsage(ctx, names, directory, credentials, quotas, nowMS)
	result.Models = projectModelGroupModels(members, advice, usage)
	boundModelGroupsModels(&result)
	return result, nil
}

// modelGroupAdviceInputs reads the same three sources model_advice uses and
// degrades a failure into empty evidence instead of a failed call: the PRD
// requires the member list even when quota data cannot be read at all, with
// every availability then reported as unknown.
func (s *Service) modelGroupAdviceInputs(ctx context.Context) (ModelSquareDirectory, []AdviceCredential, []Quota) {
	if s.AdviceDirectory == nil || s.AdviceCredentials == nil || s.Quotas == nil {
		return ModelSquareDirectory{}, nil, nil
	}
	directory, err := s.AdviceDirectory(ctx)
	if err != nil {
		return ModelSquareDirectory{}, nil, nil
	}
	credentials, err := s.AdviceCredentials(ctx)
	if err != nil {
		return directory, nil, nil
	}
	quotas, err := s.readAdviceQuotas(ctx)
	if err != nil {
		return directory, credentials, nil
	}
	return directory, credentials, quotas
}

// projectModelGroupModels keeps the group's own vocabulary while borrowing the
// verdicts: it maps advice's four outcomes onto the group's four bands, then
// orders by band, operator position and finally the client-requested name so
// two reads of unchanged data return the same list.
func projectModelGroupModels(members []repository.ModelGroupMember, advice ModelAdviceResult, usage map[string]*float64) []ModelGroupModelItem {
	verdicts := make(map[string]string, len(advice.Models))
	for _, item := range advice.Models {
		verdicts[item.Model] = item.Verdict
	}
	items := make([]ModelGroupModelItem, 0, len(members))
	for index, member := range members {
		position := member.Position
		if position <= 0 {
			position = index + 1
		}
		items = append(items, ModelGroupModelItem{
			Model:               member.Model,
			Availability:        modelGroupAvailability(verdicts[member.Model]),
			AverageUsagePercent: usage[member.Model],
			Position:            position,
			IsPreferred:         position == 1,
		})
	}
	sort.SliceStable(items, func(left, right int) bool {
		first, second := items[left], items[right]
		if modelGroupAvailabilityRanking[first.Availability] != modelGroupAvailabilityRanking[second.Availability] {
			return modelGroupAvailabilityRanking[first.Availability] < modelGroupAvailabilityRanking[second.Availability]
		}
		if first.Position != second.Position {
			return first.Position < second.Position
		}
		return first.Model < second.Model
	})
	return items
}

func modelGroupAvailability(verdict string) string {
	switch verdict {
	case "ok":
		return ModelGroupAvailabilityAvailable
	case "caution":
		return ModelGroupAvailabilityTight
	case "avoid":
		return ModelGroupAvailabilityUnavailable
	}
	return ModelGroupAvailabilityUnknown
}

func boundModelGroupsModels(result *ModelGroupsModelsResult) {
	for len(result.Models) > 0 {
		encoded, _ := json.Marshal(*result)
		if len(encoded) <= modelGroupsByteLimit {
			return
		}
		result.Models = result.Models[:len(result.Models)-1]
		result.Omitted++
	}
}

// modelGroupAverageUsage computes each member's seven-day daily usage from the
// stored quota observation history. The PRD's "merge accounts, then average"
// means: sum what every account serving the model consumed per day, and divide
// by the covered days. A member whose accounts have no usable history is left
// nil rather than reported as zero.
func (s *Service) modelGroupAverageUsage(ctx context.Context, models []string, directory ModelSquareDirectory, credentials []AdviceCredential, quotas []Quota, nowMS int64) map[string]*float64 {
	usage := make(map[string]*float64, len(models))
	if s.Repo == nil || len(credentials) == 0 || len(quotas) == 0 {
		return usage
	}
	routes := map[string][]ModelSquareRoute{}
	for _, route := range directory.Routes {
		routes[route.CallPoint] = append(routes[route.CallPoint], route)
	}
	providers := map[string]ModelSquareProvider{}
	for _, provider := range directory.Providers {
		providers[provider.ID] = provider
	}
	quotaByIndex := make(map[string]Quota, len(quotas))
	for _, reading := range quotas {
		quotaByIndex[reading.AuthIndex] = reading
	}
	// A standard window is shared by every model a credential serves, so its
	// history is read once and reused for each member that points at it.
	rates := map[string]float64{}
	fromMS := nowMS - modelGroupsUsageDays
	for _, model := range models {
		upstream, resolved := modelGroupUpstream(routes[model])
		if !resolved {
			continue
		}
		total := 0.0
		found := false
		for _, credential := range credentials {
			if !modelGroupCredentialServes(model, upstream, routes[model], providers, credential) {
				continue
			}
			reading, ok := quotaByIndex[credential.AuthIndex]
			if !ok {
				continue
			}
			window, ok := modelGroupUsageWindow(reading.Windows, upstream)
			if !ok {
				continue
			}
			key := strings.Join([]string{credential.AuthIndex, reading.Provider, modelGroupWindowKind(window), window.Scope, window.Model}, "\x00")
			rate, cached := rates[key]
			if !cached {
				ok := false
				rate, ok = s.modelGroupWindowDailyUsage(ctx, credential.AuthIndex, reading.Provider, window, fromMS, nowMS)
				if !ok {
					continue
				}
				rates[key] = rate
			}
			total += rate
			found = true
		}
		if found {
			value := roundGroupUsage(total)
			usage[model] = &value
		}
	}
	return usage
}

// modelGroupUpstream resolves the one upstream a client-requested name routes
// to. A name with no route or several upstream targets has no defensible
// per-model window; the same ambiguity keeps model_advice unknown.
func modelGroupUpstream(routes []ModelSquareRoute) (string, bool) {
	targets := map[string]bool{}
	for _, route := range routes {
		targets[route.UpstreamModel] = true
	}
	if len(routes) == 0 || targets[""] || len(targets) != 1 {
		return "", false
	}
	return routes[0].UpstreamModel, true
}

// modelGroupCredentialServes mirrors ProjectModelAdvice's credential matching,
// so usage is drawn from the accounts the verdict was drawn from.
func modelGroupCredentialServes(model, upstream string, routes []ModelSquareRoute, providers map[string]ModelSquareProvider, credential AdviceCredential) bool {
	if credential.IsDisabled {
		return false
	}
	hasRoute := false
	for _, route := range routes {
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
		return false
	}
	for _, served := range credential.Models {
		if served == model || served == upstream {
			return true
		}
	}
	return len(credential.Models) == 0
}

// modelGroupUsageWindow picks one window per credential so a five-hour and a
// weekly window of the same account cannot both contribute the same requests.
// A model-scoped window is more precise than the account-wide one; among equals
// the longest period is the best basis for a seven-day rate.
func modelGroupUsageWindow(windows []quota.QuotaWindow, upstream string) (quota.QuotaWindow, bool) {
	var selected quota.QuotaWindow
	found := false
	selectedScoped := false
	selectedPeriod := 0.0
	for _, window := range windows {
		if !quota.IsWindowRelevant(window, upstream) {
			continue
		}
		if window.UsedPercent == nil && window.RemainingPercent == nil {
			continue
		}
		if modelGroupWindowKind(window) == "" {
			continue
		}
		scoped := window.Scope == "model" || window.Scope == "group"
		period := 0.0
		if window.PeriodHours != nil && *window.PeriodHours > 0 {
			period = *window.PeriodHours
		}
		better := !found ||
			scoped && !selectedScoped ||
			scoped == selectedScoped && period > selectedPeriod ||
			scoped == selectedScoped && period == selectedPeriod && window.ID < selected.ID
		if better {
			selected, found, selectedScoped, selectedPeriod = window, true, scoped, period
		}
	}
	return selected, found
}

func modelGroupWindowKind(window quota.QuotaWindow) string {
	return inferAdviceWindowKind(window)
}

// modelGroupWindowDailyUsage is the seven-day daily consumption of one
// account's window, in percentage points per day. A drop or a later reset means
// the cycle restarted, so the reading after it is usage in the new cycle and
// the reading before it is never subtracted from it.
func (s *Service) modelGroupWindowDailyUsage(ctx context.Context, authIndex, provider string, window quota.QuotaWindow, fromMS, toMS int64) (float64, bool) {
	limit := int(modelGroupsUsageDays/quota.PredictionBucketMS) + 2
	buckets, hasMore, err := s.Repo.ListQuotaObservationBuckets(ctx, repository.QuotaObservationBucketFilter{
		AuthIndex: authIndex, Provider: provider, WindowKind: modelGroupWindowKind(window),
		Scope: window.Scope, Model: window.Model, FromMS: fromMS, ToMS: toMS,
	}, limit)
	if err != nil || hasMore {
		return 0, false
	}
	return averageGroupDailyUsage(buckets)
}

func averageGroupDailyUsage(buckets []repository.QuotaObservationBucket) (float64, bool) {
	var total float64
	var firstMS, lastMS int64
	count := 0
	var previous *repository.QuotaObservationBucket
	for index := range buckets {
		bucket := buckets[index]
		if bucket.LastObservedAtMS <= 0 {
			continue
		}
		if count == 0 || bucket.LastObservedAtMS < firstMS {
			firstMS = bucket.LastObservedAtMS
		}
		if bucket.LastObservedAtMS > lastMS {
			lastMS = bucket.LastObservedAtMS
		}
		switch {
		case previous == nil:
			if bucket.LastUsedPercent > bucket.MinUsedPercent {
				total += bucket.LastUsedPercent - bucket.MinUsedPercent
			}
		case groupUsageReset(*previous, bucket):
			if bucket.LastUsedPercent > 0 {
				total += bucket.LastUsedPercent
			}
		case bucket.LastUsedPercent > previous.LastUsedPercent:
			total += bucket.LastUsedPercent - previous.LastUsedPercent
		}
		previous = &buckets[index]
		count++
	}
	if count < 2 {
		return 0, false
	}
	covered := lastMS - firstMS
	if covered < modelGroupsUsageMinimumCoverage {
		return 0, false
	}
	if covered > modelGroupsUsageDays {
		covered = modelGroupsUsageDays
	}
	days := float64(covered) / float64(24*60*60*1000)
	if days <= 0 {
		return 0, false
	}
	return total / days, true
}

func groupUsageReset(previous, current repository.QuotaObservationBucket) bool {
	if current.ResetDetected {
		return true
	}
	if previous.LastUsedPercent > current.MinUsedPercent+quotaPredictionResetTolerancePercent {
		return true
	}
	return previous.LastResetAtMS != nil && current.MinResetAtMS != nil &&
		float64(*previous.LastResetAtMS)-float64(*current.MinResetAtMS) > float64(quotaPredictionResetToleranceMS)
}

func roundGroupUsage(value float64) float64 {
	if value < 0 {
		return 0
	}
	return float64(int64(value*100+0.5)) / 100
}
