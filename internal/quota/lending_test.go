package quota

import (
	"math"
	"testing"
)

const lendingTestNow = int64(1_763_000_000_000)

const lendingDayMS = int64(24 * 60 * 60 * 1000)

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

func lendingTestCard(authIndex, name string, windows ...QuotaWindow) LendingCardInput {
	return LendingCardInput{
		AuthIndex:    authIndex,
		Name:         name,
		Provider:     "codex",
		PlanType:     "pro",
		PlanLabel:    "Pro 200",
		ObservedAtMS: lendingTestNow - lendingDayMS/24,
		Windows:      windows,
	}
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

func TestProjectLendingAdviceTwoSeats(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("codex-a", "codex-a", lendingTestWeekly(88, lendingTestNow+int64(3.9*float64(lendingDayMS)))),
		lendingTestCard("codex-b", "codex-b", lendingTestWeekly(40, lendingTestNow+int64(2.2727*float64(lendingDayMS)))),
	}, lendingTestNow)

	if result.UnpooledCount != 0 || len(result.Pools) != 1 {
		t.Fatalf("pools = %+v, unpooled = %+v", result.Pools, result.Unpooled)
	}
	pool := result.Pools[0]
	if pool.ID != "codex:pro" || pool.State != LendingStateAdvised || pool.WindowKind != "weekly" {
		t.Fatalf("pool = %+v", pool)
	}
	prefer := lendingPoolCard(t, &pool, "codex-a")
	avoid := lendingPoolCard(t, &pool, "codex-b")
	if prefer.Role != LendingRolePrefer || avoid.Role != LendingRoleAvoid {
		t.Fatalf("roles = %s/%s", prefer.Role, avoid.Role)
	}
	if prefer.DaysToReset == nil || prefer.DailyAvailablePercent == nil || math.Abs(*prefer.DailyAvailablePercent-22.6) > 0.001 {
		t.Fatalf("prefer numbers = %+v", prefer)
	}
	if avoid.DaysToReset == nil || math.Abs(*avoid.DaysToReset-2.2727) > 0.001 || avoid.DailyAvailablePercent == nil || math.Abs(*avoid.DailyAvailablePercent-17.6) > 0.001 {
		t.Fatalf("avoid numbers = %+v", avoid)
	}
	if pool.ValidUntilMS == nil || *pool.ValidUntilMS != lendingTestNow+int64(2.2727*float64(lendingDayMS)) {
		t.Fatalf("valid_until = %v, want the earliest reset", pool.ValidUntilMS)
	}
	if !prefer.IsComparable || pool.IsReference {
		t.Fatalf("comparability = %+v reference=%t", prefer, pool.IsReference)
	}
}

func TestProjectLendingAdviceFastResetWithHighRemainingPrefersTheFastSeat(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("slow", "slow", lendingTestWeekly(30, lendingTestNow+4*lendingDayMS)),
		lendingTestCard("fast", "fast", lendingTestWeekly(95, lendingTestNow+lendingDayMS)),
	}, lendingTestNow)

	pool := result.Pools[0]
	if pool.State != LendingStateAdvised {
		t.Fatalf("state = %s", pool.State)
	}
	if card := lendingPoolCard(t, &pool, "fast"); card.Role != LendingRolePrefer {
		t.Fatalf("fast role = %s, want prefer", card.Role)
	}
	if card := lendingPoolCard(t, &pool, "slow"); card.Role != LendingRoleAvoid {
		t.Fatalf("slow role = %s, want avoid", card.Role)
	}
}

func TestProjectLendingAdviceLowRemainingSeatIsLimited(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("low", "low", lendingTestWeekly(5, lendingTestNow+30*lendingDayMS)),
		lendingTestCard("ok", "ok", lendingTestWeekly(80, lendingTestNow+2*lendingDayMS)),
	}, lendingTestNow)

	pool := result.Pools[0]
	if pool.State != LendingStateLimited {
		t.Fatalf("state = %s, want limited", pool.State)
	}
	for _, card := range pool.Cards {
		if card.Role == LendingRolePrefer || card.Role == LendingRoleAvoid {
			t.Fatalf("limited pool must not rank: %+v", card)
		}
	}
	low := lendingPoolCard(t, &pool, "low")
	if low.Role != LendingRoleLimited || !lendingHasReason(low.ReasonCodes, LendingReasonRemainingBelowTen) {
		t.Fatalf("low card = %+v", low)
	}
	// A limited pool names only the seats that cannot serve. The healthy seat is
	// still serviceable, so it keeps the neutral role and carries no reason.
	ok := lendingPoolCard(t, &pool, "ok")
	if ok.Role != LendingRoleNormal {
		t.Fatalf("ok card role = %s, want normal: %+v", ok.Role, ok)
	}
	if len(ok.ReasonCodes) != 0 {
		t.Fatalf("ok card must carry no serviceability reason: %v", ok.ReasonCodes)
	}
}

