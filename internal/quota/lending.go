package quota

import (
	"math"
	"sort"
	"strings"
)

// Cross-credential lending advice.
//
// The advice is a pure projection of cards the caller has already read. It groups
// the credentials that share a provider and a plan, compares their long standard
// windows, and states which of them should carry more traffic before its natural
// reset. It never reads a repository, calls an upstream, or writes a route: the
// console only displays the result, and an operator decides what to do with it.

// LendingReason* are the wire reason codes. They are named once here so the console
// and the projection cannot drift on spelling.
const (
	LendingReasonPlanTypeUnknown    = "plan_type_unknown"
	LendingReasonPoolTooSmall       = "pool_too_small"
	LendingReasonNoLongWindow       = "no_long_window"
	LendingReasonWindowTypeMismatch = "window_type_mismatch"
	LendingReasonResetTimeUnknown   = "reset_time_unknown"
	LendingReasonReadingStale       = "reading_stale"
	LendingReasonReadingFailed      = "reading_failed"
	LendingReasonRemainingUnknown   = "remaining_unknown"
	LendingReasonRemainingBelowTen  = "remaining_below_ten"
	LendingReasonWindowExhausted    = "window_exhausted"
	LendingReasonCooldownActive     = "cooldown_active"
	LendingReasonNeedsReauth        = "needs_reauth"
	LendingReasonCredentialDisabled = "credential_disabled"
	// LendingReasonNotServiceable is the contract's catch-all. Every signal the
	// console can observe today maps to a specific code above, so it is reserved
	// for a future one rather than emitted speculatively.
	LendingReasonNotServiceable = "not_serviceable"
)

// Pool states. "Unavailable" means the pool exists but its readings cannot be
// compared; "limited" means they compare but too few cards can carry traffic.
const (
	LendingStateAdvised     = "advised"
	LendingStateBalanced    = "balanced"
	LendingStateLimited     = "limited"
	LendingStateUnavailable = "unavailable"
)

// Card roles. A card only ever holds one of these.
const (
	LendingRolePrefer      = "prefer"
	LendingRoleAvoid       = "avoid"
	LendingRoleNormal      = "normal"
	LendingRoleBalanced    = "balanced"
	LendingRoleLimited     = "limited"
	LendingRoleUnavailable = "unavailable"
)

const (
	// A window resetting within the hour is compared as if it reset in an hour, so a
	// card about to refill cannot produce a near-infinite daily allowance.
	lendingMinimumDays = 1.0 / 24.0
	// A difference below one percentage point per day is not worth telling an
	// operator to change which credential carries traffic.
	lendingBalanceThreshold = 1.0
	// Below ten percent the card is kept for emergencies rather than ranked.
	lendingLowRemainingPercent = 10.0
	lendingDayMilliseconds     = 86_400_000
)

// LendingCardInput is one credential as the caller has already normalized it: the
// plan, status, cooldown and windows have been resolved, and only the comparison is
// left. IsCooldown and NeedsReauth are separate from Status because a credential
// can be in either state while its stored reading still looks healthy.
type LendingCardInput struct {
	AuthIndex     string
	Name          string
	Provider      string
	PlanType      string
	PlanLabel     string
	IsDisabled    bool
	IsCooldown    bool
	NeedsReauth   bool
	ReadingFailed bool
	Status        string
	ObservedAtMS  int64
	Windows       []QuotaWindow
}

// LendingCard is one credential's row inside a pool.
type LendingCard struct {
	AuthIndex             string
	Name                  string
	Role                  string
	WindowID              string
	WindowKind            string
	RemainingPercent      *float64
	ResetAtMS             *int64
	ResetAccuracy         string
	ObservedAtMS          *int64
	FreshUntilMS          *int64
	DaysToReset           *float64
	DailyAvailablePercent *float64
	ReasonCodes           []string
	IsComparable          bool
}

// LendingPool is the advice for one provider and plan.
type LendingPool struct {
	ID           string
	Provider     string
	PlanType     string
	PlanLabel    string
	WindowKind   string
	State        string
	ValidUntilMS *int64
	ObservedAtMS *int64
	FreshUntilMS *int64
	IsReference  bool
	ReasonCodes  []string
	Cards        []LendingCard
}

// LendingUnpooled names a credential that takes part in no pool and why.
type LendingUnpooled struct {
	AuthIndex   string
	Name        string
	ReasonCodes []string
}

// LendingResult is the whole projection: every pool plus every credential left out.
type LendingResult struct {
	GeneratedAtMS int64
	Pools         []LendingPool
	Unpooled      []LendingUnpooled
	UnpooledCount int
}

