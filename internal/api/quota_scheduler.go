package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// writeQuotaRateLimited answers a refresh that must not reach the upstream yet:
// the same stable code the MCP tool returns, with when the credential is free.
func writeQuotaRateLimited(writer http.ResponseWriter, until time.Time) {
	retryAfterMS := time.Until(until).Milliseconds()
	if retryAfterMS < 0 {
		retryAfterMS = 0
	}
	writeJSON(writer, http.StatusTooManyRequests, map[string]any{
		"error":          "rate_limited_backoff",
		"retry_at_ms":    until.UnixMilli(),
		"retry_after_ms": retryAfterMS,
	})
}

// quotaBackoffBlocked reports whether a credential or its provider is parked
// after an upstream 429. Callers must refuse instead of probing either scope.
func (h *Handler) quotaBackoffBlocked(provider, authIndex string) (bool, time.Time) {
	if h.quotaBackoff == nil {
		return false, time.Time{}
	}
	return h.quotaBackoff.Blocked(provider, authIndex, h.now())
}

// recordQuotaRateLimit parks a credential when a refresh failed with an upstream
// 429, returning whether it did.
func (h *Handler) recordQuotaRateLimit(authIndex, provider string, err error) bool {
	var rateErr *quota.RateLimitError
	if !errors.As(err, &rateErr) {
		return false
	}
	if h.quotaBackoff != nil {
		h.quotaBackoff.Record(provider, authIndex, rateErr.RetryAfter, rateErr.HasRetryAfter, h.now())
	}
	return true
}

// clearQuotaRateLimit forgets a credential's park after a successful refresh.
func (h *Handler) clearQuotaRateLimit(authIndex, provider string) {
	if h.quotaBackoff != nil {
		h.quotaBackoff.Clear(provider, authIndex)
	}
}

// quotaBackoffError is the refusal a caller already inside a park receives. It
// carries the remaining wait so the agent or operator knows when to retry the
// same credential.
func quotaBackoffError(provider, authIndex string, until, now time.Time) error {
	return &quota.RateLimitError{Provider: provider, AuthIndex: authIndex, RetryAfter: until.Sub(now), HasRetryAfter: true}
}

func (h *Handler) readCredentialQuota(ctx context.Context, service *quota.Service, file management.AuthFile, prior *quota.NormalizedQuota) (value *quota.NormalizedQuota, err error) {
	authIndex := strings.TrimSpace(file.AuthIndex)
	provider := quota.DetectProvider(file.Type, file.Provider)
	unlock, err := h.quotaBackoff.LockCredential(ctx, provider, authIndex)
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer func() {
		if recordErr := h.recordQuotaRefreshAttempt(ctx, file, value, err); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
	}()
	if blocked, until := h.quotaBackoffBlocked(provider, authIndex); blocked {
		return nil, quotaBackoffError(provider, authIndex, until, h.now())
	}
	value, err = service.RefreshCredentialQuota(ctx, file, prior)
	if err != nil {
		if h.recordQuotaRateLimit(authIndex, provider, err) {
			if blocked, until := h.quotaBackoffBlocked(provider, authIndex); blocked {
				return nil, quotaBackoffError(provider, authIndex, until, h.now())
			}
		}
		return nil, err
	}
	h.clearQuotaRateLimit(authIndex, provider)
	return value, nil
}

// schedulerConfig maps the process configuration onto the scheduler's rules.
func (h *Handler) schedulerConfig() quota.SchedulerConfig {
	return quota.SchedulerConfig{
		WatchInterval:             h.cfg.Quota.WatchInterval,
		ActiveInterval:            h.cfg.Quota.ActiveInterval,
		NormalInterval:            h.cfg.Quota.NormalInterval,
		IdleInterval:              h.cfg.Quota.IdleInterval,
		WatchUsedPercent:          h.cfg.Quota.WatchUsedPercent,
		WatchWindow:               h.cfg.Quota.WatchWindow,
		NormalWindow:              h.cfg.Quota.NormalWindow,
		WindowEndMargin:           h.cfg.Quota.WindowEndMargin,
		PassiveFreshInterval:      h.cfg.Quota.PassiveFreshInterval,
		CredentialFloor:           h.cfg.Quota.CredentialFloor,
		CredentialFloorByProvider: h.cfg.Quota.CredentialFloorByProvider,
		GlobalConcurrency:         h.cfg.Quota.GlobalConcurrency,
		ResetGrace:                time.Minute,
		ResetLookback:             h.cfg.Quota.IdleInterval,
	}
}

