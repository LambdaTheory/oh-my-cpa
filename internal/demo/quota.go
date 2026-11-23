package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Seed observations, not estimates: the real handlers join the same locked
// request costs and scoped models a live deployment uses.
func seedQuotaObservations(ctx context.Context, repo *repository.Repository, now time.Time) error {
	payloads := quotaPayloads(now)
	for _, credential := range []struct{ authIndex, provider, url string }{
		{"auth-codex-01", "codex", codexUsageURL},
		{"auth-codex-02", "codex", codexUsageURL},
		{"auth-claude-01", "claude", claudeUsageURL},
		{"auth-antigravity-01", "antigravity", antigravityUsageURL},
	} {
		raw, err := json.Marshal(payloads[credential.url])
		if err != nil {
			return fmt.Errorf("marshal quota payload for %s: %w", credential.authIndex, err)
		}
		var plan *quota.QuotaPlan
		var windows []quota.QuotaWindow
		switch credential.provider {
		case "codex":
			plan, windows, _, err = quota.ParseCodexUsage(raw, now.UnixMilli())
		case "claude":
			windows, _, err = quota.ParseClaudeUsage(raw, now.UnixMilli())
		case "antigravity":
			windows, err = quota.ParseAntigravityUsage(raw, now.UnixMilli(), 0)
		}
		if err != nil {
			return fmt.Errorf("parse %s quota payload for %s: %w", credential.provider, credential.authIndex, err)
		}
		if credential.authIndex == "auth-codex-02" && len(windows) > 0 {
			previous := windows[0]
			currentStart, _, reason := quota.WindowCycleRange(previous, now.UnixMilli(), now.UnixMilli())
			if reason == "" {
				previous.ResetAtMS = &currentStart
				used, remaining := 65.0, 35.0
				previous.UsedPercent, previous.RemainingPercent = &used, &remaining
				if err := saveDemoQuotaSnapshot(ctx, repo, credential.authIndex, credential.provider, plan, []quota.QuotaWindow{previous}, currentStart-60000); err != nil {
					return fmt.Errorf("seed previous-cycle quota for %s: %w", credential.authIndex, err)
				}
				currentUsed, currentRemaining := 2.0, 98.0
				windows[0].UsedPercent, windows[0].RemainingPercent = &currentUsed, &currentRemaining
			}
		}
		if err := saveDemoQuotaSnapshot(ctx, repo, credential.authIndex, credential.provider, plan, windows, now.UnixMilli()); err != nil {
			return fmt.Errorf("seed current-cycle quota for %s: %w", credential.authIndex, err)
		}
	}
	return nil
}

// saveDemoQuotaSnapshot writes the plan beside the windows. The lending pool is
// grouped by provider and plan_type, so a demo snapshot without the plan leaves
// every card in the "unpooled" list and the demonstration never shows advice.
func saveDemoQuotaSnapshot(ctx context.Context, repo *repository.Repository, authIndex, provider string, plan *quota.QuotaPlan, windows []quota.QuotaWindow, observedAtMS int64) error {
	raw, err := json.Marshal(windows)
	if err != nil {
		return fmt.Errorf("marshal quota snapshot for %s: %w", authIndex, err)
	}
	record := repository.QuotaSnapshotRecord{
		ID:           "demo-quota-" + authIndex + "-" + strconv.FormatInt(observedAtMS, 10),
		CreatedAtMS:  observedAtMS,
		AuthIndex:    authIndex,
		Provider:     provider,
		Status:       "healthy",
		WindowsJSON:  string(raw),
		ObservedAtMS: observedAtMS,
	}
	if plan != nil {
		encoded, err := json.Marshal(plan)
		if err != nil {
			return fmt.Errorf("marshal quota plan for %s: %w", authIndex, err)
		}
		record.PlanJSON = string(encoded)
		record.PlanType = plan.PlanType
		record.PlanTier = plan.Tier
	}
	if err := repo.SaveQuotaSnapshot(ctx, record); err != nil {
		return fmt.Errorf("save quota snapshot for %s: %w", authIndex, err)
	}
	return nil
}
