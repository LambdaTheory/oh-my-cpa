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
	advice := projectModelGroupAdvice(names, directory, quotas, credentials, nowMS, s.AdviceBlocked)
	usage := s.modelGroupAverageUsage(ctx, names, directory, credentials, quotas, nowMS)
	result.Models = projectModelGroupModels(members, advice, usage)
	boundModelGroupsModels(&result)
	return result, nil
}

// projectModelGroupAdvice asks ProjectModelAdvice for one member at a time.
// That projection bounds its own result to model_advice's 50-row / 32 KiB
// capability budget; a single-member call cannot reach that bound, so a group
// of up to modelGroupsMemberLimit keeps, for every member, the verdict
// model_advice would give that model on its own.
func projectModelGroupAdvice(models []string, directory ModelSquareDirectory, quotas []Quota, credentials []AdviceCredential, nowMS int64, isBlocked func(string, string) bool) ModelAdviceResult {
	result := ModelAdviceResult{Models: []ModelAdviceItem{}, GeneratedAtMS: nowMS}
	for _, model := range models {
		single := ProjectModelAdvice(ModelAdviceInput{Models: []string{model}}, directory, quotas, credentials, nowMS, isBlocked)
		result.Models = append(result.Models, single.Models...)
	}
	return result
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

// modelGroupUsageSeries is one account's window history, read once and shared
// by every member that points at the same window.
type modelGroupUsageSeries struct {
	buckets  []repository.QuotaObservationBucket
	periodMS int64
}

// modelGroupAverageUsage computes each member's seven-day daily usage from the
// stored quota observation history. The PRD's "merge accounts, then average"
// means: sum every serving account's consumption inside the interval all of
// them cover, then divide by that interval's length. A member is nil when any
// serving account lacks usable history - a partial account would present part
// of the model's usage as the whole figure.
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
	seriesByKey := map[string]modelGroupUsageSeries{}
	unreadable := map[string]bool{}
	fromMS := nowMS - modelGroupsUsageDays
	for _, model := range models {
		upstream, resolved := modelGroupUpstream(routes[model])
		if !resolved {
			continue
		}
		modelSeries := []modelGroupUsageSeries{}
		serving, complete := 0, true
		for _, credential := range credentials {
			if !modelGroupCredentialServes(model, upstream, routes[model], providers, credential) {
				continue
			}
			serving++
			reading, ok := quotaByIndex[credential.AuthIndex]
			if !ok {
				complete = false
				break
			}
			window, ok := modelGroupUsageWindow(reading.Windows, upstream)
			if !ok {
				complete = false
				break
			}
			key := strings.Join([]string{credential.AuthIndex, reading.Provider, modelGroupWindowKind(window), window.Scope, window.Model}, "\x00")
			if unreadable[key] {
				complete = false
				break
			}
			series, cached := seriesByKey[key]
			if !cached {
				series, ok = s.modelGroupWindowUsageSeries(ctx, credential.AuthIndex, reading.Provider, window, fromMS, nowMS)
				if !ok {
					unreadable[key] = true
					complete = false
					break
				}
				seriesByKey[key] = series
			}
			modelSeries = append(modelSeries, series)
		}
		if serving == 0 || !complete {
			continue
		}
		if value, ok := combinedGroupDailyUsage(modelSeries); ok {
			rounded := roundGroupUsage(value)
			usage[model] = &rounded
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

// modelGroupWindowUsageSeries reads one account's window history over the
// rolling seven days. Overflow or a read failure leaves the series unusable,
// which makes the member's average null rather than partial.
func (s *Service) modelGroupWindowUsageSeries(ctx context.Context, authIndex, provider string, window quota.QuotaWindow, fromMS, toMS int64) (modelGroupUsageSeries, bool) {
	limit := int(modelGroupsUsageDays/quota.PredictionBucketMS) + 2
	buckets, hasMore, err := s.Repo.ListQuotaObservationBuckets(ctx, repository.QuotaObservationBucketFilter{
		AuthIndex: authIndex, Provider: provider, WindowKind: modelGroupWindowKind(window),
		Scope: window.Scope, Model: window.Model, FromMS: fromMS, ToMS: toMS,
	}, limit)
	if err != nil || hasMore {
		return modelGroupUsageSeries{}, false
	}
	if _, _, ok := groupUsageCoverage(buckets); !ok {
		return modelGroupUsageSeries{}, false
	}
	periodMS := int64(0)
	if window.PeriodHours != nil && *window.PeriodHours > 0 {
		periodMS = int64(*window.PeriodHours * 60 * 60 * 1000)
	}
	return modelGroupUsageSeries{buckets: buckets, periodMS: periodMS}, true
}

// groupUsageCoverage is the span a series actually observed. A single reading
// describes an instant, not an interval, so it is not coverage.
func groupUsageCoverage(buckets []repository.QuotaObservationBucket) (firstMS, lastMS int64, ok bool) {
	count := 0
	for _, bucket := range buckets {
		if bucket.LastObservedAtMS <= 0 {
			continue
		}
		if count == 0 || bucket.LastObservedAtMS < firstMS {
			firstMS = bucket.LastObservedAtMS
		}
		if bucket.LastObservedAtMS > lastMS {
			lastMS = bucket.LastObservedAtMS
		}
		count++
	}
	return firstMS, lastMS, count >= 2
}

// modelGroupUsageShare is one account's consumption inside the common span and
// the span its own readings actually cover there.
type modelGroupUsageShare struct {
	total    float64
	firstMS  int64
	lastMS   int64
	readings int
}

// combinedGroupDailyUsage merges the accounts on one interval and one
// denominator: the span every account covers. Each account's readings inside
// that span must themselves cover at least half of it; otherwise its only
// increment crosses an edge and is discarded, and the result would be a
// confident zero standing for missing history. A genuine steady zero keeps its
// readings through the span and stays measurable.
func combinedGroupDailyUsage(series []modelGroupUsageSeries) (float64, bool) {
	if len(series) == 0 {
		return 0, false
	}
	var startMS, endMS int64
	for index, item := range series {
		firstMS, lastMS, ok := groupUsageCoverage(item.buckets)
		if !ok {
			return 0, false
		}
		if index == 0 || firstMS > startMS {
			startMS = firstMS
		}
		if index == 0 || lastMS < endMS {
			endMS = lastMS
		}
	}
	span := endMS - startMS
	if span < modelGroupsUsageMinimumCoverage {
		return 0, false
	}
	total := 0.0
	for _, item := range series {
		share := groupUsageWithin(item.buckets, item.periodMS, startMS, endMS)
		if share.readings < 2 || (share.lastMS-share.firstMS)*2 < span {
			return 0, false
		}
		total += share.total
	}
	days := float64(span) / float64(24*60*60*1000)
	if days > 7 {
		days = 7
	}
	if days <= 0 {
		return 0, false
	}
	return total / days, true
}

// groupUsageWithin adds the consumption an account recorded between two
// readings that are both inside [startMS, endMS]. The first reading inside the
// span has no in-span predecessor, so the increment that crosses the boundary
// is skipped rather than attributed partly to the common window. It also
// reports the span the in-window readings cover, which is what tells a real
// zero apart from too little history.
func groupUsageWithin(buckets []repository.QuotaObservationBucket, periodMS, startMS, endMS int64) modelGroupUsageShare {
	var share modelGroupUsageShare
	var previous *repository.QuotaObservationBucket
	for index := range buckets {
		bucket := &buckets[index]
		if bucket.LastObservedAtMS <= 0 {
			continue
		}
		if bucket.LastObservedAtMS > endMS {
			break
		}
		if bucket.LastObservedAtMS < startMS {
			previous = bucket
			continue
		}
		if share.readings == 0 || bucket.LastObservedAtMS < share.firstMS {
			share.firstMS = bucket.LastObservedAtMS
		}
		if bucket.LastObservedAtMS > share.lastMS {
			share.lastMS = bucket.LastObservedAtMS
		}
		share.readings++
		if previous != nil && previous.LastObservedAtMS >= startMS {
			switch {
			case groupUsageReset(*previous, *bucket, periodMS):
				if bucket.LastUsedPercent > 0 {
					share.total += bucket.LastUsedPercent
				}
			case bucket.LastUsedPercent > previous.LastUsedPercent:
				share.total += bucket.LastUsedPercent - previous.LastUsedPercent
			}
		}
		previous = bucket
	}
	return share
}

// groupUsageReset reports whether current starts a new cycle relative to
// previous. A percentage drop is direct evidence. So is a reset instant that
// jumps forward by more than half the window period: a cycle can roll over
// while its used share is already higher than the previous reading (40% before
// a weekly boundary, 60% in the new cycle). A rolling window moves its boundary
// every few minutes, so a forward jump only counts at a period-sized scale;
// without a period it cannot be told apart from rolling, and only a drop counts.
func groupUsageReset(previous, current repository.QuotaObservationBucket, periodMS int64) bool {
	if current.ResetDetected {
		return true
	}
	if previous.LastUsedPercent > current.MinUsedPercent+quotaPredictionResetTolerancePercent {
		return true
	}
	if previous.LastResetAtMS == nil || current.MinResetAtMS == nil {
		return false
	}
	advance := *current.MinResetAtMS - *previous.LastResetAtMS
	if periodMS > 0 && advance > periodMS/2 {
		return true
	}
	return advance < -int64(quotaPredictionResetToleranceMS)
}

func roundGroupUsage(value float64) float64 {
	if value < 0 {
		return 0
	}
	return float64(int64(value*100+0.5)) / 100
}
