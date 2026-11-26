package quota

import (
	"math"
	"testing"
)

const lendingTestNow = int64(1_763_000_000_000)

const lendingTestDayMS = int64(24 * 60 * 60 * 1000)

func lendingTestWindow(remaining float64, resetAtMS int64, kind string, periodHours float64) QuotaWindow {
	remainingCopy := remaining
	resetCopy := resetAtMS
	periodCopy := periodHours
	return QuotaWindow{
		ID:               kind,
		Kind:             kind,
		Scope:            "standard",
		RemainingPercent: &remainingCopy,
		ResetAtMS:        &resetCopy,
		PeriodHours:      &periodCopy,
		ResetAccuracy:    "exact",
	}
}

func lendingTestWeekly(remaining float64, resetAtMS int64) QuotaWindow {
	return lendingTestWindow(remaining, resetAtMS, "weekly", 168)
}

func lendingTestDaily(remaining float64, resetAtMS int64) QuotaWindow {
	return lendingTestWindow(remaining, resetAtMS, "daily", 24)
}

func lendingTestCard(authIndex, name string, windows ...QuotaWindow) LendingCardInput {
	return LendingCardInput{
		AuthIndex:       authIndex,
		Name:            name,
		Provider:        "codex",
		PlanType:        "pro",
		PlanLabel:       "Pro 200",
		ObservedAtMS:    lendingTestNow - 60*60*1000,
		Windows:         windows,
		PriorityKnown:   true,
		CanEditPriority: true,
		Models:          []string{"gpt-5", "gpt-5-codex"},
	}
}

func lendingWithPriority(card LendingCardInput, priority int) LendingCardInput {
	card.Priority = priority
	card.PriorityKnown = true
	return card
}

func lendingWithUnknownPriority(card LendingCardInput) LendingCardInput {
	card.Priority = 0
	card.PriorityKnown = false
	return card
}

func lendingPoolCard(t *testing.T, pool *LendingPool, authIndex string) LendingCard {
	t.Helper()
	if pool == nil {
		t.Fatalf("pool is nil, want a pool containing %s", authIndex)
	}
	for _, card := range pool.Cards {
		if card.AuthIndex == authIndex {
			return card
		}
	}
	t.Fatalf("pool %s has no card %s: %+v", pool.ID, authIndex, pool.Cards)
	return LendingCard{}
}

func lendingHasReason(codes []string, reason string) bool {
	for _, code := range codes {
		if code == reason {
			return true
		}
	}
	return false
}

func lendingUnpooledReasons(t *testing.T, result LendingResult, authIndex string) []string {
	t.Helper()
	for _, entry := range result.Unpooled {
		if entry.AuthIndex == authIndex {
			return entry.ReasonCodes
		}
	}
	t.Fatalf("credential %s is in neither a pool nor the unpooled list", authIndex)
	return nil
}

func lendingDerefInt(value *int) int {
	if value == nil {
		return math.MinInt
	}
	return *value
}

func lendingDerefBool(value *bool) (bool, bool) {
	if value == nil {
		return false, false
	}
	return *value, true
}

func TestLendingRanksByLatestRefillNotDailyAllowance(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingWithPriority(lendingTestCard("early", "early", lendingTestWeekly(40, lendingTestNow+2*lendingTestDayMS)), 0),
		lendingWithPriority(lendingTestCard("late", "late", lendingTestWeekly(20, lendingTestNow+4*lendingTestDayMS)), 0),
	}, lendingTestNow)

	if len(result.Pools) != 1 {
		t.Fatalf("pools = %+v", result.Pools)
	}
	pool := result.Pools[0]
	if pool.State != LendingStateAdvised || pool.WindowKind != "weekly" || pool.IsBalanced {
		t.Fatalf("pool = %+v", pool)
	}
	late := lendingPoolCard(t, &pool, "late")
	early := lendingPoolCard(t, &pool, "early")
	if late.Role != LendingRoleSuggested || late.Tier != 1 || lendingDerefInt(late.SuggestedPriority) != 0 {
		t.Fatalf("late card = %+v, want tier 1 at 0", late)
	}
	if early.Role != LendingRoleSuggested || early.Tier != 2 || lendingDerefInt(early.SuggestedPriority) != -10 {
		t.Fatalf("early card = %+v, want tier 2 at -10", early)
	}
	if changed, ok := lendingDerefBool(late.NeedsChange); !ok || changed {
		t.Fatalf("late needs_change = %v %t, want false", late.NeedsChange, ok)
	}
	if changed, ok := lendingDerefBool(early.NeedsChange); !ok || !changed {
		t.Fatalf("early needs_change = %v %t, want true", early.NeedsChange, ok)
	}
	// The early refill is still the advice deadline, even though the late refill ranks first.
	if pool.ValidUntilMS == nil || *pool.ValidUntilMS != lendingTestNow+2*lendingTestDayMS {
		t.Fatalf("valid_until = %v", pool.ValidUntilMS)
	}
	if pool.NeedsChangeCount != 1 || pool.HoldCount != 0 {
		t.Fatalf("counts = change %d hold %d", pool.NeedsChangeCount, pool.HoldCount)
	}
	// The old ranking key must not decide the order: the early card has the higher daily rate.
	if early.DailyAvailablePercent == nil || late.DailyAvailablePercent == nil || *early.DailyAvailablePercent <= *late.DailyAvailablePercent {
		t.Fatalf("daily rates = early %v late %v", early.DailyAvailablePercent, late.DailyAvailablePercent)
	}
}