// LendingDetail is the projection narrowed to one credential's pool.
type LendingDetail struct {
	GeneratedAtMS       int64
	Pool                *LendingPool
	UnpooledReasonCodes []string
}

// ProjectLendingAdvice groups the cards by provider and plan and projects the
// advice. It is pure: the same cards and clock always produce the same result.
func ProjectLendingAdvice(cards []LendingCardInput, nowMS int64) LendingResult {
	result := LendingResult{
		GeneratedAtMS: nowMS,
		Pools:         []LendingPool{},
		Unpooled:      []LendingUnpooled{},
	}

	groups := make(map[string][]LendingCardInput)
	groupOrder := make([]string, 0, len(cards))
	for _, card := range cards {
		if card.IsDisabled {
			result.Unpooled = append(result.Unpooled, LendingUnpooled{
				AuthIndex:   card.AuthIndex,
				Name:        card.Name,
				ReasonCodes: []string{LendingReasonCredentialDisabled},
			})
			continue
		}
		provider := normalizeLendingKey(card.Provider)
		planType := normalizeLendingKey(card.PlanType)
		if provider == "" || planType == "" || planType == "unknown" {
			result.Unpooled = append(result.Unpooled, LendingUnpooled{
				AuthIndex:   card.AuthIndex,
				Name:        card.Name,
				ReasonCodes: []string{LendingReasonPlanTypeUnknown},
			})
			continue
		}
		key := provider + ":" + planType
		if _, seen := groups[key]; !seen {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], card)
	}
	sort.Strings(groupOrder)

	for _, key := range groupOrder {
		members := groups[key]
		sort.SliceStable(members, func(first, second int) bool {
			if members[first].Name != members[second].Name {
				return members[first].Name < members[second].Name
			}
			return members[first].AuthIndex < members[second].AuthIndex
		})
		analyses := make([]lendingCardAnalysis, 0, len(members))
		withWindow := 0
		for _, member := range members {
			analysis := analyzeLendingCard(member, nowMS)
			if analysis.window != nil {
				withWindow++
			}
			analyses = append(analyses, analysis)
		}
		// A pool needs two credentials that published a long window at all. One card
		// alone is not advice; it is a normal single-seat plan.
		if withWindow < 2 {
			for _, analysis := range analyses {
				reasons := []string{LendingReasonPoolTooSmall}
				reasons = append(reasons, analysis.reasons...)
				result.Unpooled = append(result.Unpooled, LendingUnpooled{
					AuthIndex:   analysis.input.AuthIndex,
					Name:        analysis.input.Name,
					ReasonCodes: reasons,
				})
			}
			continue
		}
		result.Pools = append(result.Pools, buildLendingPool(key, analyses, nowMS))
	}

	sort.SliceStable(result.Unpooled, func(first, second int) bool {
		if result.Unpooled[first].Name != result.Unpooled[second].Name {
			return result.Unpooled[first].Name < result.Unpooled[second].Name
		}
		return result.Unpooled[first].AuthIndex < result.Unpooled[second].AuthIndex
	})
	sort.SliceStable(result.Pools, func(first, second int) bool {
		left, right := result.Pools[first].ValidUntilMS, result.Pools[second].ValidUntilMS
		if (left == nil) != (right == nil) {
			return left != nil
		}
		if left != nil && *left != *right {
			return *left < *right
		}
		return result.Pools[first].ID < result.Pools[second].ID
	})
	result.UnpooledCount = len(result.Unpooled)
	return result
}

// PoolFor returns the pool a credential belongs to, or the reasons it belongs to
// none. The reasons are a copy so a caller cannot mutate the projection.
func (result LendingResult) PoolFor(authIndex string) (*LendingPool, []string) {
	for index := range result.Pools {
		for _, card := range result.Pools[index].Cards {
			if card.AuthIndex == authIndex {
				return &result.Pools[index], nil
			}
		}
	}
	for _, entry := range result.Unpooled {
		if entry.AuthIndex == authIndex {
			return nil, append([]string{}, entry.ReasonCodes...)
		}
	}
	return nil, []string{}
}

// DetailFor narrows the result to one credential for the detail read.
func (result LendingResult) DetailFor(authIndex string) LendingDetail {
	pool, reasons := result.PoolFor(authIndex)
	if reasons == nil {
		reasons = []string{}
	}
	return LendingDetail{GeneratedAtMS: result.GeneratedAtMS, Pool: pool, UnpooledReasonCodes: reasons}
}

