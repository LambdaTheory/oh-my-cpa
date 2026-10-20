package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
	"github.com/oh-my-cpa/oh-my-cpa/internal/warmup"
)

// QuotaItemDTO keeps the legacy quota_exceeded/quota_reason fields alongside the
// embedded normalized quota, so older clients still see the summary they expect.
type QuotaItemDTO struct {
	quota.NormalizedQuota
	Quota            *managementQuotaObservation           `json:"quota,omitempty"`
	ModelQuotas      map[string]managementQuotaObservation `json:"model_quotas,omitempty"`
	QuotaExceeded    bool                                  `json:"quota_exceeded"`
	QuotaReason      string                                `json:"quota_reason,omitempty"`
	NextRecoverAtMS  *int64                                `json:"next_recover_at_ms,omitempty"`
	NextRetryAfterMS *int64                                `json:"next_retry_after_ms,omitempty"`
}

func (h *Handler) getQuotaOverview(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	client, ok := h.managementClientOrError(writer, request)
	if !ok {
		return
	}

	overview, err := h.buildQuotaOverview(request.Context(), client)
	if err != nil {
		writeCPAFacadeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, overview)
}

type refreshQuotaRequest struct {
	AuthIndex   string   `json:"auth_index,omitempty"`
	AuthIndexes []string `json:"auth_indexes,omitempty"`
}

type refreshQuotaResultDTO struct {
	AuthIndex    string                 `json:"auth_index"`
	Name         string                 `json:"name"`
	Outcome      string                 `json:"outcome"`
	Quota        *quota.NormalizedQuota `json:"quota,omitempty"`
	Error        string                 `json:"error,omitempty"`
	RetryAtMS    *int64                 `json:"retry_at_ms,omitempty"`
	RetryAfterMS *int64                 `json:"retry_after_ms,omitempty"`
}

const (
	quotaRefreshMaxUpstreamCalls = 3
	quotaRefreshWriteGrace       = 5 * time.Second
)

// extendQuotaRefreshWriteDeadline replaces the server-wide 30-second deadline
// with the longest bounded path this request can take: one credential-list read,
// up to three provider reads per credential, one four-wide wave, and a serial
// tail if that wave observes a rate limit.
func (h *Handler) extendQuotaRefreshWriteDeadline(writer http.ResponseWriter, targetCount int) {
	if targetCount < 1 {
		return
	}
	if targetCount > 10 {
		targetCount = 10
	}
	requestTimeout := h.queryTimeout()
	serialTail := max(0, targetCount-4)
	probeWaves := 1 + serialTail
	window := time.Duration(1+quotaRefreshMaxUpstreamCalls*probeWaves)*requestTimeout + quotaRefreshWriteGrace
	_ = http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(window))
}

