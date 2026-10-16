package api

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/ollama"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// The Feishu report source projects OMC's newest stored readings into the bot's
// minimal read-only report. It is fully stored-only: a query or a tick must
// answer even when CPA is unreachable, so ReadReport never calls the management
// gateway and never triggers a refresh. It reads:
//
//   - the safe credential metadata that the discovery sync persisted, for which
//     credentials exist and whether OMC disabled one. That disable flag is a
//     cache from the last sync, so its age travels with the account and is never
//     presented as live;
//   - the newest stored quota snapshot per credential, which carries the last
//     successful reading and its real observed instant;
//   - correlated cooldowns, for a cooldown recovery time;
//   - the Ollama module's persisted safe readings.
//
// It never invents an observation time: a credential with no stored snapshot
// reports "never read", and its GeneratedAt stays zero rather than being
// replaced by now. When no stored credential metadata exists at all, the channel
// is still named from the persisted observation timeline with a collector-level
// error, which states the limit of a stored source instead of reaching for CPA.

// BuildFeishuReportSource returns the read-only report source the Feishu bot
// answers from.
func (h *Handler) BuildFeishuReportSource() feishu.ReportSource {
	return feishu.ReportSourceFunc(func(ctx context.Context) (feishu.Report, bool) {
		return h.readFeishuReport(ctx)
	})
}

