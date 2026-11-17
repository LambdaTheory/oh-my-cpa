package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

// Command Code serves its account surface on the provider authority, outside
// the OpenAI-compatible surface the credential's base-url points at. These are
// the two documents the vendor CLI reads to draw its own usage bars, and the
// vendor's CPA plugin reads them too; the plugin does not implement
// quota.fetch, so OMC reads them directly through api-call instead.
const (
	CommandCodeBillingPrefix    = "https://api.commandcode.ai/alpha/billing/"
	CommandCodeCreditsURL       = CommandCodeBillingPrefix + "credits"
	CommandCodeSubscriptionsURL = CommandCodeBillingPrefix + "subscriptions"
)

// commandCodePlanAllowances is the vendor CLI's own plan table (planId ->
// monthly credit allowance), mirrored from
// mczhoucn/commandcode-go-cliproxyapi@v0.1.4 internal/plugin/quota.go. It is
// metadata for the monthly denominator only: Command Code reports the
// REMAINING monthly credits, never the granted allowance, so a plan outside
// this table has no denominator and must not be given a guessed one.
var commandCodePlanAllowances = map[string]struct {
	name    string
	credits float64
}{
	"individual-goat":     {"GOAT", 70},
	"individual-pro":      {"Pro", 30},
	"individual-pro-v1":   {"Pro", 80},
	"individual-provider": {"Provider", 15},
	"individual-max":      {"Max", 150},
	"individual-ultra":    {"Ultra", 300},
	"teams-pro":           {"Teams Pro", 40},
}

// commandCodePlanFor resolves a planId to the vendor's display name and
// allowance. Only an exact table key matches: the vendor has not published a
// suffix contract (an annual or revision suffix is a different plan), and a
// prefix guess would give a new plan an unconfirmed denominator. An unknown
// plan keeps its remaining credits but no percentage.
func commandCodePlanFor(planID string) (string, float64) {
	id := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(planID, "_", "-")))
	if id == "" {
		return "", 0
	}
	if plan, ok := commandCodePlanAllowances[id]; ok {
		return plan.name, plan.credits
	}
	return "", 0
}

type rawCommandCodeWindow struct {
	Used     *float64 `json:"used"`
	Cap      *float64 `json:"cap"`
	Exceeded bool     `json:"exceeded"`
	// ResetAt is Unix milliseconds.
	ResetAt *int64 `json:"resetAt"`
}

type rawCommandCodeCredits struct {
	Credits struct {
		MonthlyCredits   *float64 `json:"monthlyCredits"`
		PurchasedCredits *float64 `json:"purchasedCredits"`
	} `json:"credits"`
	WindowLimits struct {
		// Limited is the account-wide switch; a false value means the vendor
		// does not meter these windows.
		Limited  *bool                `json:"limited"`
		FiveHour rawCommandCodeWindow `json:"fiveHour"`
		Weekly   rawCommandCodeWindow `json:"weekly"`
	} `json:"windowLimits"`
}

type rawCommandCodeSubscription struct {
	Success bool `json:"success"`
	Data    struct {
		PlanID           string `json:"planId"`
		Status           string `json:"status"`
		CurrentPeriodEnd string `json:"currentPeriodEnd"`
	} `json:"data"`
}