func TestLendingTierGapUsesOneDay(t *testing.T) {
	cases := []struct {
		name      string
		gap       int64
		wantTiers int
		balanced  bool
	}{
		{name: "under one day is one tier", gap: lendingTestDayMS - 1, wantTiers: 1, balanced: true},
		{name: "23h59m is one tier", gap: 23*60*60*1000 + 59*60*1000, wantTiers: 1, balanced: true},
		{name: "exactly one day splits", gap: lendingTestDayMS, wantTiers: 2, balanced: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := ProjectLendingAdvice([]LendingCardInput{
				lendingTestCard("a", "a", lendingTestWeekly(60, lendingTestNow+3*lendingTestDayMS)),
				lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+3*lendingTestDayMS-testCase.gap)),
			}, lendingTestNow)
			pool := result.Pools[0]
			if pool.IsBalanced != testCase.balanced {
				t.Fatalf("is_balanced = %t, want %t", pool.IsBalanced, testCase.balanced)
			}
			tiers := map[int]bool{}
			for _, card := range pool.Cards {
				tiers[card.Tier] = true
			}
			if len(tiers) != testCase.wantTiers {
				t.Fatalf("tiers = %v, want %d", tiers, testCase.wantTiers)
			}
			if testCase.balanced && (lendingDerefInt(lendingPoolCard(t, &pool, "b").SuggestedPriority) != 0 || pool.NeedsChangeCount != 0) {
				t.Fatalf("balanced pool should not change anything: %+v", pool)
			}
		})
	}
}

func TestLendingTierAnchorsOnTheLatestRefill(t *testing.T) {
	// 72h and 60h are 12h apart, so the first two share a tier. 45h is 27h after the
	// tier anchor even though it is only 15h after the previous card, so comparing with
	// the previous card alone would wrongly chain all three into one tier.
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingWithPriority(lendingTestCard("a", "a", lendingTestWeekly(60, lendingTestNow+72*60*60*1000)), 0),
		lendingWithPriority(lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+60*60*60*1000)), 0),
		lendingWithPriority(lendingTestCard("c", "c", lendingTestWeekly(60, lendingTestNow+45*60*60*1000)), 0),
	}, lendingTestNow)
	pool := result.Pools[0]
	if pool.State != LendingStateAdvised || pool.IsBalanced {
		t.Fatalf("pool = %+v", pool)
	}
	wantTier := map[string]int{"a": 1, "b": 1, "c": 2}
	wantSuggest := map[string]int{"a": 0, "b": 0, "c": -10}
	wantChange := map[string]bool{"a": false, "b": false, "c": true}
	for authIndex, tier := range wantTier {
		card := lendingPoolCard(t, &pool, authIndex)
		if card.Tier != tier {
			t.Fatalf("card %s tier = %d, want %d", authIndex, card.Tier, tier)
		}
		if got := lendingDerefInt(card.SuggestedPriority); got != wantSuggest[authIndex] {
			t.Fatalf("card %s suggestion = %d, want %d", authIndex, got, wantSuggest[authIndex])
		}
		changed, ok := lendingDerefBool(card.NeedsChange)
		if !ok || changed != wantChange[authIndex] {
			t.Fatalf("card %s needs_change = %v %t, want %t", authIndex, card.NeedsChange, ok, wantChange[authIndex])
		}
	}
	if pool.NeedsChangeCount != 1 {
		t.Fatalf("needs_change_count = %d, want 1", pool.NeedsChangeCount)
	}
}

func TestLendingSuggestionValues(t *testing.T) {
	cases := []struct {
		name        string
		priorities  []int
		known       []bool
		wantSuggest []int
		wantChange  []bool // false means needs_change is absent
		wantChanged int
	}{
		{
			name: "all default split into tiers", priorities: []int{0, 0, 0}, known: []bool{true, true, true},
			wantSuggest: []int{0, -10, -20}, wantChange: []bool{false, true, true}, wantChanged: 2,
		},
		{
			name: "already ordered keeps every number", priorities: []int{20, 10, 0}, known: []bool{true, true, true},
			wantSuggest: []int{20, 10, 0}, wantChange: []bool{false, false, false}, wantChanged: 0,
		},
		{
			name: "first seat already holds the maximum", priorities: []int{20, 10}, known: []bool{true, true},
			wantSuggest: []int{20, 10}, wantChange: []bool{false, false}, wantChanged: 0,
		},
		{
			name: "negative numbers are legal", priorities: []int{-5, -5}, known: []bool{true, true},
			wantSuggest: []int{-5, -15}, wantChange: []bool{false, true}, wantChanged: 1,
		},
		{
			name: "unreadable priority still gets a number", priorities: []int{0, 0}, known: []bool{true, false},
			wantSuggest: []int{0, -10}, wantChange: []bool{false, false}, wantChanged: 0,
		},
		{
			name: "unreadable priority in the first tier does not block the other seat", priorities: []int{0, 0}, known: []bool{false, true},
			wantSuggest: []int{0, -10}, wantChange: []bool{false, true}, wantChanged: 1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cards := make([]LendingCardInput, 0, len(testCase.priorities))
			resets := []int64{6, 4, 2}
			for index := range testCase.priorities {
				card := lendingTestCard(string(rune('a'+index)), string(rune('a'+index)),
					lendingTestWeekly(50, lendingTestNow+resets[index]*lendingTestDayMS))
				card.Priority = testCase.priorities[index]
				card.PriorityKnown = testCase.known[index]
				cards = append(cards, card)
			}
			result := ProjectLendingAdvice(cards, lendingTestNow)
			pool := result.Pools[0]
			for index := range testCase.priorities {
				card := lendingPoolCard(t, &pool, string(rune('a'+index)))
				if got := lendingDerefInt(card.SuggestedPriority); got != testCase.wantSuggest[index] {
					t.Fatalf("card %d suggestion = %d, want %d", index, got, testCase.wantSuggest[index])
				}
				changed, ok := lendingDerefBool(card.NeedsChange)
				if !testCase.wantChange[index] && !testCase.known[index] {
					if ok {
						t.Fatalf("card %d with unreadable priority carries needs_change", index)
					}
					continue
				}
				if !ok || changed != testCase.wantChange[index] {
					t.Fatalf("card %d needs_change = %v %t, want %t", index, card.NeedsChange, ok, testCase.wantChange[index])
				}
			}
			if pool.NeedsChangeCount != testCase.wantChanged {
				t.Fatalf("needs_change_count = %d, want %d", pool.NeedsChangeCount, testCase.wantChanged)
			}
		})
	}
}