func (h *Handler) readFeishuReport(ctx context.Context) (feishu.Report, bool) {
	if h == nil || h.repo == nil {
		return feishu.Report{}, false
	}
	now := h.now()
	nowMS := now.UnixMilli()

	metadata, metaErr := h.repo.ListFeishuCredentialMetadata(ctx)
	if metaErr != nil {
		h.logger.Warn("feishu report: credential metadata read failed", "error", metaErr)
	}
	accountsByProvider := map[string][]feishu.AccountReport{}
	// unavailableChannels marks the channels that were named only from the
	// observation timeline because no stored credential metadata exists. They carry
	// the collector-level error; a channel with real stored metadata does not.
	unavailableChannels := map[string]bool{}

	// No stored credential metadata means there is no trusted cache of which
	// credentials exist or which OMC disabled. The channel is still known from
	// the persisted observation timeline, so it is named with a collector-level
	// error and no account: that is what lets monitor_down be timed from the
	// channel's persisted first-seen instant instead of silently disappearing,
	// and it keeps a possibly-disabled credential from being reported as active.
	metadataUnavailable := metaErr != nil || len(metadata) == 0

	if !metadataUnavailable {
		authIndexes := make([]string, 0, len(metadata))
		for _, entry := range metadata {
			authIndexes = append(authIndexes, entry.AuthIndex)
		}
		snapshots, err := h.repo.GetLatestQuotaSnapshots(ctx, authIndexes)
		if err != nil {
			h.logger.Warn("feishu report: quota snapshot read failed", "error", err)
		}
		signals, err := h.repo.GetFeishuAccountSignals(ctx, authIndexes)
		if err != nil {
			h.logger.Warn("feishu report: collection signal read failed", "error", err)
		}
		cooldowns, err := h.repo.BatchCorrelatedCooldowns(ctx, authIndexes, nowMS)
		if err != nil {
			h.logger.Warn("feishu report: cooldown read failed", "error", err)
		}
		for _, entry := range metadata {
			snapshot := snapshots[entry.AuthIndex]
			account := feishuAccountReport(entry, snapshot, signals[entry.AuthIndex], now)
			if cooldown, ok := cooldowns[entry.AuthIndex]; ok && cooldown.IsActive {
				account.CooldownUntil = timeFromMS(cooldown.RecoverAtMS)
			}
			provider := feishuCredentialProvider(entry, snapshot)
			accountsByProvider[provider] = append(accountsByProvider[provider], account)
		}
	} else {
		h.logger.Warn("feishu report: stored credential metadata unavailable", "error", metaErr)
		if providers, err := h.repo.ListKnownQuotaProviders(ctx); err == nil {
			for _, provider := range providers {
				if provider == ollama.Provider {
					continue
				}
				accountsByProvider[provider] = nil
				unavailableChannels[provider] = true
			}
		}
	}

	// Ollama accounts are read from OMC's own persisted safe readings, never scraped
	// here. A disabled account only stops collection; it is excluded from channel
	// outage evidence because there is nothing for an operator to repair.
	if ollamaAccounts, err := h.repo.ListOllamaAccounts(ctx); err == nil {
		for _, account := range ollamaAccounts {
			accountsByProvider[ollama.Provider] = append(accountsByProvider[ollama.Provider], ollamaAccountReport(account))
		}
	} else {
		h.logger.Warn("feishu report: ollama account read failed", "error", err)
	}

	providers := make([]string, 0, len(accountsByProvider))
	for provider := range accountsByProvider {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	report := feishu.Report{Providers: make([]feishu.ProviderReport, 0, len(providers))}
	newest := int64(0)
	hasData := false
	for _, provider := range providers {
		accounts := accountsByProvider[provider]
		if accounts == nil {
			accounts = []feishu.AccountReport{}
		}
		pr := feishu.ProviderReport{Provider: provider, Accounts: accounts}
		if unavailableChannels[provider] {
			pr.Error = "credential metadata unavailable"
		}
		for _, account := range accounts {
			if account.ObservedAt.After(time.Time{}) {
				hasData = true
				if ms := account.ObservedAt.UnixMilli(); ms > newest {
					newest = ms
				}
			}
		}
		report.Providers = append(report.Providers, pr)
	}
	if newest > 0 {
		report.GeneratedAt = time.UnixMilli(newest).UTC()
	}
	// has is true whenever a channel is known at all, so a configured-but-failing
	// channel reaches the policy instead of reading as "no data yet".
	return report, hasData || len(report.Providers) > 0
}

// feishuCredentialProvider names the display channel for a stored credential. The
// newest stored snapshot's provider is the same key the quota console groups by;
// a credential that has never been read falls back to the discovery driver, which
// is the normalized provider CPA reported. It never reaches for the live gateway.
func feishuCredentialProvider(meta repository.FeishuCredentialMetadata, snapshot repository.QuotaSnapshotRecord) string {
	if provider := strings.TrimSpace(snapshot.Provider); provider != "" {
		return provider
	}
	return quota.DetectProvider("", meta.Driver)
}

// feishuAccountReport projects one stored credential, its newest snapshot and its
// newest safe collection signal. The disable flag and its sync time come from the
// discovery metadata, never from a live read; the reading times come from the
// snapshot, never from now.
//
// The cached "unavailable" flag is never an authentication failure: the stored
// details carry no error or status text, so a generic unavailable cannot be told
// apart from an exhausting or transiently broken credential. It is reported as
// stale at worst, never invalid. The only auth-failure evidence is the enum-only
// collection signal, which carries the explicit needs_reauth outcome that a quota
// snapshot cannot store.
func feishuAccountReport(meta repository.FeishuCredentialMetadata, snapshot repository.QuotaSnapshotRecord, signal repository.FeishuAccountSignal, now time.Time) feishu.AccountReport {
	hasSnapshot := snapshot.AuthIndex != ""
	account := feishu.AccountReport{
		Key:                strings.TrimSpace(meta.AuthIndex),
		Label:              strings.TrimSpace(meta.Name),
		Disabled:           meta.Disabled,
		State:              feishuStateForStatus(snapshot.Status, hasSnapshot),
		Windows:            feishuWindowsFromJSON(snapshot.WindowsJSON),
		MetadataObservedAt: meta.ObservedAt,
	}
	account.OK = hasNumericWindow(account.Windows)
	if hasSnapshot && snapshot.ObservedAtMS > 0 {
		observed := time.UnixMilli(snapshot.ObservedAtMS).UTC()
		account.LastSuccessAt = observed
		account.ObservedAt = observed
	}
	// A cached availability problem is not an auth failure: it reads as stale, never
	// invalid, whether the cache is fresh or old.
	if meta.Unavailable && account.State != feishu.StateInvalid {
		account.State = feishu.StateStale
	}
	applyQuotaSignal(&account, signal, hasSnapshot, now)
	return account
}

// applyQuotaSignal overlays the safe collection result onto an account. A fresh
// auth failure opens a credential problem; a successful reading observed after the
// auth failure is the only thing that clears it; a stale auth failure neither
// alerts nor resolves; a non-auth failure never becomes a credential problem.
func applyQuotaSignal(account *feishu.AccountReport, signal repository.FeishuAccountSignal, hasSnapshot bool, now time.Time) {
	if account == nil {
		return
	}
	switch strings.TrimSpace(signal.State) {
	case repository.FeishuSignalAuthInvalid:
		// A successful reading newer than the auth failure is positive recovery.
		if hasSnapshot && account.OK && account.ObservedAt.After(signal.ObservedAt) {
			return
		}
		account.AuthFailed = true
		if signalFresh(signal.ObservedAt, now) {
			account.State = feishu.StateInvalid
		} else {
			// Stale auth: do not raise a new alert, and do not claim recovery.
			account.State = feishu.StateStale
		}
	case repository.FeishuSignalHealthy:
		// The newest collection succeeded. Without a stored reading, the channel is
		// still known to be healthy; with one, the snapshot already carries its state.
		if !hasSnapshot {
			account.State = feishu.StateHealthy
		}
	case repository.FeishuSignalTransport, repository.FeishuSignalParse, repository.FeishuSignalUnknown:
		// Not auth: never a credential problem. Absent a valid reading, the account
		// reads as stale rather than failing authentication.
		if !account.OK {
			account.State = feishu.StateStale
		}
	}
}

// signalFresh reports whether a collection signal's observation instant is recent
// enough to be current evidence. A zero instant is unknown, never fresh.
func signalFresh(observedAt, now time.Time) bool {
	if observedAt.IsZero() {
		return false
	}
	age := now.Sub(observedAt)
	if age < 0 {
		age = 0
	}
	return age <= feishu.DefaultFreshWindow
}

// ollamaAccountReport projects one persisted Ollama account. It reads only the
// safe snapshot, never the encrypted cookie.
func ollamaAccountReport(account repository.OllamaAccount) feishu.AccountReport {
	windows := feishuWindowsFromNormalizedQuota(account.QuotaSnapshotJSON)
	report := feishu.AccountReport{
		Key:      "ollama:" + strings.TrimSpace(account.ID),
		Label:    strings.TrimSpace(account.Label),
		Disabled: !account.Enabled,
		Windows:  windows,
	}
	report.State = ollamaStateFor(account.Status, windows)
	report.OK = strings.EqualFold(strings.TrimSpace(account.Status), ollama.StatusOK) && hasNumericWindow(windows)
	if account.LastSuccessAtMS != nil && *account.LastSuccessAtMS > 0 {
		report.LastSuccessAt = time.UnixMilli(*account.LastSuccessAtMS).UTC()
	}
	if account.QuotaObservedAtMS != nil && *account.QuotaObservedAtMS > 0 {
		report.ObservedAt = time.UnixMilli(*account.QuotaObservedAtMS).UTC()
	} else if account.LastObservedAtMS != nil && *account.LastObservedAtMS > 0 {
		report.ObservedAt = time.UnixMilli(*account.LastObservedAtMS).UTC()
	}
	return report
}

// feishuStateForStatus maps a stored quota status onto the bot's state vocabulary.
// A failed read is "stale" (a transport problem), not "invalid" (a credential
// problem); an unread or unpublished credential is unknown, never healthy.
func feishuStateForStatus(status string, hasSnapshot bool) string {
	if !hasSnapshot {
		return feishu.StateUnknown
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "healthy":
		return feishu.StateHealthy
	case "warning":
		return feishu.StateWarning
	case "exhausted":
		return feishu.StateExhausted
	case "cooldown":
		return feishu.StateNotice
	case "needs_reauth":
		return feishu.StateInvalid
	case "stale", "error":
		return feishu.StateStale
	case "unpublished", "idle", "":
		return feishu.StateUnknown
	default:
		return feishu.StateUnknown
	}
}

// ollamaStateFor maps the newest collection status and the safe windows onto the
// bot's state vocabulary. A scrape failure is stale, an expired cookie is invalid,
// and a never-collected account is unknown.
func ollamaStateFor(status string, windows []feishu.Window) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case ollama.StatusOK:
		for _, window := range windows {
			if window.Scope == feishu.ScopeAccount && window.UsedPercent != nil && *window.UsedPercent >= 100 {
				return feishu.StateExhausted
			}
		}
		if len(windows) == 0 {
			return feishu.StateUnknown
		}
		return feishu.StateHealthy
	case ollama.StatusAuthInvalid:
		return feishu.StateInvalid
	case ollama.StatusTransportError, ollama.StatusParseError:
		return feishu.StateStale
	default:
		return feishu.StateUnknown
	}
}