// ParseCommandCodeQuota projects the billing documents onto the shared window
// shape. The subscription document is best effort: a missing or malformed one
// only drops the monthly window's plan and reset, never the two rate-limit
// windows the credits document carries.
func ParseCommandCodeQuota(creditsRaw, subscriptionRaw []byte, nowMS int64) (*QuotaPlan, []QuotaWindow, error) {
	if !strings.HasPrefix(strings.TrimSpace(string(creditsRaw)), "{") {
		return nil, nil, errors.New("invalid Command Code credits payload")
	}
	var credits rawCommandCodeCredits
	if err := json.Unmarshal(creditsRaw, &credits); err != nil {
		return nil, nil, fmt.Errorf("invalid Command Code credits payload: %w", err)
	}

	limited := credits.WindowLimits.Limited
	windows := make([]QuotaWindow, 0, 3)
	if window, ok := commandCodeRateLimitWindow("five_hour", "5小时限制 (5-Hour)", "five_hour", 5, credits.WindowLimits.FiveHour, limited, nowMS); ok {
		windows = append(windows, window)
	}
	if window, ok := commandCodeRateLimitWindow("weekly", "每周限制 (Weekly)", "weekly", 168, credits.WindowLimits.Weekly, limited, nowMS); ok {
		windows = append(windows, window)
	}

	subscription := rawCommandCodeSubscription{}
	if len(subscriptionRaw) > 0 && strings.HasPrefix(strings.TrimSpace(string(subscriptionRaw)), "{") {
		if err := json.Unmarshal(subscriptionRaw, &subscription); err != nil || !subscription.Success {
			subscription = rawCommandCodeSubscription{}
		}
	}
	planID := ""
	if subscription.Success {
		planID = strings.TrimSpace(subscription.Data.PlanID)
	}
	planName, allowance := commandCodePlanFor(planID)
	plan := commandCodeQuotaPlan(planID, planName, credits.Credits.MonthlyCredits)
	if window := commandCodeMonthlyWindow(planID, planName, allowance, credits.Credits.MonthlyCredits, credits.Credits.PurchasedCredits, subscription, nowMS); window != nil {
		windows = append(windows, *window)
	}
	return plan, windows, nil
}

// commandCodeRateLimitWindow converts one windowLimits entry. A window whose
// fields are all absent is omitted rather than published as an empty reading.
func commandCodeRateLimitWindow(id, label, kind string, periodHours float64, raw rawCommandCodeWindow, limited *bool, nowMS int64) (QuotaWindow, bool) {
	if raw.Used == nil && raw.Cap == nil && raw.ResetAt == nil && !raw.Exceeded {
		return QuotaWindow{}, false
	}
	window := QuotaWindow{
		ID:          id,
		Label:       label,
		Kind:        kind,
		Scope:       "standard",
		PeriodHours: floatPointer(periodHours),
	}
	notes := make([]string, 0, 2)
	if raw.Used != nil {
		window.Used = floatPointer(*raw.Used)
	}
	if raw.Cap != nil {
		window.Limit = floatPointer(*raw.Cap)
	}
	if raw.Used != nil && raw.Cap != nil && *raw.Cap > 0 {
		usedPercent := clamp(*raw.Used / *raw.Cap * 100, 0, 100)
		window.UsedPercent = floatPointer(usedPercent)
		window.RemainingPercent = floatPointer(100 - usedPercent)
	} else if raw.Used != nil {
		// Without a denominator the remaining share is unknowable; the plugin's
		// own card omits the percentage in this case too.
		notes = append(notes, "上游未给出该窗口上限")
	}
	if limited != nil && !*limited {
		notes = append(notes, "上游标记为不限量")
	}
	if raw.Exceeded {
		window.UsedPercent = floatPointer(100)
		window.RemainingPercent = floatPointer(0)
		notes = append(notes, "上游报告该窗口已超额")
	}
	if raw.ResetAt != nil && *raw.ResetAt > 0 {
		resetAtMS := *raw.ResetAt
		window.ResetAtMS = &resetAtMS
		window.ResetLabel = formatResetInstant(resetAtMS, nowMS)
		window.ResetAccuracy = "exact"
	}
	window.Note = strings.Join(notes, "；")
	return window, true
}