func TestLendingAllPrioritiesUnreadableStillSuggests(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingWithUnknownPriority(lendingTestCard("a", "a", lendingTestWeekly(60, lendingTestNow+4*lendingTestDayMS))),
		lendingWithUnknownPriority(lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+2*lendingTestDayMS))),
	}, lendingTestNow)
	pool := result.Pools[0]
	if pool.IsBalanced || pool.NeedsChangeCount != 0 {
		t.Fatalf("pool = %+v", pool)
	}
	for authIndex, want := range map[string]int{"a": 0, "b": -10} {
		card := lendingPoolCard(t, &pool, authIndex)
		if got := lendingDerefInt(card.SuggestedPriority); got != want {
			t.Fatalf("card %s suggestion = %d, want %d", authIndex, got, want)
		}
		if card.Priority != nil {
			t.Fatalf("card %s carries a priority: %v", authIndex, *card.Priority)
		}
		if _, ok := lendingDerefBool(card.NeedsChange); ok {
			t.Fatalf("card %s carries needs_change", authIndex)
		}
	}
}

func TestLendingPartialPriorityKnowledgeBreaksOrdering(t *testing.T) {
	cases := []struct {
		name       string
		unreadable int
	}{
		{name: "reported value would break the order", unreadable: 999},
		{name: "reported value would look already ordered", unreadable: 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// The unreadable card reports a value, but PriorityKnown is false, so it must
			// neither raise H nor complete the ordering check. A value of 1 would look
			// ordered if the flag were ignored, which is the case this subtest pins.
			unknown := lendingWithUnknownPriority(lendingTestCard("c", "c", lendingTestWeekly(60, lendingTestNow+2*lendingTestDayMS)))
			unknown.Priority = testCase.unreadable
			result := ProjectLendingAdvice([]LendingCardInput{
				lendingWithPriority(lendingTestCard("a", "a", lendingTestWeekly(60, lendingTestNow+6*lendingTestDayMS)), 20),
				lendingWithPriority(lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+4*lendingTestDayMS)), 5),
				unknown,
			}, lendingTestNow)
			pool := result.Pools[0]
			if pool.State != LendingStateAdvised || pool.IsBalanced {
				t.Fatalf("pool = %+v", pool)
			}
			wantSuggest := map[string]int{"a": 20, "b": 10, "c": 0}
			wantChange := map[string]bool{"a": false, "b": true}
			for authIndex, want := range wantSuggest {
				card := lendingPoolCard(t, &pool, authIndex)
				if got := lendingDerefInt(card.SuggestedPriority); got != want {
					t.Fatalf("card %s suggestion = %d, want %d (H must come from readable priorities only)", authIndex, got, want)
				}
				if authIndex == "c" {
					if _, ok := lendingDerefBool(card.NeedsChange); ok {
						t.Fatalf("unreadable card carries needs_change")
					}
					continue
				}
				changed, ok := lendingDerefBool(card.NeedsChange)
				if !ok || changed != wantChange[authIndex] {
					t.Fatalf("card %s needs_change = %v %t, want %t", authIndex, card.NeedsChange, ok, wantChange[authIndex])
				}
			}
			if pool.NeedsChangeCount != 1 {
				t.Fatalf("needs_change_count = %d, want 1", pool.NeedsChangeCount)
			}
		})
	}
}

func TestLendingSameTierKeepsCurrentNumbers(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingWithPriority(lendingTestCard("a", "a", lendingTestWeekly(50, lendingTestNow+3*lendingTestDayMS)), 7),
		lendingWithPriority(lendingTestCard("b", "b", lendingTestWeekly(50, lendingTestNow+3*lendingTestDayMS-6*60*60*1000)), 3),
	}, lendingTestNow)
	pool := result.Pools[0]
	if !pool.IsBalanced || pool.NeedsChangeCount != 0 {
		t.Fatalf("pool = %+v", pool)
	}
	for index, want := range []int{7, 3} {
		card := pool.Cards[index]
		if lendingDerefInt(card.SuggestedPriority) != want {
			t.Fatalf("card %s suggestion = %d, want %d", card.AuthIndex, lendingDerefInt(card.SuggestedPriority), want)
		}
	}
}

func TestLendingSameTierTreatsUnreadablePriorityAsNoChange(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingWithPriority(lendingTestCard("a", "a", lendingTestWeekly(50, lendingTestNow+3*lendingTestDayMS)), 7),
		lendingWithPriority(lendingTestCard("b", "b", lendingTestWeekly(50, lendingTestNow+3*lendingTestDayMS-6*60*60*1000)), 3),
		lendingWithUnknownPriority(lendingTestCard("c", "c", lendingTestWeekly(50, lendingTestNow+3*lendingTestDayMS-12*60*60*1000))),
	}, lendingTestNow)
	pool := result.Pools[0]
	if !pool.IsBalanced || pool.NeedsChangeCount != 0 {
		t.Fatalf("pool = %+v", pool)
	}
	for index, want := range []int{7, 3, 7} {
		card := pool.Cards[index]
		if lendingDerefInt(card.SuggestedPriority) != want {
			t.Fatalf("card %s suggestion = %d, want %d", card.AuthIndex, lendingDerefInt(card.SuggestedPriority), want)
		}
	}
	if _, ok := lendingDerefBool(pool.Cards[2].NeedsChange); ok {
		t.Fatalf("unreadable card carries needs_change")
	}
}