func (h *Handler) refreshCredentialQuota(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	var req refreshQuotaRequest
	if err := decodeManagementJSON(writer, request, 32*1024, &req); err != nil {
		return
	}
	targetCount := len(req.AuthIndexes)
	if strings.TrimSpace(req.AuthIndex) != "" {
		targetCount = 1
	}
	h.extendQuotaRefreshWriteDeadline(writer, targetCount)

	client, ok := h.managementClientOrError(writer, request)
	if !ok {
		return
	}

	ctx := request.Context()
	filesResp, err := client.AuthFiles(ctx)
	if err != nil {
		writeCPAFacadeError(writer, err)
		return
	}

	// Meta's observation endpoint can mint a key. Fail closed before downloading
	// its DCA credential or making that upstream call, including batch refreshes.
	auditMetaAttempt := func(file management.AuthFile) error {
		if quota.DetectProvider(file.Type, file.Provider) != "meta" || file.Disabled {
			return nil
		}
		return h.recordAudit(request, "quota.refresh", "quota", file.AuthIndex, "attempt", map[string]any{"provider": "meta", "upstream_action": "key_exchange"})
	}

	fileMap := make(map[string]management.AuthFile)
	for _, f := range filesResp.Files {
		idx := strings.TrimSpace(f.AuthIndex)
		if idx != "" {
			fileMap[idx] = f
		}
	}

	targetIndex := strings.TrimSpace(req.AuthIndex)
	if targetIndex != "" {
		file, exists := fileMap[targetIndex]
		if !exists {
			writeError(writer, http.StatusNotFound, fmt.Sprintf("credential with auth_index %q not found", targetIndex))
			return
		}

		if err := auditMetaAttempt(file); err != nil {
			writeAuditFailure(writer, "quota refresh attempt could not be audited")
			return
		}
		prior := h.loadPriorNormalizedQuota(ctx, targetIndex)
		refreshed, err := h.readCredentialQuota(ctx, quota.NewService(client), file, prior)
		if err != nil {
			if blocked, until := h.quotaBackoffBlocked(quota.DetectProvider(file.Type, file.Provider), targetIndex); blocked {
				writeQuotaRateLimited(writer, until)
				return
			}
			writeError(writer, http.StatusBadGateway, fmt.Sprintf("refresh quota failed: %v", err))
			return
		}

		// Record the safe collection result for every outcome; the snapshot itself is
		// written only for a successful read inside persistNormalizedQuotaSnapshot.
		if h.repo != nil {
			_ = h.persistNormalizedQuotaSnapshot(ctx, refreshed)
		}
		h.attachWindowCapacity(ctx, refreshed, h.now().UnixMilli())

		outcome := "success"
		if refreshed.Provider == "meta" && refreshed.Error != "" {
			outcome = "failure"
		}
		_ = h.recordAudit(request, "quota.refresh", "quota", targetIndex, outcome, map[string]any{
			"status": refreshed.Status,
		})

		writeJSON(writer, http.StatusOK, map[string]any{
			"status": "ok",
			"quota":  refreshed,
		})
		return
	}

	// Deduplicate before auditing or exchanging keys: repeated selections must
	// not perform the same credential-bearing observation more than once.
	targetIndexes := make([]string, 0, len(req.AuthIndexes))
	seenIndexes := make(map[string]bool, len(req.AuthIndexes))
	for _, value := range req.AuthIndexes {
		authIndex := strings.TrimSpace(value)
		if authIndex != "" && !seenIndexes[authIndex] {
			seenIndexes[authIndex] = true
			targetIndexes = append(targetIndexes, authIndex)
		}
	}
	if len(targetIndexes) == 0 {
		writeError(writer, http.StatusBadRequest, "auth_index or auth_indexes is required")
		return
	}
	if len(targetIndexes) > 10 {
		targetIndexes = targetIndexes[:10]
	}

	for _, targetIndex := range targetIndexes {
		if file, exists := fileMap[strings.TrimSpace(targetIndex)]; exists {
			if err := auditMetaAttempt(file); err != nil {
				writeAuditFailure(writer, "quota refresh attempt could not be audited")
				return
			}
		}
	}
	svc := quota.NewService(client)
	type refreshCompletion struct {
		index     int
		file      management.AuthFile
		refreshed *quota.NormalizedQuota
		err       error
	}
	completed := make(chan refreshCompletion, len(targetIndexes))
	launch := func(index int) {
		authIndex := targetIndexes[index]
		file, exists := fileMap[authIndex]
		if !exists {
			completed <- refreshCompletion{index: index, err: errors.New("credential not found")}
			return
		}
		go func() {
			prior := h.loadPriorNormalizedQuota(ctx, authIndex)
			refreshed, refreshErr := h.readCredentialQuota(ctx, svc, file, prior)
			completed <- refreshCompletion{index: index, file: file, refreshed: refreshed, err: refreshErr}
		}()
	}

	results := make([]refreshQuotaResultDTO, len(targetIndexes))
	refreshedByIndex := make([]*quota.NormalizedQuota, len(targetIndexes))
	nextIndex := 0
	inFlight := 0
	isSerial := false
	for nextIndex < len(targetIndexes) && inFlight < 4 {
		launch(nextIndex)
		nextIndex++
		inFlight++
	}
	for settled := 0; settled < len(targetIndexes); settled++ {
		completion := <-completed
		inFlight--
		authIndex := targetIndexes[completion.index]
		name := strings.TrimSpace(completion.file.Name)
		if name == "" {
			name = authIndex
		}
		result := refreshQuotaResultDTO{AuthIndex: authIndex, Name: name}
		var rateLimit *quota.RateLimitError
		switch {
		case errors.As(completion.err, &rateLimit):
			now := h.now()
			retryAt := now.Add(rateLimit.RetryAfter)
			if blocked, until := h.quotaBackoffBlocked(quota.DetectProvider(completion.file.Type, completion.file.Provider), authIndex); blocked {
				retryAt = until
			}
			retryAfterMS := max(int64(0), retryAt.Sub(now).Milliseconds())
			retryAtMS := retryAt.UnixMilli()
			result.Outcome = "rate_limited"
			result.RetryAtMS = &retryAtMS
			result.RetryAfterMS = &retryAfterMS
			isSerial = true
		case completion.err != nil:
			result.Outcome = "failed"
			result.Error = security.RedactText(completion.err.Error())
		case completion.refreshed == nil:
			result.Outcome = "failed"
			result.Error = "quota refresh returned no result"
		default:
			result.Quota = completion.refreshed
			if h.repo != nil {
				_ = h.persistNormalizedQuotaSnapshot(ctx, completion.refreshed)
			}
			h.attachWindowCapacity(ctx, completion.refreshed, h.now().UnixMilli())
			refreshedByIndex[completion.index] = completion.refreshed
			if completion.refreshed.Error != "" || completion.refreshed.Status == "error" || completion.refreshed.Status == "stale" {
				result.Outcome = "failed"
				result.Error = completion.refreshed.Error
			} else {
				result.Outcome = "success"
			}
		}
		results[completion.index] = result
		if quota.DetectProvider(completion.file.Type, completion.file.Provider) == "meta" {
			auditOutcome := "success"
			if result.Outcome != "success" {
				auditOutcome = "failure"
			}
			_ = h.recordAudit(request, "quota.refresh", "quota", authIndex, auditOutcome, map[string]any{"outcome": result.Outcome})
		}

		if isSerial {
			if inFlight == 0 && nextIndex < len(targetIndexes) {
				launch(nextIndex)
				nextIndex++
				inFlight++
			}
		} else {
			for nextIndex < len(targetIndexes) && inFlight < 4 {
				launch(nextIndex)
				nextIndex++
				inFlight++
			}
		}
	}
	quotas := make([]*quota.NormalizedQuota, 0, len(targetIndexes))
	for _, refreshed := range refreshedByIndex {
		if refreshed != nil {
			quotas = append(quotas, refreshed)
		}
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"status":  "ok",
		"quotas":  quotas,
		"results": results,
		"total":   len(quotas),
	})
}

