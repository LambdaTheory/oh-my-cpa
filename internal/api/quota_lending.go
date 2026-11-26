package api

import (
	"math"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// Handwritten allowlists for the cross-credential lending advice. The projection in
// quota is the only place the advice is computed; these types decide which of its
// fields a console response may carry, so a domain field cannot reach the wire by
// being added to a struct.

type lendingOverviewDTO struct {
	GeneratedAtMS int64                `json:"generated_at_ms"`
	Pools         []lendingPoolDTO     `json:"pools"`
	UnpooledCount int                  `json:"unpooled_count"`
	Unpooled      []lendingUnpooledDTO `json:"unpooled"`
}

type lendingDetailDTO struct {
	GeneratedAtMS       int64           `json:"generated_at_ms"`
	Pool                *lendingPoolDTO `json:"pool"`
	UnpooledReasonCodes []string        `json:"unpooled_reason_codes"`
}

type lendingPoolDTO struct {
	ID               string                  `json:"id"`
	Provider         string                  `json:"provider"`
	PlanType         string                  `json:"plan_type"`
	PlanLabel        string                  `json:"plan_label,omitempty"`
	WindowKind       string                  `json:"window_kind,omitempty"`
	State            string                  `json:"state"`
	ValidUntilMS     *int64                  `json:"valid_until_ms,omitempty"`
	ObservedAtMS     *int64                  `json:"observed_at_ms,omitempty"`
	FreshUntilMS     *int64                  `json:"fresh_until_ms,omitempty"`
	IsReference      bool                    `json:"is_reference"`
	IsBalanced       bool                    `json:"is_balanced"`
	NeedsChangeCount int                     `json:"needs_change_count"`
	HoldCount        int                     `json:"hold_count"`
	ReasonCodes      []string                `json:"reason_codes"`
	Cards            []lendingCardDTO        `json:"cards"`
	OutsideCards     []lendingOutsideCardDTO `json:"outside_cards"`
}

type lendingCardDTO struct {
	AuthIndex             string   `json:"auth_index"`
	Name                  string   `json:"name"`
	DisplayName           string   `json:"display_name"`
	Role                  string   `json:"role"`
	Tier                  int      `json:"tier,omitempty"`
	WindowID              string   `json:"window_id,omitempty"`
	WindowKind            string   `json:"window_kind,omitempty"`
	RemainingPercent      *float64 `json:"remaining_percent,omitempty"`
	ResetAtMS             *int64   `json:"reset_at_ms,omitempty"`
	ResetAccuracy         string   `json:"reset_accuracy,omitempty"`
	ObservedAtMS          *int64   `json:"observed_at_ms,omitempty"`
	FreshUntilMS          *int64   `json:"fresh_until_ms,omitempty"`
	DaysToReset           *float64 `json:"days_to_reset,omitempty"`
	DailyAvailablePercent *float64 `json:"daily_available_percent,omitempty"`
	IsComparable          bool     `json:"is_comparable"`
	Priority              *int     `json:"priority,omitempty"`
	PriorityKnown         bool     `json:"priority_known"`
	PriorityIsDefault     bool     `json:"priority_is_default"`
	CanEditPriority       bool     `json:"can_edit_priority"`
	SuggestedPriority     *int     `json:"suggested_priority,omitempty"`
	NeedsChange           *bool    `json:"needs_change,omitempty"`
	PrimaryReason         string   `json:"primary_reason,omitempty"`
	ReasonCodes           []string `json:"reason_codes"`
}

type lendingOutsideCardDTO struct {
	AuthIndex         string `json:"auth_index"`
	Name              string `json:"name"`
	DisplayName       string `json:"display_name"`
	Provider          string `json:"provider"`
	PoolID            string `json:"pool_id,omitempty"`
	PoolLabel         string `json:"pool_label,omitempty"`
	Priority          *int   `json:"priority,omitempty"`
	PriorityKnown     bool   `json:"priority_known"`
	PriorityIsDefault bool   `json:"priority_is_default"`
}

type lendingUnpooledDTO struct {
	AuthIndex   string   `json:"auth_index"`
	Name        string   `json:"name"`
	ReasonCodes []string `json:"reason_codes"`
}

func projectLendingOverview(result quota.LendingResult) lendingOverviewDTO {
	dto := lendingOverviewDTO{
		GeneratedAtMS: result.GeneratedAtMS,
		Pools:         make([]lendingPoolDTO, 0, len(result.Pools)),
		UnpooledCount: result.UnpooledCount,
		Unpooled:      make([]lendingUnpooledDTO, 0, len(result.Unpooled)),
	}
	for _, pool := range result.Pools {
		dto.Pools = append(dto.Pools, projectLendingPool(pool))
	}
	for _, entry := range result.Unpooled {
		dto.Unpooled = append(dto.Unpooled, lendingUnpooledDTO{
			AuthIndex:   entry.AuthIndex,
			Name:        entry.Name,
			ReasonCodes: lendingStrings(entry.ReasonCodes),
		})
	}
	return dto
}

func projectLendingDetail(detail quota.LendingDetail) lendingDetailDTO {
	dto := lendingDetailDTO{
		GeneratedAtMS:       detail.GeneratedAtMS,
		UnpooledReasonCodes: lendingStrings(detail.UnpooledReasonCodes),
	}
	if detail.Pool != nil {
		pool := projectLendingPool(*detail.Pool)
		dto.Pool = &pool
	}
	return dto
}

func projectLendingPool(pool quota.LendingPool) lendingPoolDTO {
	dto := lendingPoolDTO{
		ID:               pool.ID,
		Provider:         pool.Provider,
		PlanType:         pool.PlanType,
		PlanLabel:        pool.PlanLabel,
		WindowKind:       pool.WindowKind,
		State:            pool.State,
		ValidUntilMS:     lendingInt64Value(pool.ValidUntilMS),
		ObservedAtMS:     lendingInt64Value(pool.ObservedAtMS),
		FreshUntilMS:     lendingInt64Value(pool.FreshUntilMS),
		IsReference:      pool.IsReference,
		IsBalanced:       pool.IsBalanced,
		NeedsChangeCount: pool.NeedsChangeCount,
		HoldCount:        pool.HoldCount,
		ReasonCodes:      lendingStrings(pool.ReasonCodes),
		Cards:            make([]lendingCardDTO, 0, len(pool.Cards)),
		OutsideCards:     make([]lendingOutsideCardDTO, 0, len(pool.OutsideCards)),
	}
	for _, card := range pool.Cards {
		dto.Cards = append(dto.Cards, projectLendingCard(card))
	}
	for _, card := range pool.OutsideCards {
		dto.OutsideCards = append(dto.OutsideCards, projectLendingOutsideCard(card))
	}
	return dto
}

func projectLendingCard(card quota.LendingCard) lendingCardDTO {
	return lendingCardDTO{
		AuthIndex:             card.AuthIndex,
		Name:                  card.Name,
		DisplayName:           card.DisplayName,
		Role:                  card.Role,
		Tier:                  card.Tier,
		WindowID:              card.WindowID,
		WindowKind:            card.WindowKind,
		RemainingPercent:      lendingFloatValue(card.RemainingPercent),
		ResetAtMS:             lendingInt64Value(card.ResetAtMS),
		ResetAccuracy:         card.ResetAccuracy,
		ObservedAtMS:          lendingInt64Value(card.ObservedAtMS),
		FreshUntilMS:          lendingInt64Value(card.FreshUntilMS),
		DaysToReset:           lendingRoundedFloat(card.DaysToReset),
		DailyAvailablePercent: lendingFloatValue(card.DailyAvailablePercent),
		IsComparable:          card.IsComparable,
		Priority:              lendingIntValue(card.Priority),
		PriorityKnown:         card.PriorityKnown,
		PriorityIsDefault:     card.PriorityIsDefault,
		CanEditPriority:       card.CanEditPriority,
		SuggestedPriority:     lendingIntValue(card.SuggestedPriority),
		NeedsChange:           lendingBoolValue(card.NeedsChange),
		PrimaryReason:         card.PrimaryReason,
		ReasonCodes:           lendingStrings(card.ReasonCodes),
	}
}

func projectLendingOutsideCard(card quota.LendingOutsideCard) lendingOutsideCardDTO {
	return lendingOutsideCardDTO{
		AuthIndex:         card.AuthIndex,
		Name:              card.Name,
		DisplayName:       card.DisplayName,
		Provider:          card.Provider,
		PoolID:            card.PoolID,
		PoolLabel:         card.PoolLabel,
		Priority:          lendingIntValue(card.Priority),
		PriorityKnown:     card.PriorityKnown,
		PriorityIsDefault: card.PriorityIsDefault,
	}
}

// lendingCardInputs maps the projected console cards onto the pure computation. It
// is the one place the console's status vocabulary and the roster's per-file fields
// are turned into the lending conditions, so a change in either stays visible here.
//
// The priority is known for a real auth file even when it is zero: CPA stores an
// unset priority as zero, and the console shows that as the default. A compatibility
// entry OMC synthesizes from the opencode group has no per-key priority or model
// list in the roster, so it stays unknown rather than borrowing the group's value.
func lendingCardInputs(items []QuotaItemDTO, files []management.AuthFile) []quota.LendingCardInput {
	byIndex := make(map[string]management.AuthFile, len(files))
	nameCounts := make(map[string]int, len(files))
	for _, file := range files {
		index := strings.TrimSpace(file.AuthIndex)
		if index == "" {
			continue
		}
		byIndex[index] = file
		nameCounts[strings.TrimSpace(file.Name)]++
	}

	inputs := make([]quota.LendingCardInput, 0, len(items))
	for _, item := range items {
		normalized := item.NormalizedQuota
		input := quota.LendingCardInput{
			AuthIndex:    normalized.AuthIndex,
			Name:         normalized.Name,
			Provider:     normalized.Provider,
			IsDisabled:   normalized.Disabled,
			Status:       normalized.Status,
			ObservedAtMS: normalized.ObservedAtMS,
			Windows:      normalized.Windows,
		}
		if normalized.Plan != nil {
			input.PlanType = normalized.Plan.PlanType
			input.PlanLabel = normalized.Plan.PlanLabel
		}
		if normalized.ActiveCooldown != nil && normalized.ActiveCooldown.IsActive {
			input.IsCooldown = true
		}
		// EvaluateStatusAndRecommendation marks an authentication rejection as
		// "needs_reauth"; every other error state is a failed read.
		input.NeedsReauth = normalized.Recommendation.Status == "needs_reauth"
		input.ReadingFailed = !input.NeedsReauth && (normalized.Status == "error" ||
			(item.LastRefresh != nil && item.LastRefresh.Outcome != "success"))
		if file, ok := byIndex[strings.TrimSpace(normalized.AuthIndex)]; ok {
			isCompatibilityKey := file.Type == "openai-compatibility"
			input.IsUnavailable = file.Unavailable
			input.Email = file.Email
			input.ProjectID = file.ProjectID
			input.PriorityKnown = !isCompatibilityKey
			input.Priority = file.Priority
			input.CanEditPriority = !isCompatibilityKey && !file.RuntimeOnly &&
				strings.TrimSpace(file.Name) != "" && nameCounts[strings.TrimSpace(file.Name)] == 1
			input.Models = lendingModelIDs(file.Models)
		}
		inputs = append(inputs, input)
	}
	return inputs
}

func lendingModelIDs(models []management.AuthModel) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		if id := strings.TrimSpace(model.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func lendingStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string{}, values...)
}

func lendingFloatValue(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func lendingIntValue(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func lendingInt64Value(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func lendingBoolValue(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// lendingRoundedFloat keeps the one-decimal contract for a day count; the pure
// computation returns it unrounded so sub-day distances stay available.
func lendingRoundedFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	rounded := math.Round(*value*10) / 10
	return &rounded
}