func TestLendingCardLevelJudgement(t *testing.T) {
	stale := lendingTestCard("stale", "stale", lendingTestWeekly(90, lendingTestNow+5*lendingTestDayMS))
	stale.ObservedAtMS = lendingTestNow - 25*60*60*1000
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("weekly-a", "weekly-a", lendingTestWeekly(70, lendingTestNow+4*lendingTestDayMS)),
		lendingTestCard("weekly-b", "weekly-b", lendingTestWeekly(60, lendingTestNow+2*lendingTestDayMS)),
		lendingTestCard("daily", "daily", lendingTestDaily(50, lendingTestNow+lendingTestDayMS)),
		stale,
	}, lendingTestNow)

	pool := result.Pools[0]
	if pool.State != LendingStateAdvised || pool.HoldCount != 2 {
		t.Fatalf("pool = %+v", pool)
	}
	if card := lendingPoolCard(t, &pool, "weekly-a"); card.Role != LendingRoleSuggested {
		t.Fatalf("weekly-a = %+v", card)
	}
	daily := lendingPoolCard(t, &pool, "daily")
	if daily.Role != LendingRoleHold || daily.PrimaryReason != LendingReasonWindowTypeMismatch ||
		!lendingHasReason(daily.ReasonCodes, LendingReasonWindowTypeMismatch) {
		t.Fatalf("daily = %+v", daily)
	}
	staleCard := lendingPoolCard(t, &pool, "stale")
	if staleCard.Role != LendingRoleHold || staleCard.PrimaryReason != LendingReasonReadingStale || staleCard.IsComparable {
		t.Fatalf("stale = %+v", staleCard)
	}
	// A card that cannot be read no longer blocks the readable seats.
	if pool.NeedsChangeCount != 1 {
		t.Fatalf("needs_change_count = %d, want 1", pool.NeedsChangeCount)
	}
}

func TestLendingNoAdviceWhenOnlyOneParticipant(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("ok", "ok", lendingTestWeekly(80, lendingTestNow+3*lendingTestDayMS)),
		lendingTestCard("low", "low", lendingTestWeekly(5, lendingTestNow+30*lendingTestDayMS)),
	}, lendingTestNow)

	pool := result.Pools[0]
	if pool.State != LendingStateNoAdvice || pool.WindowKind != "" || pool.ValidUntilMS != nil {
		t.Fatalf("pool = %+v", pool)
	}
	if pool.HoldCount != 2 || pool.NeedsChangeCount != 0 {
		t.Fatalf("counts = %+v", pool)
	}
	low := lendingPoolCard(t, &pool, "low")
	if low.PrimaryReason != LendingReasonRemainingBelowTen {
		t.Fatalf("low = %+v", low)
	}
	ok := lendingPoolCard(t, &pool, "ok")
	if ok.PrimaryReason != LendingReasonWindowTypeMismatch || !lendingHasReason(ok.ReasonCodes, LendingReasonWindowTypeMismatch) {
		t.Fatalf("ok = %+v", ok)
	}
	if len(pool.ReasonCodes) != 2 || pool.ReasonCodes[0] != LendingReasonRemainingBelowTen || pool.ReasonCodes[1] != LendingReasonWindowTypeMismatch {
		t.Fatalf("pool reasons = %v", pool.ReasonCodes)
	}
}

func TestLendingBoardPicksTheMostCommonWindowType(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("weekly-a", "weekly-a", lendingTestWeekly(70, lendingTestNow+4*lendingTestDayMS)),
		lendingTestCard("weekly-b", "weekly-b", lendingTestWeekly(60, lendingTestNow+2*lendingTestDayMS)),
		lendingTestCard("daily-a", "daily-a", lendingTestDaily(50, lendingTestNow+lendingTestDayMS)),
	}, lendingTestNow)
	pool := result.Pools[0]
	if pool.State != LendingStateAdvised || pool.WindowKind != "weekly" {
		t.Fatalf("pool = %+v", pool)
	}
	if card := lendingPoolCard(t, &pool, "daily-a"); card.Role != LendingRoleHold || card.PrimaryReason != LendingReasonWindowTypeMismatch {
		t.Fatalf("daily-a = %+v", card)
	}
}

func TestLendingBoardTieGoesToTheEarlierRefill(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("weekly-a", "weekly-a", lendingTestWeekly(70, lendingTestNow+4*lendingTestDayMS)),
		lendingTestCard("weekly-b", "weekly-b", lendingTestWeekly(60, lendingTestNow+5*lendingTestDayMS)),
		lendingTestCard("daily-a", "daily-a", lendingTestDaily(50, lendingTestNow+lendingTestDayMS)),
		lendingTestCard("daily-b", "daily-b", lendingTestDaily(40, lendingTestNow+2*lendingTestDayMS)),
	}, lendingTestNow)
	pool := result.Pools[0]
	if pool.WindowKind != "daily" {
		t.Fatalf("window kind = %q, want daily", pool.WindowKind)
	}
	if card := lendingPoolCard(t, &pool, "weekly-a"); card.Role != LendingRoleHold {
		t.Fatalf("weekly-a = %+v", card)
	}
	// Two cards of one type still form a board; a tie between two single-card types does not.
	single := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("weekly-a", "weekly-a", lendingTestWeekly(70, lendingTestNow+4*lendingTestDayMS)),
		lendingTestCard("daily-a", "daily-a", lendingTestDaily(50, lendingTestNow+lendingTestDayMS)),
	}, lendingTestNow)
	if single.Pools[0].State != LendingStateNoAdvice {
		t.Fatalf("single-card types formed advice: %+v", single.Pools[0])
	}
}