func (h *Handler) loadPriorNormalizedQuota(ctx context.Context, authIndex string) *quota.NormalizedQuota {
	if h.repo == nil {
		return nil
	}
	snapshots, err := h.repo.GetLatestQuotaSnapshots(ctx, []string{authIndex})
	if err != nil {
		return nil
	}
	snapshot, ok := snapshots[authIndex]
	if !ok {
		return nil
	}
	return normalizedQuotaFromSnapshot(authIndex, snapshot)
}

// normalizedQuotaFromSnapshot rebuilds the prior reading a refresh starts from.
func normalizedQuotaFromSnapshot(authIndex string, snapshot repository.QuotaSnapshotRecord) *quota.NormalizedQuota {
	q := &quota.NormalizedQuota{
		AuthIndex:    authIndex,
		Provider:     snapshot.Provider,
		Status:       snapshot.Status,
		ObservedAtMS: snapshot.ObservedAtMS,
	}
	if plan := planFromSnapshot(snapshot); plan != nil {
		q.Plan = plan
	}
	if snapshot.WindowsJSON != "" {
		var windows []quota.QuotaWindow
		if err := json.Unmarshal([]byte(snapshot.WindowsJSON), &windows); err == nil {
			q.Windows = windows
		}
	}
	if snapshot.ResetCreditsJSON != "" {
		var credits quota.CodexResetCreditsInfo
		if err := json.Unmarshal([]byte(snapshot.ResetCreditsJSON), &credits); err == nil {
			q.ResetCredits = &credits
		}
	}
	if snapshot.ResetGrantsJSON != "" {
		var grants []quota.ClaudeResetGrant
		if err := json.Unmarshal([]byte(snapshot.ResetGrantsJSON), &grants); err == nil {
			q.ResetGrants = grants
		}
	}
	if snapshot.WarmupJSON != "" {
		var observation warmup.Observation
		if json.Unmarshal([]byte(snapshot.WarmupJSON), &observation) == nil {
			q.WarmupEvidence = &observation
		}
	}
	return q
}

