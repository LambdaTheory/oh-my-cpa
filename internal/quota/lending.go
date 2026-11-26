package quota

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// Cross-credential lending advice.
//
// The advice is a pure projection of cards the caller has already read. It groups
// the credentials that share a provider and a plan, ranks the ones that can carry
// traffic by when they refill, and states the priority each of them should hold in
// CPA before its natural reset. It never reads a repository, calls an upstream, or
// writes a route: the console only displays the result, and an operator decides
// what to do with it.

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

// Pool states. A pool either has a board of at least two comparable, serviceable
// cards, or it names why no board could be formed.
const (
	LendingStateAdvised  = "advised"
	LendingStateNoAdvice = "no_advice"
)

// Card roles. A card either holds a suggested priority or stays untouched.
const (
	LendingRoleSuggested = "suggested"
	LendingRoleHold      = "hold"
)

const (
	// A window resetting within the hour is compared as if it reset in an hour, so a
	// card about to refill cannot produce a near-infinite daily allowance.
	lendingMinimumDays = 1.0 / 24.0
	// Below ten percent the card is kept for emergencies rather than ranked.
	lendingLowRemainingPercent = 10.0
	// Refill instants less than one day apart are not worth a different priority:
	// the operator would be re-editing numbers as the two clocks drift.
	lendingTierWindowMS = 86_400_000
	lendingDayMS        = 86_400_000
)

// lendingReasonOrder is the console's fixed reason vocabulary, most specific
// obstacle first. A hold card's primary reason is the first entry here that it
// carries, and the full list is returned in the same order.
var lendingReasonOrder = []string{
	LendingReasonNeedsReauth,
	LendingReasonCooldownActive,
	LendingReasonReadingFailed,
	LendingReasonWindowExhausted,
	LendingReasonReadingStale,
	LendingReasonResetTimeUnknown,
	LendingReasonRemainingUnknown,
	LendingReasonNoLongWindow,
	LendingReasonRemainingBelowTen,
	LendingReasonWindowTypeMismatch,
}

// LendingCardInput is one credential as the caller has already normalized it: the
// plan, status, cooldown and windows have been resolved, and only the comparison is
// left. PriorityKnown is false when the roster entry could not state a priority
// (the compatibility entries OMC synthesizes carry none); Priority carries the
// effective value when it is known.
type LendingCardInput struct {
	AuthIndex       string
	Name            string
	Email           string
	ProjectID       string
	Provider        string
	PlanType        string
	PlanLabel       string
	IsDisabled      bool
	IsUnavailable   bool
	IsCooldown      bool
	NeedsReauth     bool
	ReadingFailed   bool
	Status          string
	ObservedAtMS    int64
	Windows         []QuotaWindow
	Priority        int
	PriorityKnown   bool
	CanEditPriority bool
	Models          []string
}

// LendingCard is one credential's row inside a pool.
type LendingCard struct {
	AuthIndex             string
	Name                  string
	DisplayName           string
	Role                  string
	Tier                  int
	WindowID              string
	WindowKind            string
	RemainingPercent      *float64
	ResetAtMS             *int64
	ResetAccuracy         string
	ObservedAtMS          *int64
	FreshUntilMS          *int64
	DaysToReset           *float64
	DailyAvailablePercent *float64
	IsComparable          bool
	Priority              *int
	PriorityKnown         bool
	PriorityIsDefault     bool
	CanEditPriority       bool
	SuggestedPriority     *int
	NeedsChange           *bool
	PrimaryReason         string
	ReasonCodes           []string
}

// LendingOutsideCard names a credential outside this pool that serves at least one
// of the board's models, so the operator can see what the suggestion competes with.
type LendingOutsideCard struct {
	AuthIndex         string
	Name              string
	DisplayName       string
	Provider          string
	PoolID            string
	PoolLabel         string
	Priority          *int
	PriorityKnown     bool
	PriorityIsDefault bool
}