// LongStandardWindow reports whether a window is the credential's own standard
// window over at least a day. Five-hour windows recover too quickly to rank, and a
// scoped window belongs to a model or a group rather than the credential.
func LongStandardWindow(window QuotaWindow) bool {
	if window.Scope != "standard" {
		return false
	}
	if window.Kind == "rolling" || window.Kind == "credit_usage" {
		return false
	}
	return window.PeriodHours != nil && *window.PeriodHours >= 24
}

// DailyAvailablePercent is the remaining share divided by the days until the
// natural reset. A reset inside the hour is treated as one hour out. The rate is
// kept to one decimal; the day count is returned unrounded so a caller can format
// sub-day distances without losing them.
func DailyAvailablePercent(remaining float64, resetAtMS, nowMS int64) (float64, float64) {
	days := float64(resetAtMS-nowMS) / lendingDayMilliseconds
	if !(days >= lendingMinimumDays) {
		days = lendingMinimumDays
	}
	daily := remaining / days
	return days, roundLending(daily)
}

func roundLending(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	return math.Round(value*10) / 10
}

type lendingCardAnalysis struct {
	input      LendingCardInput
	window     *QuotaWindow
	comparable bool
	reasons    []string
	days       float64
	daily      float64
	observedAt int64
	freshUntil int64
}

func analyzeLendingCard(card LendingCardInput, nowMS int64) lendingCardAnalysis {
	analysis := lendingCardAnalysis{input: card}
	window := lendingSuggestionWindow(card.Windows)
	if window == nil {
		analysis.reasons = []string{LendingReasonNoLongWindow}
		analysis.observedAt = card.ObservedAtMS
		return analysis
	}
	analysis.window = window
	observedAt := card.ObservedAtMS
	if window.ObservedAtMS != nil && *window.ObservedAtMS > observedAt {
		observedAt = *window.ObservedAtMS
	}
	analysis.observedAt = observedAt
	freshWindow := PredictionFreshWindow(*window)
	if observedAt > 0 {
		analysis.freshUntil = observedAt + freshWindow.Milliseconds()
	}
	if window.RemainingPercent == nil {
		analysis.reasons = append(analysis.reasons, LendingReasonRemainingUnknown)
	}
	if window.ResetAtMS == nil {
		analysis.reasons = append(analysis.reasons, LendingReasonResetTimeUnknown)
	}
	if observedAt <= 0 || nowMS-observedAt > freshWindow.Milliseconds() || window.Stale {
		analysis.reasons = append(analysis.reasons, LendingReasonReadingStale)
	}
	analysis.comparable = len(analysis.reasons) == 0
	if analysis.comparable {
		analysis.days, analysis.daily = DailyAvailablePercent(*window.RemainingPercent, *window.ResetAtMS, nowMS)
	}
	return analysis
}

// lendingSuggestionWindow picks the long window that will bottleneck this credential
// first: the lowest remaining share. A tie goes to the earlier reset, then the kind
// and id so the choice is stable.
func lendingSuggestionWindow(windows []QuotaWindow) *QuotaWindow {
	var chosen *QuotaWindow
	for index := range windows {
		candidate := &windows[index]
		if !LongStandardWindow(*candidate) {
			continue
		}
		if chosen == nil || lendingWindowBefore(*candidate, *chosen) {
			chosen = candidate
		}
	}
	return chosen
}

func lendingWindowBefore(candidate, current QuotaWindow) bool {
	if (candidate.RemainingPercent == nil) != (current.RemainingPercent == nil) {
		return candidate.RemainingPercent != nil
	}
	if candidate.RemainingPercent != nil && *candidate.RemainingPercent != *current.RemainingPercent {
		return *candidate.RemainingPercent < *current.RemainingPercent
	}
	if (candidate.ResetAtMS == nil) != (current.ResetAtMS == nil) {
		return candidate.ResetAtMS != nil
	}
	if candidate.ResetAtMS != nil && *candidate.ResetAtMS != *current.ResetAtMS {
		return *candidate.ResetAtMS < *current.ResetAtMS
	}
	if candidate.Kind != current.Kind {
		return candidate.Kind < current.Kind
	}
	return candidate.ID < current.ID
}