func (h *Handler) persistNormalizedQuotaSnapshot(ctx context.Context, q *quota.NormalizedQuota) error {
	if h.repo == nil || q == nil {
		return nil
	}
	// Record the safe collection outcome before the success gate. An auth failure
	// never produces a quota snapshot, so this enum-only signal is the only durable
	// evidence the Feishu report can read that a credential needs re-authentication.
	h.recordQuotaSignal(ctx, q)
	if q.Disabled || q.Error != "" || q.Status == "stale" || q.Status == "error" {
		return nil
	}
	// Carry observed reset evidence through the existing JSON snapshots, so a
	// tainted cycle stays tainted after earlier readings fall out of retention.
	var history []quota.ObservedWindows
	hasIncompleteHistory := false
	for _, window := range q.Windows {
		if !quota.SupportsWindowCapacity(q.Provider, window) {
			continue
		}
		var err error
		history, err = h.loadObservedWindows(ctx, q.AuthIndex, q.Provider)
		if err != nil {
			hasIncompleteHistory = true
			if h.logger != nil {
				h.logger.Warn("quota capacity history unavailable while saving observation", "provider", q.Provider, "error", err)
			}
		}
		break
	}
	windows := append([]quota.QuotaWindow(nil), q.Windows...)
	for i := range windows {
		window := &windows[i]
		window.Usage, window.Capacity, window.CapacityUnavailable = nil, nil, ""
		fromMS, toMS, reason := quota.WindowCycleRange(*window, q.ObservedAtMS, q.ObservedAtMS)
		if reason == "" && quota.SupportsWindowCapacity(q.Provider, *window) {
			window.HasIncompleteHistory = hasIncompleteHistory || quota.HasIncompleteWindowHistory(*window, history, fromMS, toMS)
			window.HasMidCycleReset = quota.HasWindowCycleReset(*window, history, fromMS, toMS)
		}
	}
	q.Windows = windows
	winJSON := "[]"
	if len(q.Windows) > 0 {
		if b, err := json.Marshal(windows); err == nil {
			winJSON = string(b)
		}
	}
	creditsJSON := ""
	if q.ResetCredits != nil {
		if b, err := json.Marshal(q.ResetCredits); err == nil {
			creditsJSON = string(b)
		}
	}
	grantsJSON := ""
	if len(q.ResetGrants) > 0 {
		if b, err := json.Marshal(q.ResetGrants); err == nil {
			grantsJSON = string(b)
		}
	}
	planType := ""
	planTier := ""
	planJSON := ""
	if q.Plan != nil {
		planType = q.Plan.PlanType
		planTier = q.Plan.Tier
		if b, err := json.Marshal(q.Plan); err == nil {
			planJSON = string(b)
		}
	}

	warmupJSON := ""
	if q.WarmupEvidence != nil {
		if raw, err := json.Marshal(q.WarmupEvidence); err == nil {
			warmupJSON = string(raw)
		}
	}
	if err := h.repo.SaveQuotaSnapshot(ctx, repository.QuotaSnapshotRecord{
		WarmupJSON:       warmupJSON,
		AuthIndex:        q.AuthIndex,
		Provider:         q.Provider,
		Status:           q.Status,
		PlanType:         planType,
		PlanTier:         planTier,
		PlanJSON:         planJSON,
		WindowsJSON:      winJSON,
		ResetCreditsJSON: creditsJSON,
		ResetGrantsJSON:  grantsJSON,
		ObservedAtMS:     q.ObservedAtMS,
	}); err != nil {
		return err
	}
	// The same successful refresh that produced the snapshot is recorded in the
	// observation timeline, so a manual refresh, an MCP refresh and the scheduler
	// all leave the same history. A recording failure is not a snapshot failure:
	// the snapshot is already durable, and the timeline is best-effort history.
	if _, err := h.repo.RecordActiveQuotaObservations(ctx, q); err != nil && h.logger != nil {
		h.logger.Warn("record active quota observations", "auth_index", q.AuthIndex, "error", err)
	}
	return nil
}