// LendingPool is the advice for one provider and plan.
type LendingPool struct {
	ID               string
	Provider         string
	PlanType         string
	PlanLabel        string
	WindowKind       string
	State            string
	ValidUntilMS     *int64
	ObservedAtMS     *int64
	FreshUntilMS     *int64
	IsReference      bool
	IsBalanced       bool
	NeedsChangeCount int
	HoldCount        int
	ReasonCodes      []string
	Cards            []LendingCard
	OutsideCards     []LendingOutsideCard
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
		// One credential on a plan is a normal single-seat subscription, not a pool
		// with nothing to compare. Two or more form a pool even when none of them can
		// be ranked: the cards still carry their own reasons.
		if len(members) < 2 {
			for _, member := range members {
				analysis := analyzeLendingCard(member, nowMS)
				reasons := []string{LendingReasonPoolTooSmall}
				reasons = append(reasons, analysis.reasons...)
				result.Unpooled = append(result.Unpooled, LendingUnpooled{
					AuthIndex:   member.AuthIndex,
					Name:        member.Name,
					ReasonCodes: normalizeLendingReasons(reasons),
				})
			}
			continue
		}
		analyses := make([]lendingCardAnalysis, 0, len(members))
		for _, member := range members {
			analyses = append(analyses, analyzeLendingCard(member, nowMS))
		}
		result.Pools = append(result.Pools, buildLendingPool(key, analyses, nowMS))
	}

	attachLendingOutsideCards(result.Pools, cards, nowMS)

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
	days := float64(resetAtMS-nowMS) / lendingDayMS
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
	if observedAt <= 0 || nowMS-observedAt > freshWindow.Milliseconds() || window.Stale ||
		(window.ResetAtMS != nil && *window.ResetAtMS <= nowMS) {
		// A refill instant that has already passed invalidates the reading: the share
		// was measured against a window that has since reset.
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

type lendingBoardAssignment struct {
	tier        int
	suggestion  int
	needsChange *bool
}

func buildLendingPool(key string, analyses []lendingCardAnalysis, nowMS int64) LendingPool {
	pool := LendingPool{
		ID:       key,
		Provider: normalizeLendingKey(analyses[0].input.Provider),
		PlanType: normalizeLendingKey(analyses[0].input.PlanType),
		// The contract takes the label from the first card by name, so a pool with a
		// single labelled seat does not borrow another seat's label.
		PlanLabel:    analyses[0].input.PlanLabel,
		State:        LendingStateNoAdvice,
		ReasonCodes:  []string{},
		Cards:        []LendingCard{},
		OutsideCards: []LendingOutsideCard{},
	}

	serviceReasons := make([][]string, len(analyses))
	kinds := make(map[string][]int)
	for index, analysis := range analyses {
		serviceReasons[index] = lendingServiceReasons(analysis, nowMS)
		if analysis.comparable && len(serviceReasons[index]) == 0 {
			kinds[analysis.window.Kind] = append(kinds[analysis.window.Kind], index)
		}
	}

	// One board per pool: the window type most cards share, and among types with
	// the same count the one that refills first. Ranking two clock types against
	// each other would compare figures that do not mean the same thing.
	boardKind, board := lendingBoard(kinds, analyses)
	hasBoard := len(board) >= 2
	if hasBoard {
		pool.State = LendingStateAdvised
		pool.WindowKind = boardKind
	}

	assignments := map[int]lendingBoardAssignment{}
	if hasBoard {
		pool.IsBalanced = lendingAssignBoard(board, analyses, assignments)
	}

	for index, analysis := range analyses {
		card := lendingCardFromAnalysis(analysis)
		if assignment, ok := assignments[index]; ok {
			card.Role = LendingRoleSuggested
			card.Tier = assignment.tier
			card.SuggestedPriority = lendingInt(assignment.suggestion)
			card.NeedsChange = assignment.needsChange
		} else {
			card.Role = LendingRoleHold
			reasons := append([]string{}, analysis.reasons...)
			reasons = append(reasons, serviceReasons[index]...)
			// Outside the board is itself a reason: either this card shares no window
			// type with a board, or it is the only card of its type.
			switch {
			case !hasBoard && analysis.comparable:
				reasons = append(reasons, LendingReasonWindowTypeMismatch)
			case hasBoard && analysis.window != nil && analysis.window.Kind != boardKind:
				reasons = append(reasons, LendingReasonWindowTypeMismatch)
			}
			card.ReasonCodes = normalizeLendingReasons(reasons)
			if len(card.ReasonCodes) > 0 {
				card.PrimaryReason = card.ReasonCodes[0]
			}
		}
		pool.Cards = append(pool.Cards, card)
	}

	var validUntil, observedAt, freshUntil *int64
	for index, analysis := range analyses {
		if analysis.observedAt > 0 {
			observedAt = earlierInstant(observedAt, analysis.observedAt)
		}
		if analysis.freshUntil > 0 {
			freshUntil = earlierInstant(freshUntil, analysis.freshUntil)
		}
		if !hasBoard || !lendingContainsIndex(board, index) {
			continue
		}
		if analysis.window.ResetAtMS != nil {
			validUntil = earlierInstant(validUntil, *analysis.window.ResetAtMS)
		}
		if analysis.window.ResetAccuracy == "derived" || analysis.window.ResetAccuracy == "approximate" {
			pool.IsReference = true
		}
	}
	// A pool without a board has no advice deadline; borrowing the earliest reset
	// would read as a promise the projection cannot keep.
	pool.ValidUntilMS = validUntil
	pool.ObservedAtMS = observedAt
	pool.FreshUntilMS = freshUntil

	for _, card := range pool.Cards {
		switch card.Role {
		case LendingRoleSuggested:
			if card.NeedsChange != nil && *card.NeedsChange {
				pool.NeedsChangeCount++
			}
		default:
			pool.HoldCount++
			for _, reason := range card.ReasonCodes {
				pool.ReasonCodes = append(pool.ReasonCodes, reason)
			}
		}
	}
	pool.ReasonCodes = normalizeLendingReasons(pool.ReasonCodes)
	if pool.State == LendingStateAdvised {
		// A pool that gives advice states holds per card; the pool-level list would
		// repeat them without adding a decision.
		pool.ReasonCodes = []string{}
	}

	sort.SliceStable(pool.Cards, func(first, second int) bool {
		leftRank, rightRank := lendingRoleRank(pool.Cards[first].Role), lendingRoleRank(pool.Cards[second].Role)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if pool.Cards[first].Tier != pool.Cards[second].Tier {
			return pool.Cards[first].Tier < pool.Cards[second].Tier
		}
		if pool.Cards[first].DisplayName != pool.Cards[second].DisplayName {
			return pool.Cards[first].DisplayName < pool.Cards[second].DisplayName
		}
		return pool.Cards[first].AuthIndex < pool.Cards[second].AuthIndex
	})
	return pool
}

// lendingBoard returns the window type that carries the board and its card indexes.
// Ties go to the type whose earliest refill comes first, then to the lower kind so
// the choice is stable.
func lendingBoard(kinds map[string][]int, analyses []lendingCardAnalysis) (string, []int) {
	bestKind := ""
	bestCount := 0
	var bestReset int64
	for kind, indexes := range kinds {
		reset := int64(math.MaxInt64)
		for _, index := range indexes {
			if analysis := analyses[index]; analysis.window != nil && analysis.window.ResetAtMS != nil && *analysis.window.ResetAtMS < reset {
				reset = *analysis.window.ResetAtMS
			}
		}
		better := len(indexes) > bestCount
		if len(indexes) == bestCount && bestKind != "" {
			better = reset < bestReset || (reset == bestReset && kind < bestKind)
		}
		if better {
			bestKind = kind
			bestCount = len(indexes)
			bestReset = reset
		}
	}
	if bestKind == "" {
		return "", nil
	}
	return bestKind, kinds[bestKind]
}

// lendingAssignBoard tiers the board by refill instant and assigns each tier a
// priority. It returns whether the board is one tier (the "about the same" case).
func lendingAssignBoard(board []int, analyses []lendingCardAnalysis, assignments map[int]lendingBoardAssignment) bool {
	ranked := append([]int{}, board...)
	sort.SliceStable(ranked, func(first, second int) bool {
		left, right := analyses[ranked[first]], analyses[ranked[second]]
		if *left.window.ResetAtMS != *right.window.ResetAtMS {
			return *left.window.ResetAtMS > *right.window.ResetAtMS
		}
		if leftDisplay, rightDisplay := lendingDisplayName(left.input), lendingDisplayName(right.input); leftDisplay != rightDisplay {
			return leftDisplay < rightDisplay
		}
		return left.input.AuthIndex < right.input.AuthIndex
	})

	tiers := make([][]int, 0, len(ranked))
	tiers = append(tiers, []int{ranked[0]})
	for _, index := range ranked[1:] {
		// Anchor a tier on its latest refill. Comparing only with the previous card would
		// chain two cards a day apart into one tier even when its ends are days apart.
		anchor := *analyses[tiers[len(tiers)-1][0]].window.ResetAtMS
		gap := anchor - *analyses[index].window.ResetAtMS
		if gap < lendingTierWindowMS {
			tiers[len(tiers)-1] = append(tiers[len(tiers)-1], index)
			continue
		}
		tiers = append(tiers, []int{index})
	}

	highest := 0
	hasKnown := false
	for _, index := range board {
		card := analyses[index].input
		if !card.PriorityKnown {
			continue
		}
		if !hasKnown || card.Priority > highest {
			highest = card.Priority
			hasKnown = true
		}
	}

	allKnown := true
	for _, index := range board {
		allKnown = allKnown && analyses[index].input.PriorityKnown
	}
	alreadyOrdered := allKnown
	if alreadyOrdered {
		for earlier := 0; earlier < len(tiers) && alreadyOrdered; earlier++ {
			lowestEarlier := math.MaxInt
			for _, index := range tiers[earlier] {
				if priority := analyses[index].input.Priority; priority < lowestEarlier {
					lowestEarlier = priority
				}
			}
			for later := earlier + 1; later < len(tiers); later++ {
				highestLater := math.MinInt
				for _, index := range tiers[later] {
					if priority := analyses[index].input.Priority; priority > highestLater {
						highestLater = priority
					}
				}
				if lowestEarlier <= highestLater {
					alreadyOrdered = false
					break
				}
			}
		}
	}

	balanced := len(tiers) == 1
	for tierIndex, tier := range tiers {
		value := highest - 10*tierIndex
		for _, index := range tier {
			assignment := lendingBoardAssignment{tier: tierIndex + 1, suggestion: value}
			if alreadyOrdered || balanced {
				card := analyses[index].input
				if card.PriorityKnown {
					assignment.suggestion = card.Priority
					unchanged := false
					assignment.needsChange = &unchanged
				}
			} else if card := analyses[index].input; card.PriorityKnown {
				changed := card.Priority != value
				assignment.needsChange = &changed
			}
			assignments[index] = assignment
		}
	}
	return balanced
}

func lendingContainsIndex(indexes []int, index int) bool {
	for _, candidate := range indexes {
		if candidate == index {
			return true
		}
	}
	return false
}

// lendingServiceReasons names why a card cannot take traffic right now. Cooldown,
// reauthorization and a failed read are properties of the credential, so they apply
// even when the window itself is unusable; the reason list is a union, not a choice.
// Exhaustion and low remaining stay mutually exclusive because they describe the same
// balance.
func lendingServiceReasons(analysis lendingCardAnalysis, nowMS int64) []string {
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
	} else if analysis.window != nil && analysis.window.RemainingPercent != nil && *analysis.window.RemainingPercent < lendingLowRemainingPercent {
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

func lendingCardFromAnalysis(analysis lendingCardAnalysis) LendingCard {
	card := LendingCard{
		AuthIndex:       analysis.input.AuthIndex,
		Name:            analysis.input.Name,
		DisplayName:     lendingDisplayName(analysis.input),
		IsComparable:    analysis.comparable,
		PriorityKnown:   analysis.input.PriorityKnown,
		CanEditPriority: analysis.input.CanEditPriority,
		ReasonCodes:     []string{},
	}
	if card.DisplayName == "" {
		card.DisplayName = card.AuthIndex
	}
	if analysis.input.PriorityKnown {
		card.Priority = lendingInt(analysis.input.Priority)
		card.PriorityIsDefault = analysis.input.Priority == 0
	}
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
	if role == LendingRoleSuggested {
		return 0
	}
	return 1
}

// attachLendingOutsideCards fills each advised pool's outside roster. The shared
// model is the intersection of every board card's model list: a credential that
// cannot serve all of them is not competing for the same request stream. Cards OMC
// synthesizes for a compatibility entry carry no model list, so a board containing
// one yields no outside reminder rather than a guess.
func attachLendingOutsideCards(pools []LendingPool, cards []LendingCardInput, nowMS int64) {
	byIndex := make(map[string]LendingCardInput, len(cards))
	for _, card := range cards {
		byIndex[card.AuthIndex] = card
	}
	poolID := make(map[string]string, len(cards))
	poolLabel := make(map[string]string, len(cards))
	for _, pool := range pools {
		for _, card := range pool.Cards {
			poolID[card.AuthIndex] = pool.ID
			poolLabel[card.AuthIndex] = pool.PlanLabel
		}
	}

	for poolIndex := range pools {
		pool := &pools[poolIndex]
		if pool.State != LendingStateAdvised {
			continue
		}
		inPool := make(map[string]bool, len(pool.Cards))
		for _, card := range pool.Cards {
			inPool[card.AuthIndex] = true
		}
		common := map[string]bool{}
		first := true
		for _, card := range pool.Cards {
			if card.Role != LendingRoleSuggested {
				continue
			}
			models := byIndex[card.AuthIndex].Models
			if len(models) == 0 {
				common = nil
				break
			}
			if first {
				for _, model := range models {
					if model != "" {
						common[model] = true
					}
				}
				first = false
				continue
			}
			for model := range common {
				if !lendingHasModel(models, model) {
					delete(common, model)
				}
			}
			if len(common) == 0 {
				break
			}
		}
		if len(common) == 0 {
			continue
		}
		outside := make([]LendingOutsideCard, 0, len(cards))
		for _, card := range cards {
			if inPool[card.AuthIndex] || !lendingOutsideEligible(card, nowMS) {
				continue
			}
			if !lendingSharesModel(card.Models, common) {
				continue
			}
			entry := LendingOutsideCard{
				AuthIndex:     card.AuthIndex,
				Name:          card.Name,
				DisplayName:   lendingDisplayName(card),
				Provider:      normalizeLendingKey(card.Provider),
				PoolID:        poolID[card.AuthIndex],
				PoolLabel:     poolLabel[card.AuthIndex],
				PriorityKnown: card.PriorityKnown,
			}
			if entry.DisplayName == "" {
				entry.DisplayName = card.AuthIndex
			}
			if card.PriorityKnown {
				entry.Priority = lendingInt(card.Priority)
				entry.PriorityIsDefault = card.Priority == 0
			}
			outside = append(outside, entry)
		}
		sort.SliceStable(outside, func(first, second int) bool {
			if outside[first].DisplayName != outside[second].DisplayName {
				return outside[first].DisplayName < outside[second].DisplayName
			}
			return outside[first].AuthIndex < outside[second].AuthIndex
		})
		pool.OutsideCards = outside
	}
}

func lendingOutsideEligible(card LendingCardInput, nowMS int64) bool {
	if card.IsDisabled || card.IsUnavailable || card.IsCooldown || card.NeedsReauth || card.ReadingFailed {
		return false
	}
	return card.Status != "exhausted" && !lendingHasExhaustedStandardWindow(card, nowMS)
}

func lendingHasModel(models []string, model string) bool {
	for _, candidate := range models {
		if candidate == model {
			return true
		}
	}
	return false
}

func lendingSharesModel(models []string, common map[string]bool) bool {
	for _, model := range models {
		if common[model] {
			return true
		}
	}
	return false
}

func normalizeLendingReasons(reasons []string) []string {
	if len(reasons) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(reasons))
	unique := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason == "" || seen[reason] {
			continue
		}
		seen[reason] = true
		unique = append(unique, reason)
	}
	sort.SliceStable(unique, func(first, second int) bool {
		left, right := lendingReasonRank(unique[first]), lendingReasonRank(unique[second])
		if left != right {
			return left < right
		}
		return unique[first] < unique[second]
	})
	return unique
}