// commandCodeMonthlyWindow renders the monthly credit allowance. Command Code
// reports only the REMAINING monthly credits, so the used amount is derived the
// way the vendor CLI derives its own usage bar: pool =
// max(allowance, remaining) + purchased, used = pool - (remaining + purchased).
// A plan outside the vendor table has no denominator; its window states the
// remaining credits in the note instead of inventing a percentage.
func commandCodeMonthlyWindow(planID, planName string, allowance float64, monthlyCredits, purchasedCredits *float64, subscription rawCommandCodeSubscription, nowMS int64) *QuotaWindow {
	if monthlyCredits == nil {
		return nil
	}
	monthly := *monthlyCredits
	purchased := 0.0
	if purchasedCredits != nil {
		purchased = *purchasedCredits
	}
	window := QuotaWindow{
		ID:          "monthly",
		Label:       "每月额度 (Monthly)",
		Kind:        "monthly",
		Scope:       "standard",
		PeriodHours: floatPointer(720),
	}
	if allowance > 0 {
		pool := math.Max(allowance, monthly) + purchased
		used := math.Max(pool-(monthly+purchased), 0)
		window.Used = floatPointer(used)
		window.Limit = floatPointer(pool)
		if pool > 0 {
			usedPercent := clamp(used/pool*100, 0, 100)
			window.UsedPercent = floatPointer(usedPercent)
			window.RemainingPercent = floatPointer(100 - usedPercent)
		}
		// The vendor reports remaining credits, not the granted allowance; the
		// denominator is mirrored from the vendor CLI's plan table. Say so, so
		// the window is not read as an upstream-exact cap.
		window.Note = fmt.Sprintf("分母来自厂商套餐表（%s），非上游 API 精确值", planName)
	} else {
		name := planID
		if name == "" {
			name = "未知套餐"
		}
		window.Note = fmt.Sprintf("套餐 %s 不在厂商额度表内，无法计算已用比例；剩余 %s credits", name, formatCommandCodeCredits(monthly))
	}
	if endMS, ok := parseCommandCodePeriodEnd(subscription.Data.CurrentPeriodEnd); ok {
		window.ResetAtMS = &endMS
		window.ResetLabel = formatResetInstant(endMS, nowMS)
		window.ResetAccuracy = "exact"
	}
	return &window
}

func commandCodeQuotaPlan(planID, planName string, monthlyCredits *float64) *QuotaPlan {
	planType := strings.TrimSpace(planID)
	if planType == "" {
		planType = "unknown"
	}
	label := planName
	if label == "" {
		label = planType
		if label == "unknown" {
			label = "Command Code"
		}
	}
	tier := "unknown"
	if planName != "" {
		tier = "standard"
	}
	plan := &QuotaPlan{PlanType: planType, PlanLabel: label, Tier: tier}
	if monthlyCredits != nil {
		// Command Code credits are an allowance unit, not dollars. The unit
		// keeps the balance out of monetary projections (model-group sums) and
		// remains the text the quota page shows as "Credit balance".
		plan.Credits = &QuotaCredits{Balance: fmt.Sprintf("%s credits", formatCommandCodeCredits(*monthlyCredits))}
	}
	return plan
}

func formatCommandCodeCredits(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// parseCommandCodePeriodEnd reads the subscription's currentPeriodEnd. The
// vendor sends an ISO timestamp; an unparseable value is dropped rather than
// invented as a reset.
func parseCommandCodePeriodEnd(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if instant, err := time.Parse(layout, raw); err == nil {
			return instant.UnixMilli(), true
		}
	}
	return 0, false
}

// fetchCommandCodeQuota reads the two billing documents through CPA's api-call
// with the credential under observation. The subscription read is supplemental:
// its failure must not discard the rate-limit windows the credits read already
// returned, and a 429 there must not park a credential whose windows are fine.
func (s *Service) fetchCommandCodeQuota(ctx context.Context, file management.AuthFile, nowMS int64) (*QuotaPlan, []QuotaWindow, error) {
	headers := management.WithQuotaCredential(map[string]string{
		"Accept": "application/json",
	})
	creditsResp, err := s.SafeApiCall(ctx, file.AuthIndex, http.MethodGet, CommandCodeCreditsURL, headers, "")
	if err != nil {
		return nil, nil, err
	}
	creditsBody, err := creditsResp.NormalizedBody()
	if err != nil {
		return nil, nil, err
	}
	var subscriptionBody []byte
	if response, subErr := s.SafeApiCall(ctx, file.AuthIndex, http.MethodGet, CommandCodeSubscriptionsURL, headers, ""); subErr == nil {
		if body, bodyErr := response.NormalizedBody(); bodyErr == nil {
			subscriptionBody = body
		}
	}
	return ParseCommandCodeQuota(creditsBody, subscriptionBody, nowMS)
}