// recordQuotaSignal persists the safe, enum-only collection outcome for one
// credential. It runs for every refresh result, including a failure, so the Feishu
// report can tell an auth failure (which never produces a snapshot) from a
// transport one. It stores no raw error, token or secret.
func (h *Handler) recordQuotaSignal(ctx context.Context, q *quota.NormalizedQuota) {
	if h == nil || h.repo == nil || q == nil {
		return
	}
	authIndex := strings.TrimSpace(q.AuthIndex)
	if authIndex == "" {
		return
	}
	state := classifyQuotaSignal(q)
	signal := repository.FeishuAccountSignal{
		AuthIndex:  authIndex,
		Provider:   strings.TrimSpace(q.Provider),
		State:      state,
		ObservedAt: quotaSignalObservedAt(q, state, h.now()),
	}
	if err := h.repo.SaveFeishuAccountSignal(ctx, signal); err != nil && h.logger != nil {
		h.logger.Warn("record feishu quota signal", "auth_index", authIndex, "error", err)
	}
}

// classifyQuotaSignal maps a refresh result to the fixed collection-state
// vocabulary. The authentication decision reuses the quota layer's already-tested
// classification: EvaluateStatusAndRecommendation sets the recommendation to
// needs_reauth only for an explicit auth failure, so this never re-parses a raw
// error string for a 401. An error that is not needs_reauth is a transport
// failure; a successful read is healthy.
func classifyQuotaSignal(q *quota.NormalizedQuota) string {
	if q == nil {
		return repository.FeishuSignalUnknown
	}
	if strings.EqualFold(strings.TrimSpace(q.Recommendation.Status), "needs_reauth") {
		return repository.FeishuSignalAuthInvalid
	}
	if strings.TrimSpace(q.Error) != "" {
		return repository.FeishuSignalTransport
	}
	switch strings.ToLower(strings.TrimSpace(q.Status)) {
	case "healthy", "warning", "exhausted", "cooldown", "unpublished":
		return repository.FeishuSignalHealthy
	case "stale", "error":
		return repository.FeishuSignalTransport
	default:
		return repository.FeishuSignalUnknown
	}
}

// quotaSignalObservedAt returns the instant the collection outcome was observed.
// A successful read carries its real reading instant; a failure carries the
// attempt instant (now) because a failed result keeps the prior reading's
// timestamp, which would otherwise make a fresh auth failure look old.
func quotaSignalObservedAt(q *quota.NormalizedQuota, state string, now time.Time) time.Time {
	if q != nil && state == repository.FeishuSignalHealthy && q.ObservedAtMS > 0 {
		return time.UnixMilli(q.ObservedAtMS).UTC()
	}
	return now
}

// planFromSnapshot rebuilds the full normalized plan (including subscription
// expiry and extra usage) from the persisted plan payload, falling back to the
// legacy plan_type/plan_tier columns for snapshots written before plan_json.
func planFromSnapshot(snapshot repository.QuotaSnapshotRecord) *quota.QuotaPlan {
	if snapshot.PlanJSON != "" {
		var plan quota.QuotaPlan
		if err := json.Unmarshal([]byte(snapshot.PlanJSON), &plan); err == nil && plan.PlanType != "" {
			return &plan
		}
	}
	if snapshot.PlanType != "" {
		return &quota.QuotaPlan{
			PlanType:  snapshot.PlanType,
			PlanLabel: snapshot.PlanType,
			Tier:      snapshot.PlanTier,
		}
	}
	return nil
}

type quotaActionRequest struct {
	AuthIndex string `json:"auth_index"`
}