func TestProjectLendingAdviceMissingResetTimeBlocksThePool(t *testing.T) {
	unsigned := lendingTestWeekly(70, lendingTestNow+2*lendingDayMS)
	unsigned.ResetAtMS = nil
	unsigned.ResetLabel = "in a few days"
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("unsigned", "unsigned", unsigned),
		lendingTestCard("signed", "signed", lendingTestWeekly(60, lendingTestNow+3*lendingDayMS)),
	}, lendingTestNow)

	pool := result.Pools[0]
	if pool.State != LendingStateUnavailable {
		t.Fatalf("state = %s, want unavailable", pool.State)
	}
	card := lendingPoolCard(t, &pool, "unsigned")
	if card.IsComparable || !lendingHasReason(card.ReasonCodes, LendingReasonResetTimeUnknown) {
		t.Fatalf("card = %+v", card)
	}
	if card.Role != LendingRoleUnavailable || card.DaysToReset != nil || card.DailyAvailablePercent != nil {
		t.Fatalf("unavailable card must not carry a ranking: %+v", card)
	}
	if !lendingHasReason(pool.ReasonCodes, LendingReasonResetTimeUnknown) {
		t.Fatalf("pool reasons = %v", pool.ReasonCodes)
	}
	// The pool cannot compare its seats, so it must not borrow the one dated reset
	// as an advice deadline.
	if pool.ValidUntilMS != nil {
		t.Fatalf("unavailable pool declared valid_until = %d", *pool.ValidUntilMS)
	}
}

func TestProjectLendingAdviceStaleReadingBlocksThePool(t *testing.T) {
	stale := lendingTestCard("stale", "stale", lendingTestWeekly(90, lendingTestNow+3*lendingDayMS))
	stale.ObservedAtMS = lendingTestNow - 25*60*60*1000
	result := ProjectLendingAdvice([]LendingCardInput{
		stale,
		lendingTestCard("fresh", "fresh", lendingTestWeekly(50, lendingTestNow+2*lendingDayMS)),
	}, lendingTestNow)

	pool := result.Pools[0]
	if pool.State != LendingStateUnavailable {
		t.Fatalf("state = %s, want unavailable", pool.State)
	}
	card := lendingPoolCard(t, &pool, "stale")
	if card.IsComparable || !lendingHasReason(card.ReasonCodes, LendingReasonReadingStale) {
		t.Fatalf("card = %+v", card)
	}
	if card.FreshUntilMS == nil || *card.FreshUntilMS >= lendingTestNow {
		t.Fatalf("fresh_until = %v, want a deadline before the clock", card.FreshUntilMS)
	}
}

func TestProjectLendingAdviceWindowTypeMismatch(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("weekly", "weekly", lendingTestWeekly(70, lendingTestNow+3*lendingDayMS)),
		lendingTestCard("daily", "daily", lendingTestWindow(60, lendingTestNow+lendingDayMS, "daily", 24)),
	}, lendingTestNow)

	pool := result.Pools[0]
	if pool.State != LendingStateUnavailable || pool.WindowKind != "" {
		t.Fatalf("pool = %+v", pool)
	}
	if !lendingHasReason(pool.ReasonCodes, LendingReasonWindowTypeMismatch) {
		t.Fatalf("pool reasons = %v", pool.ReasonCodes)
	}
	for _, authIndex := range []string{"weekly", "daily"} {
		card := lendingPoolCard(t, &pool, authIndex)
		if card.WindowKind == "" || !lendingHasReason(card.ReasonCodes, LendingReasonWindowTypeMismatch) {
			t.Fatalf("card %s must name its own window and the mismatch: %+v", authIndex, card)
		}
	}
}