func TestLendingReasonOrderAndPrimaryReason(t *testing.T) {
	blocked := lendingTestCard("blocked", "blocked", lendingTestWeekly(80, lendingTestNow+4*lendingTestDayMS))
	blocked.NeedsReauth = true
	blocked.IsCooldown = true
	blocked.ReadingFailed = true
	result := ProjectLendingAdvice([]LendingCardInput{
		blocked,
		lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+2*lendingTestDayMS)),
		lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+3*lendingTestDayMS)),
	}, lendingTestNow)
	card := lendingPoolCard(t, &result.Pools[0], "blocked")
	want := []string{LendingReasonNeedsReauth, LendingReasonCooldownActive, LendingReasonReadingFailed}
	if len(card.ReasonCodes) != len(want) {
		t.Fatalf("reasons = %v, want %v", card.ReasonCodes, want)
	}
	for index := range want {
		if card.ReasonCodes[index] != want[index] {
			t.Fatalf("reasons = %v, want %v", card.ReasonCodes, want)
		}
	}
	if card.PrimaryReason != LendingReasonNeedsReauth {
		t.Fatalf("primary = %q", card.PrimaryReason)
	}

	unsigned := lendingTestWeekly(70, lendingTestNow+2*lendingTestDayMS)
	unsigned.ResetAtMS = nil
	unsigned.RemainingPercent = nil
	unsigned.ResetLabel = "in a few days"
	stale := lendingTestCard("stale", "stale", unsigned)
	stale.ObservedAtMS = lendingTestNow - 25*60*60*1000
	second := ProjectLendingAdvice([]LendingCardInput{
		stale,
		lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+2*lendingTestDayMS)),
		lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+3*lendingTestDayMS)),
	}, lendingTestNow)
	card = lendingPoolCard(t, &second.Pools[0], "stale")
	if card.PrimaryReason != LendingReasonReadingStale {
		t.Fatalf("primary = %q, reasons = %v", card.PrimaryReason, card.ReasonCodes)
	}
}

func TestLendingReferenceAndValidUntil(t *testing.T) {
	window := lendingTestWeekly(70, lendingTestNow+3*lendingTestDayMS)
	window.ResetAccuracy = "derived"
	other := lendingTestWeekly(60, lendingTestNow+2*lendingTestDayMS)
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("a", "a", window),
		lendingTestCard("b", "b", other),
	}, lendingTestNow)
	pool := result.Pools[0]
	if !pool.IsReference {
		t.Fatal("derived refill should mark the pool as a reference")
	}
	if pool.ValidUntilMS == nil || *pool.ValidUntilMS != lendingTestNow+2*lendingTestDayMS {
		t.Fatalf("valid_until = %v, want the earliest board refill", pool.ValidUntilMS)
	}
}

func TestLendingPastRefillIsStale(t *testing.T) {
	past := lendingTestCard("past", "past", lendingTestWeekly(80, lendingTestNow-60*1000))
	exact := lendingTestCard("exact", "exact", lendingTestWeekly(80, lendingTestNow))
	result := ProjectLendingAdvice([]LendingCardInput{
		past,
		exact,
		lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+4*lendingTestDayMS)),
		lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+2*lendingTestDayMS)),
	}, lendingTestNow)
	pool := result.Pools[0]
	if pool.State != LendingStateAdvised {
		t.Fatalf("state = %s, want the two future cards on the board", pool.State)
	}
	for _, authIndex := range []string{"past", "exact"} {
		card := lendingPoolCard(t, &pool, authIndex)
		if card.Role != LendingRoleHold || !lendingHasReason(card.ReasonCodes, LendingReasonReadingStale) {
			t.Fatalf("card %s = %+v, want hold with %s", authIndex, card, LendingReasonReadingStale)
		}
		if card.PrimaryReason != LendingReasonReadingStale {
			t.Fatalf("card %s primary = %q", authIndex, card.PrimaryReason)
		}
	}
	if pool.ValidUntilMS == nil || *pool.ValidUntilMS != lendingTestNow+2*lendingTestDayMS {
		t.Fatalf("valid_until = %v, want the board's earliest refill", pool.ValidUntilMS)
	}
}

func TestLendingHoldCardListsEveryReason(t *testing.T) {
	stale := lendingTestCard("stale", "stale", lendingTestWeekly(80, lendingTestNow+4*lendingTestDayMS))
	stale.ObservedAtMS = lendingTestNow - 25*60*60*1000
	stale.IsCooldown = true
	stale.NeedsReauth = true
	result := ProjectLendingAdvice([]LendingCardInput{
		stale,
		lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+2*lendingTestDayMS)),
		lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+3*lendingTestDayMS)),
	}, lendingTestNow)
	pool := result.Pools[0]
	card := lendingPoolCard(t, &pool, "stale")
	if card.Role != LendingRoleHold {
		t.Fatalf("role = %s, want hold", card.Role)
	}
	want := []string{LendingReasonNeedsReauth, LendingReasonCooldownActive, LendingReasonReadingStale}
	if len(card.ReasonCodes) != len(want) {
		t.Fatalf("reasons = %v, want %v", card.ReasonCodes, want)
	}
	for index := range want {
		if card.ReasonCodes[index] != want[index] {
			t.Fatalf("reasons = %v, want %v", card.ReasonCodes, want)
		}
	}
	if card.PrimaryReason != LendingReasonNeedsReauth {
		t.Fatalf("primary = %q, want %q", card.PrimaryReason, LendingReasonNeedsReauth)
	}
}