func (h *Handler) clearCredentialCooldownWithAction(writer http.ResponseWriter, request *http.Request, action string) {
	writer.Header().Set("Cache-Control", "no-store")
	var req quotaActionRequest
	if err := decodeManagementJSON(writer, request, 8*1024, &req); err != nil {
		return
	}

	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		writeError(writer, http.StatusBadRequest, "auth_index is required")
		return
	}

	client, ok := h.managementClientOrError(writer, request)
	if !ok {
		return
	}

	if err := h.providerWrites.acquire(request.Context()); err != nil {
		writeProviderWriteError(writer, err)
		return
	}
	defer h.providerWrites.release()
	result, err := h.clearQuotaCooldown(request.Context(), client, authIndex, action, func(action, targetType, targetID, result string, details map[string]any) error {
		return h.recordAudit(request, action, targetType, targetID, result, details)
	})
	if err != nil {
		writeProviderWriteError(writer, err)
		return
	}
	writeJSON(writer, 200, result)
}

func (h *Handler) clearCredentialCooldown(writer http.ResponseWriter, request *http.Request) {
	h.clearCredentialCooldownWithAction(writer, request, "quota.clear_cooldown")
}

func (h *Handler) resetCredentialQuota(writer http.ResponseWriter, request *http.Request) {
	h.clearCredentialCooldownWithAction(writer, request, "quota.reset")
}

func (h *Handler) redeemCodexResetCredit(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	var req quotaActionRequest
	if err := decodeManagementJSON(writer, request, 8*1024, &req); err != nil {
		return
	}

	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		writeError(writer, http.StatusBadRequest, "auth_index is required")
		return
	}

	client, ok := h.managementClientOrError(writer, request)
	if !ok {
		return
	}

	if err := h.providerWrites.acquire(request.Context()); err != nil {
		writeProviderWriteError(writer, err)
		return
	}
	defer h.providerWrites.release()
	result, err := h.redeemQuotaCredit(request.Context(), client, authIndex, func(action, targetType, targetID, result string, details map[string]any) error {
		return h.recordAudit(request, action, targetType, targetID, result, details)
	})
	if err != nil {
		writeProviderWriteError(writer, err)
		return
	}
	writeJSON(writer, 200, result)
}

func (h *Handler) getCredentialQuotaDetail(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	authIndex := strings.TrimSpace(chi.URLParam(request, "authIndex"))
	if authIndex == "" {
		writeError(writer, http.StatusBadRequest, "auth_index is required")
		return
	}

	client, ok := h.managementClientOrError(writer, request)
	if !ok {
		return
	}

	ctx := request.Context()
	filesResp, err := client.AuthFiles(ctx)
	if err != nil {
		writeCPAFacadeError(writer, err)
		return
	}

	var targetFile *management.AuthFile
	for _, f := range filesResp.Files {
		if strings.TrimSpace(f.AuthIndex) == authIndex {
			targetFile = &f
			break
		}
	}
	if targetFile == nil {
		writeError(writer, http.StatusNotFound, fmt.Sprintf("credential %q not found", authIndex))
		return
	}

	nowMS := h.now().UnixMilli()
	stdProvider := quota.DetectProvider(targetFile.Type, targetFile.Provider)
	caps := quota.CapabilitiesForFile(*targetFile)

	normalized := quota.NormalizedQuota{
		AuthIndex:    authIndex,
		Name:         targetFile.Name,
		Type:         targetFile.Type,
		Provider:     stdProvider,
		Disabled:     targetFile.Disabled,
		ObservedAtMS: nowMS,
		Capabilities: caps,
		Windows:      []quota.QuotaWindow{},
	}

	if h.repo != nil {
		if cooldowns, err := h.repo.BatchCorrelatedCooldowns(ctx, []string{authIndex}, nowMS); err == nil {
			if cooldown, ok := cooldowns[authIndex]; ok && cooldown.IsActive {
				normalized.ActiveCooldown = &quota.ActiveCooldown{
					IsActive:          true,
					Reason:            cooldown.Reason,
					RecoverAtMS:       cooldown.RecoverAtMS,
					RetryAfterSeconds: cooldown.RetryAfterSeconds,
					CorrelatedAtMS:    cooldown.CorrelatedAtMS,
				}
			}
		}
		if snapshots, err := h.repo.GetLatestQuotaSnapshots(ctx, []string{authIndex}); err == nil {
			if snapshot, ok := snapshots[authIndex]; ok {
				normalized.ObservedAtMS = snapshot.ObservedAtMS
				// See buildQuotaOverview: an empty reading survives only through the stored status.
				normalized.Status = snapshot.Status
				if plan := planFromSnapshot(snapshot); plan != nil {
					normalized.Plan = plan
				}
				if snapshot.WindowsJSON != "" && snapshot.WindowsJSON != "[]" {
					var windows []quota.QuotaWindow
					if err := json.Unmarshal([]byte(snapshot.WindowsJSON), &windows); err == nil {
						normalized.Windows = windows
					}
				}
				if snapshot.ResetCreditsJSON != "" {
					var credits quota.CodexResetCreditsInfo
					if err := json.Unmarshal([]byte(snapshot.ResetCreditsJSON), &credits); err == nil {
						normalized.ResetCredits = &credits
					}
				}
				if snapshot.ResetGrantsJSON != "" {
					var grants []quota.ClaudeResetGrant
					if err := json.Unmarshal([]byte(snapshot.ResetGrantsJSON), &grants); err == nil {
						normalized.ResetGrants = grants
					}
				}
			}
		}
	}

	quota.EvaluateStatusAndRecommendation(&normalized, nowMS)
	h.attachWindowCapacity(ctx, &normalized, nowMS)
	h.overlayPassiveObservations(ctx, &normalized)

	var history []repository.QuotaSnapshotRecord
	if h.repo != nil {
		if hist, err := h.repo.GetQuotaSnapshotHistory(ctx, authIndex, 20); err == nil {
			history = hist
		}
	}

	writeJSON(writer, http.StatusOK, map[string]any{
		"quota":        normalized,
		"history":      history,
		"model_quotas": projectModelQuotas(targetFile.ModelQuotas),
	})
}