func buildLendingPool(key string, analyses []lendingCardAnalysis, nowMS int64) LendingPool {
	pool := LendingPool{
		ID:       key,
		Provider: normalizeLendingKey(analyses[0].input.Provider),
		PlanType: normalizeLendingKey(analyses[0].input.PlanType),
		// The contract takes the label from the first card by name, so a pool with a
		// single labelled seat does not borrow another seat's label.
		PlanLabel:   analyses[0].input.PlanLabel,
		ReasonCodes: []string{},
	}

	allComparable := true
	kinds := make(map[string]bool)
	poolReasons := make(map[string]bool)
	for _, analysis := range analyses {
		if analysis.window != nil && (analysis.window.ResetAccuracy == "derived" || analysis.window.ResetAccuracy == "approximate") {
			pool.IsReference = true
		}
		if !analysis.comparable {
			allComparable = false
			for _, reason := range analysis.reasons {
				poolReasons[reason] = true
			}
			continue
		}
		kinds[analysis.window.Kind] = true
	}

	// A window-type mismatch is a property of the comparable cards, independent of any
	// other card that could not be read. Both conditions make the pool unavailable, but
	// the mismatch must still be named on the pool and on every card.
	windowTypeMismatch := len(kinds) > 1
	if windowTypeMismatch {
		poolReasons[LendingReasonWindowTypeMismatch] = true
	}

	state := LendingStateUnavailable
	switch {
	case !allComparable:
		state = LendingStateUnavailable
	case windowTypeMismatch:
		// Comparing a weekly window against a daily one would rank two different
		// clocks, so the pool declines to advise rather than pick a winner.
		state = LendingStateUnavailable
	default:
		for kind := range kinds {
			pool.WindowKind = kind
		}
		// Provisional: the ranking below decides advised, balanced or limited.
		state = LendingStateAdvised
	}

	serviceReasons := make([][]string, len(analyses))
	serviceable := make([]int, 0, len(analyses))
	for index, analysis := range analyses {
		serviceReasons[index] = lendingServiceReasons(analysis, nowMS)
		if analysis.comparable && state != LendingStateUnavailable && len(serviceReasons[index]) == 0 {
			serviceable = append(serviceable, index)
		}
	}

	roles := make([]string, len(analyses))
	switch state {
	case LendingStateUnavailable:
		for index := range roles {
			roles[index] = LendingRoleUnavailable
		}
	default:
		switch {
		case len(serviceable) < 2:
			state = LendingStateLimited
			for index := range roles {
				// A card is limited only when it has a serviceability reason. A
				// comparable card that merely lacks a second seat to rank against
				// stays neutral, so the pool can say it cannot advise without
				// labelling a healthy seat as unable to serve.
				if len(serviceReasons[index]) > 0 {
					roles[index] = LendingRoleLimited
				} else {
					roles[index] = LendingRoleNormal
				}
			}
		default:
			highest := analyses[serviceable[0]].daily
			lowest := analyses[serviceable[0]].daily
			for _, index := range serviceable {
				highest = math.Max(highest, analyses[index].daily)
				lowest = math.Min(lowest, analyses[index].daily)
			}
			for index := range roles {
				roles[index] = LendingRoleLimited
			}
			if highest-lowest < lendingBalanceThreshold {
				state = LendingStateBalanced
				for _, index := range serviceable {
					roles[index] = LendingRoleBalanced
				}
				break
			}
			state = LendingStateAdvised
			ranked := append([]int{}, serviceable...)
			sort.SliceStable(ranked, func(first, second int) bool {
				left, right := analyses[ranked[first]], analyses[ranked[second]]
				if left.daily != right.daily {
					return left.daily > right.daily
				}
				if left.days != right.days {
					return left.days < right.days
				}
				if left.input.Name != right.input.Name {
					return left.input.Name < right.input.Name
				}
				return left.input.AuthIndex < right.input.AuthIndex
			})
			for position, index := range ranked {
				roles[index] = LendingRoleNormal
				switch position {
				case 0:
					roles[index] = LendingRolePrefer
				case len(ranked) - 1:
					roles[index] = LendingRoleAvoid
				}
			}
		}
	}
	pool.State = state

	var validUntil, observedAt, freshUntil *int64
	for _, analysis := range analyses {
		if analysis.comparable && analysis.window != nil && analysis.window.ResetAtMS != nil {
			validUntil = earlierInstant(validUntil, *analysis.window.ResetAtMS)
		}
		if analysis.observedAt > 0 {
			observedAt = earlierInstant(observedAt, analysis.observedAt)
		}
		if analysis.freshUntil > 0 {
			freshUntil = earlierInstant(freshUntil, analysis.freshUntil)
		}
	}
	// An unavailable pool has no advice window: taking the earliest comparable
	// reset would read as a promise the projection cannot keep. The observed and
	// freshness bounds describe the reads themselves, so they stay.
	if state == LendingStateUnavailable {
		validUntil = nil
	}
	pool.ValidUntilMS = validUntil
	pool.ObservedAtMS = observedAt
	pool.FreshUntilMS = freshUntil
	for reason := range poolReasons {
		pool.ReasonCodes = append(pool.ReasonCodes, reason)
	}
	sort.Strings(pool.ReasonCodes)

	pool.Cards = make([]LendingCard, 0, len(analyses))
	for index, analysis := range analyses {
		card := lendingCardFromAnalysis(analysis, roles[index], serviceReasons[index])
		if windowTypeMismatch {
			card.ReasonCodes = append(card.ReasonCodes, LendingReasonWindowTypeMismatch)
		}
		sort.Strings(card.ReasonCodes)
		pool.Cards = append(pool.Cards, card)
	}
	sort.SliceStable(pool.Cards, func(first, second int) bool {
		leftRank, rightRank := lendingRoleRank(pool.Cards[first].Role), lendingRoleRank(pool.Cards[second].Role)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if pool.Cards[first].Name != pool.Cards[second].Name {
			return pool.Cards[first].Name < pool.Cards[second].Name
		}
		return pool.Cards[first].AuthIndex < pool.Cards[second].AuthIndex
	})
	return pool
}