func TestLendingSingleSeatDoesNotFormAPool(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("only", "only", lendingTestWeekly(80, lendingTestNow+3*lendingTestDayMS)),
	}, lendingTestNow)

	if len(result.Pools) != 0 || result.UnpooledCount != 1 {
		t.Fatalf("pools = %+v unpooled = %+v", result.Pools, result.Unpooled)
	}
	if reasons := lendingUnpooledReasons(t, result, "only"); !lendingHasReason(reasons, LendingReasonPoolTooSmall) {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestLendingUnknownPlanNeverPools(t *testing.T) {
	for _, planType := range []string{"", "unknown", "  UNKNOWN  "} {
		result := ProjectLendingAdvice([]LendingCardInput{
			{AuthIndex: "one", Name: "one", Provider: "codex", PlanType: planType, ObservedAtMS: lendingTestNow, Windows: []QuotaWindow{lendingTestWeekly(80, lendingTestNow+3*lendingTestDayMS)}},
			{AuthIndex: "two", Name: "two", Provider: "codex", PlanType: planType, ObservedAtMS: lendingTestNow, Windows: []QuotaWindow{lendingTestWeekly(70, lendingTestNow+3*lendingTestDayMS)}},
		}, lendingTestNow)
		if len(result.Pools) != 0 {
			t.Fatalf("plan_type %q formed a pool: %+v", planType, result.Pools)
		}
		for _, authIndex := range []string{"one", "two"} {
			if reasons := lendingUnpooledReasons(t, result, authIndex); !lendingHasReason(reasons, LendingReasonPlanTypeUnknown) {
				t.Fatalf("plan_type %q reasons = %v", planType, reasons)
			}
		}
	}
}

func TestLendingDisabledSeatIsExcluded(t *testing.T) {
	disabled := lendingTestCard("disabled", "disabled", lendingTestWeekly(90, lendingTestNow+3*lendingTestDayMS))
	disabled.IsDisabled = true
	result := ProjectLendingAdvice([]LendingCardInput{
		disabled,
		lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+3*lendingTestDayMS)),
		lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+2*lendingTestDayMS)),
	}, lendingTestNow)

	if len(result.Pools) != 1 {
		t.Fatalf("pools = %+v", result.Pools)
	}
	if reasons := lendingUnpooledReasons(t, result, "disabled"); !lendingHasReason(reasons, LendingReasonCredentialDisabled) {
		t.Fatalf("reasons = %v", reasons)
	}
	for _, card := range result.Pools[0].Cards {
		if card.AuthIndex == "disabled" {
			t.Fatalf("disabled credential must not appear in the pool: %+v", card)
		}
	}
}

func TestLendingServiceabilityReasons(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*LendingCardInput)
		reason string
	}{
		{name: "cooldown", mutate: func(card *LendingCardInput) { card.IsCooldown = true }, reason: LendingReasonCooldownActive},
		{name: "reauth", mutate: func(card *LendingCardInput) { card.NeedsReauth = true }, reason: LendingReasonNeedsReauth},
		{name: "reading failed", mutate: func(card *LendingCardInput) { card.ReadingFailed = true }, reason: LendingReasonReadingFailed},
		{name: "status exhausted", mutate: func(card *LendingCardInput) { card.Status = "exhausted" }, reason: LendingReasonWindowExhausted},
		{
			name: "short standard window exhausted",
			mutate: func(card *LendingCardInput) {
				card.Windows = append(card.Windows, lendingTestWindow(0, lendingTestNow+2*60*60*1000, "five_hour", 5))
			},
			reason: LendingReasonWindowExhausted,
		},
		{
			name: "below ten percent",
			mutate: func(card *LendingCardInput) {
				card.Windows = []QuotaWindow{lendingTestWeekly(9, lendingTestNow+2*lendingTestDayMS)}
			},
			reason: LendingReasonRemainingBelowTen,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			blocked := lendingTestCard("blocked", "blocked", lendingTestWeekly(80, lendingTestNow+4*lendingTestDayMS))
			testCase.mutate(&blocked)
			result := ProjectLendingAdvice([]LendingCardInput{
				blocked,
				lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+2*lendingTestDayMS)),
				lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+3*lendingTestDayMS)),
			}, lendingTestNow)
			pool := result.Pools[0]
			if pool.State != LendingStateAdvised {
				t.Fatalf("state = %s, want advised among the two serviceable seats", pool.State)
			}
			card := lendingPoolCard(t, &pool, "blocked")
			if card.Role != LendingRoleHold || !lendingHasReason(card.ReasonCodes, testCase.reason) {
				t.Fatalf("card = %+v, want hold with %s", card, testCase.reason)
			}
			if card.PrimaryReason != testCase.reason {
				t.Fatalf("primary = %q, want %q", card.PrimaryReason, testCase.reason)
			}
		})
	}
}

func TestLendingNoAdvicePoolUnionsHoldReasons(t *testing.T) {
	stale := lendingTestCard("stale", "stale", lendingTestWeekly(80, lendingTestNow+4*lendingTestDayMS))
	stale.ObservedAtMS = lendingTestNow - 25*60*60*1000
	stale.IsCooldown = true
	noLong := lendingTestCard("no-long", "no-long", lendingTestWindow(80, lendingTestNow+6*60*60*1000, "five_hour", 5))
	noLong.NeedsReauth = true
	result := ProjectLendingAdvice([]LendingCardInput{stale, noLong}, lendingTestNow)
	pool := result.Pools[0]
	if pool.State != LendingStateNoAdvice || pool.ValidUntilMS != nil {
		t.Fatalf("pool = %+v, want no_advice without a deadline", pool)
	}
	want := []string{
		LendingReasonNeedsReauth,
		LendingReasonCooldownActive,
		LendingReasonReadingStale,
		LendingReasonNoLongWindow,
	}
	if len(pool.ReasonCodes) != len(want) {
		t.Fatalf("pool reasons = %v, want %v", pool.ReasonCodes, want)
	}
	for index := range want {
		if pool.ReasonCodes[index] != want[index] {
			t.Fatalf("pool reasons = %v, want %v", pool.ReasonCodes, want)
		}
	}
}

func TestDailyAvailablePercentClampsUnderAnHour(t *testing.T) {
	days, daily := DailyAvailablePercent(50, lendingTestNow+20*60*1000, lendingTestNow)
	if math.Abs(days-1.0/24.0) > 1e-9 {
		t.Fatalf("days = %v, want one hour", days)
	}
	if daily != 1200 {
		t.Fatalf("daily = %v, want 1200", daily)
	}
}

func TestLendingSelectsTheLowestLongWindow(t *testing.T) {
	early := lendingTestWindow(40, lendingTestNow+2*lendingTestDayMS, "weekly", 168)
	late := lendingTestWindow(40, lendingTestNow+4*lendingTestDayMS, "monthly", 720)
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("a", "a", early, late),
		lendingTestCard("b", "b", lendingTestWeekly(20, lendingTestNow+3*lendingTestDayMS)),
	}, lendingTestNow)
	if got := lendingPoolCard(t, &result.Pools[0], "a").WindowID; got != "weekly" {
		t.Fatalf("selected window = %q, want the earlier-reset weekly window", got)
	}
}