func TestProjectLendingAdviceWindowTypeMismatchSurvivesAnUnreadableCard(t *testing.T) {
	stale := lendingTestCard("stale", "stale", lendingTestWeekly(80, lendingTestNow+3*lendingDayMS))
	stale.ObservedAtMS = lendingTestNow - 25*60*60*1000
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("weekly", "weekly", lendingTestWeekly(70, lendingTestNow+3*lendingDayMS)),
		lendingTestCard("daily", "daily", lendingTestWindow(60, lendingTestNow+lendingDayMS, "daily", 24)),
		stale,
	}, lendingTestNow)

	pool := result.Pools[0]
	if pool.State != LendingStateUnavailable {
		t.Fatalf("state = %s, want unavailable", pool.State)
	}
	if !lendingHasReason(pool.ReasonCodes, LendingReasonWindowTypeMismatch) || !lendingHasReason(pool.ReasonCodes, LendingReasonReadingStale) {
		t.Fatalf("pool reasons = %v", pool.ReasonCodes)
	}
	for _, card := range pool.Cards {
		if !lendingHasReason(card.ReasonCodes, LendingReasonWindowTypeMismatch) {
			t.Fatalf("card %s does not name the mismatch: %+v", card.AuthIndex, card)
		}
	}
}

func TestProjectLendingAdviceSingleSeatDoesNotFormAPool(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("only", "only", lendingTestWeekly(80, lendingTestNow+3*lendingDayMS)),
	}, lendingTestNow)

	if len(result.Pools) != 0 || result.UnpooledCount != 1 {
		t.Fatalf("pools = %+v unpooled = %+v", result.Pools, result.Unpooled)
	}
	if reasons := lendingUnpooledReasons(t, result, "only"); !lendingHasReason(reasons, LendingReasonPoolTooSmall) {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestProjectLendingAdviceUnknownPlanNeverPools(t *testing.T) {
	for _, planType := range []string{"", "unknown", "  UNKNOWN  "} {
		result := ProjectLendingAdvice([]LendingCardInput{
			{AuthIndex: "one", Name: "one", Provider: "codex", PlanType: planType, ObservedAtMS: lendingTestNow, Windows: []QuotaWindow{lendingTestWeekly(80, lendingTestNow+3*lendingDayMS)}},
			{AuthIndex: "two", Name: "two", Provider: "codex", PlanType: planType, ObservedAtMS: lendingTestNow, Windows: []QuotaWindow{lendingTestWeekly(70, lendingTestNow+3*lendingDayMS)}},
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

func TestProjectLendingAdviceDisabledSeatIsExcluded(t *testing.T) {
	disabled := lendingTestCard("disabled", "disabled", lendingTestWeekly(90, lendingTestNow+3*lendingDayMS))
	disabled.IsDisabled = true
	result := ProjectLendingAdvice([]LendingCardInput{
		disabled,
		lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+3*lendingDayMS)),
		lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+2*lendingDayMS)),
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

func TestProjectLendingAdviceServiceabilityReasons(t *testing.T) {
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
				card.Windows = []QuotaWindow{lendingTestWeekly(9, lendingTestNow+2*lendingDayMS)}
			},
			reason: LendingReasonRemainingBelowTen,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			blocked := lendingTestCard("blocked", "blocked", lendingTestWeekly(80, lendingTestNow+4*lendingDayMS))
			testCase.mutate(&blocked)
			result := ProjectLendingAdvice([]LendingCardInput{
				blocked,
				lendingTestCard("a", "a", lendingTestWeekly(70, lendingTestNow+2*lendingDayMS)),
				lendingTestCard("b", "b", lendingTestWeekly(60, lendingTestNow+3*lendingDayMS)),
			}, lendingTestNow)
			pool := result.Pools[0]
			if pool.State != LendingStateAdvised {
				t.Fatalf("state = %s, want advised among the two serviceable seats", pool.State)
			}
			card := lendingPoolCard(t, &pool, "blocked")
			if card.Role != LendingRoleLimited || !lendingHasReason(card.ReasonCodes, testCase.reason) {
				t.Fatalf("card = %+v, want limited with %s", card, testCase.reason)
			}
		})
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

func TestProjectLendingAdviceBalanceThreshold(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		leftDaily  float64
		rightDaily float64
		state      string
	}{
		{name: "one point splits", leftDaily: 15, rightDaily: 14, state: LendingStateAdvised},
		{name: "under one point is balanced", leftDaily: 14.9, rightDaily: 14, state: LendingStateBalanced},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := ProjectLendingAdvice([]LendingCardInput{
				lendingTestCard("left", "left", lendingTestWeekly(testCase.leftDaily, lendingTestNow+lendingDayMS)),
				lendingTestCard("right", "right", lendingTestWeekly(testCase.rightDaily, lendingTestNow+lendingDayMS)),
			}, lendingTestNow)
			pool := result.Pools[0]
			if pool.State != testCase.state {
				t.Fatalf("state = %s, want %s", pool.State, testCase.state)
			}
			for _, card := range pool.Cards {
				if testCase.state == LendingStateBalanced && card.Role != LendingRoleBalanced {
					t.Fatalf("balanced pool card = %+v", card)
				}
			}
		})
	}
}