// lendingServiceReasons names why a comparable card cannot take traffic right now.
// It is separate from comparability: these cards still contribute their reading to
// the pool, they just do not join the ranking.
func lendingServiceReasons(analysis lendingCardAnalysis, nowMS int64) []string {
	if analysis.window == nil || !analysis.comparable {
		return nil
	}
	reasons := make([]string, 0, 3)
	if analysis.input.IsCooldown {
		reasons = append(reasons, LendingReasonCooldownActive)
	}
	if analysis.input.NeedsReauth {
		reasons = append(reasons, LendingReasonNeedsReauth)
	}
	if analysis.input.ReadingFailed {
		reasons = append(reasons, LendingReasonReadingFailed)
	}
	exhausted := analysis.input.Status == "exhausted" || lendingHasExhaustedStandardWindow(analysis.input, nowMS)
	if exhausted {
		reasons = append(reasons, LendingReasonWindowExhausted)
	} else if analysis.window.RemainingPercent != nil && *analysis.window.RemainingPercent < lendingLowRemainingPercent {
		reasons = append(reasons, LendingReasonRemainingBelowTen)
	}
	return reasons
}

func lendingHasExhaustedStandardWindow(card LendingCardInput, nowMS int64) bool {
	for _, window := range card.Windows {
		if window.Scope != "standard" {
			continue
		}
		if window.RemainingPercent == nil || *window.RemainingPercent > 0 {
			continue
		}
		if window.ResetAtMS != nil && *window.ResetAtMS > nowMS {
			return true
		}
	}
	return false
}

func lendingCardFromAnalysis(analysis lendingCardAnalysis, role string, serviceReasons []string) LendingCard {
	card := LendingCard{
		AuthIndex:    analysis.input.AuthIndex,
		Name:         analysis.input.Name,
		Role:         role,
		IsComparable: analysis.comparable,
		ReasonCodes:  []string{},
	}
	card.ReasonCodes = append(card.ReasonCodes, analysis.reasons...)
	card.ReasonCodes = append(card.ReasonCodes, serviceReasons...)
	if analysis.window != nil {
		card.WindowID = analysis.window.ID
		card.WindowKind = analysis.window.Kind
		card.ResetAccuracy = analysis.window.ResetAccuracy
		if analysis.window.RemainingPercent != nil {
			card.RemainingPercent = lendingFloat(*analysis.window.RemainingPercent)
		}
		if analysis.window.ResetAtMS != nil {
			card.ResetAtMS = lendingInt64(*analysis.window.ResetAtMS)
		}
	}
	if analysis.observedAt > 0 {
		card.ObservedAtMS = lendingInt64(analysis.observedAt)
	}
	if analysis.freshUntil > 0 {
		card.FreshUntilMS = lendingInt64(analysis.freshUntil)
	}
	if analysis.comparable {
		card.DaysToReset = lendingFloat(analysis.days)
		card.DailyAvailablePercent = lendingFloat(analysis.daily)
	}
	return card
}

func lendingRoleRank(role string) int {
	switch role {
	case LendingRolePrefer:
		return 0
	case LendingRoleNormal:
		return 1
	case LendingRoleAvoid:
		return 2
	case LendingRoleBalanced:
		return 3
	case LendingRoleLimited:
		return 4
	default:
		return 5
	}
}

func earlierInstant(current *int64, candidate int64) *int64 {
	if current == nil || candidate < *current {
		return lendingInt64(candidate)
	}
	return current
}

func lendingFloat(value float64) *float64 {
	copied := value
	return &copied
}

func lendingInt64(value int64) *int64 {
	copied := value
	return &copied
}

func normalizeLendingKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