func TestLongStandardWindow(t *testing.T) {
	period := func(value float64) *float64 { return &value }
	cases := []struct {
		name   string
		window QuotaWindow
		want   bool
	}{
		{name: "weekly standard", window: QuotaWindow{Scope: "standard", PeriodHours: period(168)}, want: true},
		{name: "daily standard", window: QuotaWindow{Scope: "standard", PeriodHours: period(24)}, want: true},
		{name: "five hour", window: QuotaWindow{Scope: "standard", PeriodHours: period(5)}, want: false},
		{name: "unknown period", window: QuotaWindow{Scope: "standard"}, want: false},
		{name: "model scoped", window: QuotaWindow{Scope: "model", PeriodHours: period(168)}, want: false},
		{name: "rolling", window: QuotaWindow{Scope: "standard", Kind: "rolling", PeriodHours: period(168)}, want: false},
		{name: "credit usage", window: QuotaWindow{Scope: "standard", Kind: "credit_usage", PeriodHours: period(168)}, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := LongStandardWindow(testCase.window); got != testCase.want {
				t.Fatalf("LongStandardWindow = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestLendingDisplayName(t *testing.T) {
	cases := []struct {
		name string
		card LendingCardInput
		want string
	}{
		{name: "email wins", card: LendingCardInput{Email: "ops@example.test", Name: "cred.json", AuthIndex: "auth-01"}, want: "ops@example.test"},
		{name: "project id when no email", card: LendingCardInput{ProjectID: "acme-project", Name: "cred.json", AuthIndex: "auth-01"}, want: "acme-project"},
		{name: "file name when no account", card: LendingCardInput{Name: "codex-primary.json", AuthIndex: "auth-01"}, want: "codex-primary.json"},
		{name: "auth index prefix is stripped", card: LendingCardInput{Email: "auth-01 ops@example.test", AuthIndex: "auth-01"}, want: "ops@example.test"},
		{name: "uuid prefix is stripped", card: LendingCardInput{Name: "0195e0aa-1234-4abc-8def-0123456789ab seat.json", AuthIndex: "auth-01"}, want: "seat.json"},
		{name: "empty account falls back to the file name", card: LendingCardInput{Email: "auth-01", Name: "seat.json", AuthIndex: "auth-01"}, want: "seat.json"},
		{name: "nothing but the index", card: LendingCardInput{AuthIndex: "auth-01"}, want: "auth-01"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := lendingDisplayName(testCase.card); got != testCase.want {
				t.Fatalf("display name = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestLendingOutsideCards(t *testing.T) {
	boardA := lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+4*lendingTestDayMS))
	boardA.Models = []string{"m1", "m2"}
	boardB := lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+2*lendingTestDayMS))
	boardB.Models = []string{"m1"}

	otherPool := lendingTestCard("other-pool", "other-pool", lendingTestWeekly(50, lendingTestNow+3*lendingTestDayMS))
	otherPool.PlanType = "max"
	otherPool.PlanLabel = "Max 20x"
	otherPool.Models = []string{"m1"}
	otherPool.Priority = 7
	otherPoolB := lendingTestCard("other-pool-b", "other-pool-b", lendingTestWeekly(45, lendingTestNow+4*lendingTestDayMS))
	otherPoolB.PlanType = "max"
	otherPoolB.PlanLabel = "Max 20x"
	otherPoolB.Models = []string{"m1"}
	otherPoolB.Priority = 6

	unpooled := lendingTestCard("unpooled", "unpooled", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	unpooled.PlanType = "unknown"
	unpooled.Models = []string{"m1"}

	otherModel := lendingTestCard("other-model", "other-model", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	otherModel.PlanType = "unknown"
	otherModel.Models = []string{"m3"}

	disabled := lendingTestCard("disabled", "disabled", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	disabled.PlanType = "unknown"
	disabled.Models = []string{"m1"}
	disabled.IsDisabled = true

	cooling := lendingTestCard("cooling", "cooling", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	cooling.PlanType = "unknown"
	cooling.Models = []string{"m1"}
	cooling.IsCooldown = true

	unavailable := lendingTestCard("unavailable", "unavailable", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	unavailable.PlanType = "unknown"
	unavailable.Models = []string{"m1"}
	unavailable.IsUnavailable = true

	noModels := lendingTestCard("no-models", "no-models", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	noModels.PlanType = "unknown"
	noModels.Models = nil

	lowOutside := lendingTestCard("low-outside", "low-outside", lendingTestWeekly(5, lendingTestNow+3*lendingTestDayMS))
	lowOutside.PlanType = "unknown"
	lowOutside.Models = []string{"m1"}

	// A held card of the advised pool stays in the pool's own list and is not repeated
	// in the outside roster. It is stale rather than cooling so the check isolates the
	// pool-membership exclusion.
	samePoolStale := lendingTestCard("same-pool-stale", "same-pool-stale", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	samePoolStale.ObservedAtMS = lendingTestNow - 25*60*60*1000
	samePoolStale.Models = []string{"m1"}

	reauth := lendingTestCard("reauth", "reauth", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	reauth.PlanType = "unknown"
	reauth.Models = []string{"m1"}
	reauth.NeedsReauth = true

	failed := lendingTestCard("failed", "failed", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	failed.PlanType = "unknown"
	failed.Models = []string{"m1"}
	failed.ReadingFailed = true

	exhausted := lendingTestCard("exhausted", "exhausted", lendingTestWeekly(40, lendingTestNow+3*lendingTestDayMS))
	exhausted.PlanType = "unknown"
	exhausted.Models = []string{"m1"}
	exhausted.Status = "exhausted"

	result := ProjectLendingAdvice([]LendingCardInput{
		boardA, boardB, samePoolStale, otherPool, otherPoolB, unpooled, otherModel, disabled, cooling, unavailable, noModels, lowOutside, reauth, failed, exhausted,
	}, lendingTestNow)
	pool := result.Pools[0]
	if len(pool.OutsideCards) != 4 {
		t.Fatalf("outside cards = %+v, want the two other-pool seats, the unpooled seat and the low-remaining seat", pool.OutsideCards)
	}
	if held := lendingPoolCard(t, &pool, "same-pool-stale"); held.Role != LendingRoleHold {
		t.Fatalf("same-pool-stale role = %s, want hold inside its own pool", held.Role)
	}
	byIndex := map[string]LendingOutsideCard{}
	for _, card := range pool.OutsideCards {
		byIndex[card.AuthIndex] = card
	}
	other := byIndex["other-pool"]
	if other.AuthIndex != "other-pool" || other.PoolID != "codex:max" || other.PoolLabel != "Max 20x" || other.Provider != "codex" || lendingDerefInt(other.Priority) != 7 {
		t.Fatalf("other pool card = %+v", other)
	}
	loose := byIndex["unpooled"]
	if loose.AuthIndex != "unpooled" || loose.PoolID != "" || loose.Provider != "codex" || !loose.PriorityKnown || !loose.PriorityIsDefault {
		t.Fatalf("unpooled card = %+v", loose)
	}
	if low := byIndex["low-outside"]; low.AuthIndex != "low-outside" || !low.PriorityKnown || !low.PriorityIsDefault {
		t.Fatalf("low-remaining card = %+v, want it listed despite staying under ten percent", low)
	}
	for _, absent := range []string{
		"other-model", "disabled", "cooling", "unavailable", "no-models",
		"reauth", "failed", "exhausted", "same-pool-stale", "a", "b",
	} {
		if _, ok := byIndex[absent]; ok {
			t.Fatalf("card %s should not be listed outside the pool", absent)
		}
	}

	// A board card without a model list cannot establish the shared model.
	boardB.Models = nil
	missingModels := ProjectLendingAdvice([]LendingCardInput{boardA, boardB, otherPool, otherPoolB}, lendingTestNow)
	if len(missingModels.Pools[0].OutsideCards) != 0 {
		t.Fatalf("a board without a model list produced outside cards: %+v", missingModels.Pools[0].OutsideCards)
	}

	// A board whose model lists do not overlap has no common model to warn about.
	boardA.Models = []string{"m1"}
	boardB.Models = []string{"m2"}
	disjoint := ProjectLendingAdvice([]LendingCardInput{boardA, boardB, otherPool, otherPoolB}, lendingTestNow)
	if len(disjoint.Pools[0].OutsideCards) != 0 {
		t.Fatalf("disjoint models produced outside cards: %+v", disjoint.Pools[0].OutsideCards)
	}
}

func TestLendingDetailMatchesOverview(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingWithPriority(lendingTestCard("codex-a", "codex-a", lendingTestWeekly(88, lendingTestNow+2*lendingTestDayMS)), 0),
		lendingWithPriority(lendingTestCard("codex-b", "codex-b", lendingTestWeekly(40, lendingTestNow+4*lendingTestDayMS)), 0),
	}, lendingTestNow)
	detail := result.DetailFor("codex-b")
	if detail.Pool == nil || len(detail.UnpooledReasonCodes) != 0 {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Pool.State != result.Pools[0].State || len(detail.Pool.Cards) != len(result.Pools[0].Cards) {
		t.Fatalf("detail pool = %+v, overview pool = %+v", detail.Pool, result.Pools[0])
	}
	for index, card := range detail.Pool.Cards {
		overviewCard := result.Pools[0].Cards[index]
		if card.AuthIndex != overviewCard.AuthIndex || card.Role != overviewCard.Role ||
			card.Tier != overviewCard.Tier ||
			lendingDerefInt(card.SuggestedPriority) != lendingDerefInt(overviewCard.SuggestedPriority) {
			t.Fatalf("card %d differs: detail=%+v overview=%+v", index, card, overviewCard)
		}
	}
	missing := result.DetailFor("absent")
	if missing.Pool != nil || missing.UnpooledReasonCodes == nil || len(missing.UnpooledReasonCodes) != 0 {
		t.Fatalf("absent detail = %+v", missing)
	}
	if pool, reasons := result.PoolFor("codex-a"); pool == nil || reasons != nil {
		t.Fatalf("PoolFor codex-a = %+v %v", pool, reasons)
	}
	if pool, reasons := result.PoolFor("absent"); pool != nil || reasons == nil {
		t.Fatalf("PoolFor absent = %+v %v", pool, reasons)
	}
}

func TestLendingIsDeterministic(t *testing.T) {
	cards := []LendingCardInput{
		lendingTestCard("b", "b", lendingTestWeekly(40, lendingTestNow+2*lendingTestDayMS)),
		lendingTestCard("a", "a", lendingTestWeekly(88, lendingTestNow+4*lendingTestDayMS)),
		lendingTestCard("c", "c", lendingTestWeekly(60, lendingTestNow+3*lendingTestDayMS)),
	}
	first := ProjectLendingAdvice(cards, lendingTestNow)
	second := ProjectLendingAdvice(cards, lendingTestNow)
	if len(first.Pools[0].Cards) != 3 {
		t.Fatalf("cards = %+v", first.Pools[0].Cards)
	}
	for index := range first.Pools[0].Cards {
		if first.Pools[0].Cards[index].AuthIndex != second.Pools[0].Cards[index].AuthIndex ||
			first.Pools[0].Cards[index].Tier != second.Pools[0].Cards[index].Tier {
			t.Fatalf("card %d is not stable", index)
		}
	}
	if first.Pools[0].Cards[0].AuthIndex != "a" || first.Pools[0].Cards[1].AuthIndex != "c" || first.Pools[0].Cards[2].AuthIndex != "b" {
		t.Fatalf("card order = %+v, want tier order a, c, b", first.Pools[0].Cards)
	}
}