// QuotaScheduler builds the background per-account refresh loop, or nil when it
// is disabled or there is no repository. Its reads and writes are the console's
// own, so the scheduler introduces no second implementation of either.
func (h *Handler) QuotaScheduler(logger *slog.Logger) *quota.Scheduler {
	if h.repo == nil || !h.cfg.Quota.SchedulerEnabled {
		return nil
	}
	scheduler := quota.NewScheduler(h.schedulerConfig(), logger, h.quotaBackoff)
	scheduler.ListFiles = func(ctx context.Context) ([]management.AuthFile, error) {
		client, err := h.capabilityClient(ctx)
		if err != nil {
			return nil, err
		}
		files, err := client.AuthFiles(ctx)
		if err != nil {
			return nil, err
		}
		return files.Files, nil
	}
	scheduler.RequestCounts = func(ctx context.Context, sinceMS int64) (map[string]int64, error) {
		return h.repo.CountUsageEventsByAuthIndex(ctx, defaultInstanceID(), sinceMS)
	}
	scheduler.Prior = func(ctx context.Context, authIndex string) *quota.NormalizedQuota {
		return h.loadPriorNormalizedQuota(ctx, authIndex)
	}
	scheduler.State = func(ctx context.Context, file management.AuthFile, prior *quota.NormalizedQuota, requestsWatch, requestsNormal int64) (quota.SchedulerState, error) {
		return h.schedulerState(ctx, file, prior, requestsWatch, requestsNormal)
	}
	scheduler.Refresh = func(ctx context.Context, file management.AuthFile, prior *quota.NormalizedQuota) (*quota.NormalizedQuota, error) {
		client, err := h.capabilityClient(ctx)
		if err != nil {
			return nil, err
		}
		return quota.NewService(client).RefreshCredentialQuota(ctx, file, prior)
	}
	scheduler.ObserveResult = h.recordQuotaRefreshAttempt
	scheduler.Persist = func(ctx context.Context, value *quota.NormalizedQuota) error {
		// Every result flows through the same persistence entry so a failed refresh
		// still records its safe collection signal; the success gate inside the
		// persist keeps a failed read from overwriting the last good snapshot.
		return h.persistNormalizedQuotaSnapshot(ctx, value)
	}
	return scheduler
}

// schedulerState gathers the windows and passive freshness the tier planner
// reads for one credential.
func (h *Handler) schedulerState(ctx context.Context, file management.AuthFile, prior *quota.NormalizedQuota, requestsWatch, requestsNormal int64) (quota.SchedulerState, error) {
	authIndex := strings.TrimSpace(file.AuthIndex)
	provider := quota.DetectProvider(file.Type, file.Provider)
	state := quota.SchedulerState{RequestsWatchWindow: requestsWatch, RequestsNormalWindow: requestsNormal}
	if prior != nil {
		state.LastSnapshotAtMS = prior.ObservedAtMS
	}
	latest, err := h.repo.LatestStandardPassiveObservations(ctx, []string{authIndex})
	if err != nil {
		return state, err
	}
	for _, row := range latest[authIndex] {
		if row.ObservedAtMS > state.LatestPassiveAtMS {
			state.LatestPassiveAtMS = row.ObservedAtMS
		}
	}
	if prior == nil {
		return state, nil
	}
	now := h.now()
	nowMS := now.UnixMilli()
	for _, window := range prior.Windows {
		scheduled := quota.SchedulerWindow{
			Kind:        quota.WindowObservationKind(provider, window),
			ResetAtMS:   window.ResetAtMS,
			UsedPercent: window.UsedPercent,
		}
		if window.UsedPercent != nil && *window.UsedPercent >= quota.CapacityMinUsedPercent {
			scheduled.HasRecentEnoughSnapshot = true
		}
		// Traffic in the cycle is only read for a window close to its reset: that
		// is the one case the answer changes a decision, and it keeps the sweep
		// from querying usage for every window of every credential every minute.
		if window.ResetAtMS != nil && *window.ResetAtMS > nowMS && time.Until(time.UnixMilli(*window.ResetAtMS)) <= h.cfg.Quota.WindowEndMargin {
			if fromMS, _, reason := quota.WindowCycleRange(window, prior.ObservedAtMS, nowMS); reason == "" {
				if usage, uerr := h.repo.QueryCredentialWindowUsage(ctx, authIndex, fromMS, nowMS); uerr == nil {
					scheduled.HasTrafficInCycle = usage.Requests > 0
				}
			}
		}
		state.Windows = append(state.Windows, scheduled)
	}
	return state, nil
}