func lendingReasonRank(reason string) int {
	for index, candidate := range lendingReasonOrder {
		if candidate == reason {
			return index
		}
	}
	return len(lendingReasonOrder)
}

// lendingCredentialUUID is the opaque prefix the console strips before showing an
// account label, mirrored here so a pool card and the OAuth list read the same.
var lendingCredentialUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:$|[\s:|/()\[\]_-]+)`)

func lendingDisplayName(card LendingCardInput) string {
	// The OAuth list picks email or, when email is blank, project id before it
	// strips the opaque prefixes; an email that cleans down to nothing therefore
	// falls through to the file name, not to the project id.
	account := card.Email
	if strings.TrimSpace(account) == "" {
		account = card.ProjectID
	}
	if label := lendingCredentialLabel(account, card.AuthIndex); label != "" {
		return label
	}
	if label := lendingCredentialLabel(card.Name, card.AuthIndex); label != "" {
		return label
	}
	return card.AuthIndex
}

func lendingCredentialLabel(value, authIndex string) string {
	label := strings.TrimSpace(value)
	if label == "" {
		return ""
	}
	key := strings.TrimSpace(authIndex)
	if key != "" {
		if label == key {
			return ""
		}
		if strings.HasPrefix(label, key) && len(label) > len(key) && strings.ContainsRune(" \t\r\n:|/()[_-", rune(label[len(key)])) {
			label = strings.TrimSpace(strings.TrimLeft(label[len(key):], " \t\r\n:|/()[]_-"))
		}
	}
	label = strings.TrimSpace(lendingCredentialUUID.ReplaceAllString(label, ""))
	return label
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

func lendingInt(value int) *int {
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