type quotaOverviewData struct {
	Summary quota.QuotaOverviewSummary `json:"summary"`
	Quotas  []QuotaItemDTO             `json:"quotas"`
	Total   int                        `json:"total"`
}

func (h *Handler) buildQuotaOverview(ctx context.Context, client *management.Client) (quotaOverviewData, error) {
	filesResp, err := client.AuthFiles(ctx)
	if err != nil {
		return quotaOverviewData{}, err
	}

	authIndexes := make([]string, 0, len(filesResp.Files))
	fileMap := make(map[string]management.AuthFile)
	for _, file := range filesResp.Files {
		authIndex := strings.TrimSpace(file.AuthIndex)
		if authIndex == "" {
			continue
		}
		authIndexes = append(authIndexes, authIndex)
		fileMap[authIndex] = file
	}

	nowMS := h.now().UnixMilli()
	var latestSnapshots map[string]repository.QuotaSnapshotRecord
	var activeCooldowns map[string]repository.ActiveCooldownRecord

	if h.repo != nil && len(authIndexes) > 0 {
		if snapshots, err := h.repo.GetLatestQuotaSnapshots(ctx, authIndexes); err == nil {
			latestSnapshots = snapshots
		}
		if cooldowns, err := h.repo.BatchCorrelatedCooldowns(ctx, authIndexes, nowMS); err == nil {
			activeCooldowns = cooldowns
		}
	}

	items := make([]QuotaItemDTO, 0, len(authIndexes))
	var soonestRecoveryMS *int64

	var healthyCount, warningCount, exhaustedCount, cooldownCount, attentionCount int

	for _, authIndex := range authIndexes {
		file := fileMap[authIndex]
		stdProvider := quota.DetectProvider(file.Type, file.Provider)
		caps := quota.CapabilitiesForFile(file)

		normalized := quota.NormalizedQuota{
			AuthIndex:    authIndex,
			Name:         file.Name,
			Type:         file.Type,
			Provider:     stdProvider,
			Disabled:     file.Disabled,
			ObservedAtMS: nowMS,
			Capabilities: caps,
			Windows:      []quota.QuotaWindow{},
		}

		if file.Quota != nil {
			if signals, ok := file.Quota["signals"].(map[string]any); ok {
				rawSigs := make(map[string]string)
				for k, v := range signals {
					rawSigs[k] = fmt.Sprint(v)
				}
				normalized.RawSignals = rawSigs
			}
		}

		if cooldown, ok := activeCooldowns[authIndex]; ok && cooldown.IsActive {
			normalized.ActiveCooldown = &quota.ActiveCooldown{
				IsActive:          true,
				Reason:            cooldown.Reason,
				RecoverAtMS:       cooldown.RecoverAtMS,
				RetryAfterSeconds: cooldown.RetryAfterSeconds,
				CorrelatedAtMS:    cooldown.CorrelatedAtMS,
			}
			if cooldown.RecoverAtMS != nil && *cooldown.RecoverAtMS > nowMS {
				if soonestRecoveryMS == nil || *cooldown.RecoverAtMS < *soonestRecoveryMS {
					soonestRecoveryMS = cooldown.RecoverAtMS
				}
			}
		}

		if snapshot, ok := latestSnapshots[authIndex]; ok {
			normalized.ObservedAtMS = snapshot.ObservedAtMS
			// The stored status is what the last read produced, and an empty reading is only
			// knowable from it: without it the evaluation below recomputes the credential as one
			// nobody has read, and the console asks for a repeat of a read that already happened.
			normalized.Status = snapshot.Status
			if plan := planFromSnapshot(snapshot); plan != nil {
				normalized.Plan = plan
			}
			if snapshot.WindowsJSON != "" && snapshot.WindowsJSON != "[]" {
				var windows []quota.QuotaWindow
				if err := json.Unmarshal([]byte(snapshot.WindowsJSON), &windows); err == nil {
					normalized.Windows = windows
					// Check windows for soonest recovery
					for _, w := range windows {
						if w.ResetAtMS != nil && *w.ResetAtMS > nowMS {
							if soonestRecoveryMS == nil || *w.ResetAtMS < *soonestRecoveryMS {
								soonestRecoveryMS = w.ResetAtMS
							}
						}
					}
				}
			}
			if snapshot.ResetCreditsJSON != "" {
				var credits quota.CodexResetCreditsInfo
				if err := json.Unmarshal([]byte(snapshot.ResetCreditsJSON), &credits); err == nil {
					normalized.ResetCredits = &credits
				}
			}
			if snapshot.ResetGrantsJSON != "" {
				var grants []quota.ClaudeResetGrant
				if err := json.Unmarshal([]byte(snapshot.ResetGrantsJSON), &grants); err == nil {
					normalized.ResetGrants = grants
				}
			}
		}

		quota.EvaluateStatusAndRecommendation(&normalized, nowMS)
		h.attachWindowCapacity(ctx, &normalized, nowMS)
		h.overlayPassiveObservations(ctx, &normalized)

		switch normalized.Status {
		case "healthy":
			healthyCount++
		case "warning":
			warningCount++
			attentionCount++
		case "exhausted":
			exhaustedCount++
			attentionCount++
		case "cooldown":
			cooldownCount++
			attentionCount++
		case "error":
			attentionCount++
		}

		dto := QuotaItemDTO{
			NormalizedQuota: normalized,
			Quota:           projectQuota(file.Quota),
			ModelQuotas:     projectModelQuotas(file.ModelQuotas),
		}
		if normalized.ActiveCooldown != nil && normalized.ActiveCooldown.IsActive {
			dto.QuotaExceeded = true
			dto.QuotaReason = normalized.ActiveCooldown.Reason
			dto.NextRecoverAtMS = normalized.ActiveCooldown.RecoverAtMS
			if normalized.ActiveCooldown.RetryAfterSeconds != nil {
				ms := *normalized.ActiveCooldown.RetryAfterSeconds * 1000
				dto.NextRetryAfterMS = &ms
			}
		} else if normalized.Status == "exhausted" {
			dto.QuotaExceeded = true
			dto.QuotaReason = "配额已耗尽"
		}

		items = append(items, dto)
	}

	summary := quota.QuotaOverviewSummary{
		TotalCredentials:  len(items),
		HealthyCount:      healthyCount,
		WarningCount:      warningCount,
		ExhaustedCount:    exhaustedCount,
		CooldownCount:     cooldownCount,
		AttentionCount:    attentionCount,
		SoonestRecoveryMS: soonestRecoveryMS,
	}

	return quotaOverviewData{summary, items, len(items)}, nil
}