// feishuWindowsFromJSON decodes the shared quota window shape from a stored
// snapshot. A window without a numeric percentage is carried as "not reported".
func feishuWindowsFromJSON(raw string) []feishu.Window {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var windows []quota.QuotaWindow
	if err := json.Unmarshal([]byte(raw), &windows); err != nil {
		return nil
	}
	return feishuWindowsFromQuotaWindows(windows)
}

func feishuWindowsFromNormalizedQuota(raw string) []feishu.Window {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var value quota.NormalizedQuota
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil
	}
	return feishuWindowsFromQuotaWindows(value.Windows)
}

func feishuWindowsFromQuotaWindows(windows []quota.QuotaWindow) []feishu.Window {
	if len(windows) == 0 {
		return nil
	}
	out := make([]feishu.Window, 0, len(windows))
	for _, window := range windows {
		scope := "group"
		switch strings.ToLower(strings.TrimSpace(window.Scope)) {
		case quota.ObservationScopeStandard, "":
			scope = feishu.ScopeAccount
		case "model":
			scope = "model"
		}
		entry := feishu.Window{
			Name:        window.ID,
			Label:       window.Label,
			Scope:       scope,
			UsedPercent: window.UsedPercent,
		}
		if window.ResetAtMS != nil && *window.ResetAtMS > 0 {
			reset := time.UnixMilli(*window.ResetAtMS).UTC()
			entry.ResetAt = &reset
		}
		out = append(out, entry)
	}
	return out
}

// hasNumericWindow reports a valid numeric reading: at least one window states a
// used share. A window with no percentage is "not reported", never zero.
func hasNumericWindow(windows []feishu.Window) bool {
	for _, window := range windows {
		if window.UsedPercent != nil {
			return true
		}
	}
	return false
}

func timeFromMS(value *int64) *time.Time {
	if value == nil || *value <= 0 {
		return nil
	}
	at := time.UnixMilli(*value).UTC()
	return &at
}