func TestProjectLendingAdviceReferencePrecision(t *testing.T) {
	for _, accuracy := range []string{"derived", "approximate", "exact"} {
		window := lendingTestWeekly(70, lendingTestNow+3*lendingDayMS)
		window.ResetAccuracy = accuracy
		other := lendingTestWeekly(60, lendingTestNow+2*lendingDayMS)
		result := ProjectLendingAdvice([]LendingCardInput{
			lendingTestCard("a", "a", window),
			lendingTestCard("b", "b", other),
		}, lendingTestNow)
		if result.Pools[0].IsReference != (accuracy != "exact") {
			t.Fatalf("accuracy %q -> is_reference=%t", accuracy, result.Pools[0].IsReference)
		}
	}
}

func TestProjectLendingAdviceSelectsTheLowestLongWindow(t *testing.T) {
	early := lendingTestWindow(40, lendingTestNow+2*lendingDayMS, "weekly", 168)
	late := lendingTestWindow(40, lendingTestNow+4*lendingDayMS, "monthly", 720)
	card := lendingTestCard("a", "a", early, late)
	result := ProjectLendingAdvice([]LendingCardInput{
		card,
		lendingTestCard("b", "b", lendingTestWeekly(20, lendingTestNow+3*lendingDayMS)),
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

func TestProjectLendingAdviceDetailMatchesOverview(t *testing.T) {
	result := ProjectLendingAdvice([]LendingCardInput{
		lendingTestCard("codex-a", "codex-a", lendingTestWeekly(88, lendingTestNow+4*lendingDayMS)),
		lendingTestCard("codex-b", "codex-b", lendingTestWeekly(40, lendingTestNow+2*lendingDayMS)),
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
			derefFloat(card.DailyAvailablePercent) != derefFloat(overviewCard.DailyAvailablePercent) {
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

func TestProjectLendingAdviceIsDeterministic(t *testing.T) {
	cards := []LendingCardInput{
		lendingTestCard("b", "b", lendingTestWeekly(40, lendingTestNow+2*lendingDayMS)),
		lendingTestCard("a", "a", lendingTestWeekly(88, lendingTestNow+4*lendingDayMS)),
		lendingTestCard("c", "c", lendingTestWeekly(60, lendingTestNow+3*lendingDayMS)),
	}
	first := ProjectLendingAdvice(cards, lendingTestNow)
	second := ProjectLendingAdvice(cards, lendingTestNow)
	if len(first.Pools[0].Cards) != 3 {
		t.Fatalf("cards = %+v", first.Pools[0].Cards)
	}
	if first.Pools[0].Cards[0].AuthIndex != second.Pools[0].Cards[0].AuthIndex {
		t.Fatalf("card order is not stable")
	}
	roles := []string{first.Pools[0].Cards[0].Role, first.Pools[0].Cards[1].Role, first.Pools[0].Cards[2].Role}
	if roles[0] != LendingRolePrefer || roles[len(roles)-1] != LendingRoleAvoid {
		t.Fatalf("roles = %v", roles)
	}
}

func derefFloat(value *float64) float64 {
	if value == nil {
		return math.NaN()
	}
	return *value
}
