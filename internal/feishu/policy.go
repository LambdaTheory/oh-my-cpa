package feishu

import (
	"hash/fnv"
	"sort"
	"strconv"
	"time"
)

// Alert kinds the denoise policy can raise. They are persisted identities, so
// the wire strings are stable.
const (
	AlertCredential         = "credential_invalid"
	AlertChannelUnavailable = "channel_unavailable"
	AlertMonitorDown        = "monitor_down"
)

// Alert severities.
const (
	SeverityUrgent = "urgent"
	SeverityWarn   = "warn"
)

// Pending item statuses.
const (
	PendingOpen     = "open"
	PendingResolved = "resolved"
)

// Alert is one immediate notification unit. It is secret-free: it carries the
// opaque credential key and the display alias, never a cookie or token.
type Alert struct {
	Scope      string
	ObservedAt time.Time
	Kind       string
	Severity   string
	Provider   string
	Key        string
	Alias      string
	Title      string
	Detail     string
	Facts      []string
	Advice     string
	OccurredAt time.Time
}

// PendingItem is one structured "something needs a human" record. ID is stable
// across restarts, so the same ongoing problem never re-alerts after a restart.
type PendingItem struct {
	ID       string
	Kind     string
	Provider string
	// Key is empty for channel-level items.
	Key        string
	Alias      string
	Since      time.Time
	LastSeen   time.Time
	Action     string
	Status     string
	NotifiedAt time.Time
	ResolvedAt time.Time
}

// ChannelTrack is the per-provider memory behind monitor_down: when the channel
// was first observed and when it last produced any valid data. Persisting it lets
// an outage be timed from its real beginning across restarts, including a
// collector-level failure that yields no account reading at all.
type ChannelTrack struct {
	FirstSeen time.Time
	LastValid time.Time
}

// PolicyConfig is the policy's immutable thresholds.
type PolicyConfig struct {
	EnableAttention         bool
	TightRemainingPercent   float64
	ChannelUnavailableAfter time.Duration
	MonitorDownAfter        time.Duration
	// Location only affects the renderer; it is threaded for fact formatting.
	Location *time.Location
}

// PolicyInput is one round.
//
// Quiet is informational only: the classification and the emitted alerts are the
// same whether or not quiet hours are active. The caller decides whether to send
// an alert now or persist it as a queued notification until the quiet window ends,
// which is why a quiet-opened item is stamped notified here and its alert is still
// returned.
type PolicyInput struct {
	Report Report
	Prev   []PendingItem
	Tracks map[string]ChannelTrack
	Now    time.Time
	Quiet  bool
}

// PolicyResult is the policy's verdict for one round.
type PolicyResult struct {
	// Items is the complete pending-item list after reconciliation.
	Items []PendingItem
	// Opened and Resolved describe this round's transitions.
	Opened   []PendingItem
	Resolved []PendingItem
	// Alerts are the immediate alerts for items opened this round. They are
	// returned whether or not quiet hours are active; the caller sends them now or
	// persists them as queued notifications.
	Alerts []Alert
}

// evaluatePolicy classifies one round. It never changes a reading; it only
// classifies the report into the three immediate conditions and reconciles the
// pending list. A problem alerts once per episode: the first time it is observed
// open, and not again until it resolves and recurs.
func evaluatePolicy(cfg PolicyConfig, in PolicyInput) PolicyResult {
	info := analyzeReport(cfg, in.Report, in.Now, in.Tracks)

	prevAll := map[string]PendingItem{}
	for _, item := range in.Prev {
		prevAll[item.ID] = item
	}

	desired, alertFor := policySignals(info, in.Now)
	if cfg.EnableAttention {
		for _, alert := range attentionSignals(in.Report, cfg.TightRemainingPercent, in.Now) {
			if alert.Kind == AlertReadingMissing && !hasFreshAttempt(in.Report, alert, in.Now) {
				continue
			}
			id := alertItemID(alert)
			desired[id] = PendingItem{ID: id, Kind: alert.Kind, Provider: alert.Provider, Key: alert.Key, Alias: alert.Alias, Action: alert.Advice}
			copy := alert
			alertFor[id] = &copy
			if alert.Kind == AlertQuotaLow || alert.Kind == AlertQuotaExhausted {
				// The credential window already explains the outage and its action.
				channelID := ItemID(AlertChannelUnavailable, alert.Provider, "", "")
				delete(alertFor, channelID)
				delete(desired, channelID)
			}
		}
	}

	var (
		opened   []PendingItem
		resolved []PendingItem
		items    []PendingItem
		seen     = map[string]bool{}
	)
	for id, sig := range desired {
		seen[id] = true
		prev, ok := prevAll[id]
		if ok && prev.Status == PendingOpen {
			item := prev
			item.Kind = sig.Kind
			item.Provider = sig.Provider
			item.Key = sig.Key
			item.Alias = sig.Alias
			item.Action = sig.Action
			item.LastSeen = in.Now
			item.Status = PendingOpen
			if item.NotifiedAt.IsZero() {
				if _, has := alertFor[id]; has {
					item.NotifiedAt = in.Now
				}
			}
			items = append(items, item)
			continue
		}
		// New, or a recurrence of a resolved item: fresh Since and no previous
		// notification, so it alerts exactly once.
		item := sig
		item.Since = in.Now
		item.LastSeen = in.Now
		item.Status = PendingOpen
		if sig.Kind == AlertMonitorDown {
			for _, previous := range in.Prev {
				if previous.Provider == sig.Provider && previous.Kind == AlertReadingMissing && previous.Status == PendingOpen && !previous.NotifiedAt.IsZero() {
					item.NotifiedAt = previous.NotifiedAt
					break
				}
			}
			if !item.NotifiedAt.IsZero() {
				items = append(items, item)
				continue
			}
		}
		if ok && !prev.NotifiedAt.IsZero() && (sig.Kind == AlertQuotaLow || sig.Kind == AlertQuotaExhausted) {
			item.NotifiedAt = prev.NotifiedAt
			items = append(items, item)
			continue
		}
		if alertFor[id] != nil {
			item.NotifiedAt = in.Now
		}
		items = append(items, item)
		opened = append(opened, item)
	}

	// Previous items with no current signal resolve only on positive evidence of
	// recovery; absent evidence is not recovery, so the item stays open.
	for _, prev := range in.Prev {
		if seen[prev.ID] {
			continue
		}
		if prev.Status == PendingResolved {
			items = append(items, prev)
			continue
		}
		if info.resolvedByEvidence(prev) || cfg.EnableAttention && attentionRecovered(prev, in.Report, cfg.TightRemainingPercent, in.Now) {
			item := prev
			item.Status = PendingResolved
			item.ResolvedAt = in.Now
			if item.Kind == AlertQuotaLow || item.Kind == AlertQuotaExhausted {
				item.ResolvedAt = quotaEpisodeEnd(item, in.Report)
			}
			items = append(items, item)
			resolved = append(resolved, item)
			continue
		}
		items = append(items, prev)
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Since.Before(items[j].Since) })

	res := PolicyResult{Items: items, Opened: opened, Resolved: resolved}
	// Newly opened items push once, in a stable order. This is returned whether or
	// not quiet hours are active; the caller persists an alert as a queued
	// notification during quiet instead of sending it.
	sorted := append([]PendingItem(nil), opened...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, item := range sorted {
		if alert := alertFor[item.ID]; alert != nil {
			res.Alerts = append(res.Alerts, *alert)
		}
	}
	return res
}

// Untimed or long-stale missing data retains the established monitor-down grace.
func hasFreshAttempt(rep Report, alert Alert, now time.Time) bool {
	for _, provider := range rep.Providers {
		if provider.Provider != alert.Provider {
			continue
		}
		for _, account := range provider.Accounts {
			if account.Key == alert.Key {
				return observationFresh(account, now, defaultFreshWindow)
			}
		}
	}
	return false
}

// policySignals builds the desired pending items from the report, plus the alert
// to attach to each item the first time it opens.
func policySignals(info reportInfo, now time.Time) (map[string]PendingItem, map[string]*Alert) {
	desired := map[string]PendingItem{}
	alerts := map[string]*Alert{}

	for _, p := range info.providers {
		for _, account := range p.accounts {
			if account.Disabled || p.states[account.Key] != StateInvalid {
				continue
			}
			id := ItemID(AlertCredential, p.provider, account.Key, "")
			desired[id] = PendingItem{
				ID: id, Kind: AlertCredential, Provider: p.provider,
				Key: account.Key, Alias: account.Label,
				Action: reauthAdvice(p.provider),
			}
			alert := credentialAlert(p.provider, account, now)
			alerts[id] = &alert
		}
	}
	for _, p := range info.providers {
		if p.channelUnavailable {
			id := ItemID(AlertChannelUnavailable, p.provider, "", "")
			desired[id] = PendingItem{
				ID: id, Kind: AlertChannelUnavailable, Provider: p.provider,
				Action: "检查该渠道账号额度，必要时等待恢复或更换账号",
			}
			alert := channelAlert(p, now)
			alerts[id] = &alert
		}
	}
	for _, p := range info.providers {
		if p.monitorDown {
			id := ItemID(AlertMonitorDown, p.provider, "", "")
			desired[id] = PendingItem{
				ID: id, Kind: AlertMonitorDown, Provider: p.provider,
				Action: "检查采集链路与上游可达性",
			}
			alert := monitorAlert(p, now)
			alerts[id] = &alert
		}
	}
	return desired, alerts
}

func credentialAlert(provider string, account AccountReport, now time.Time) Alert {
	label := account.Label
	if label == "" {
		label = providerName(provider)
	}
	return Alert{
		Kind:       AlertCredential,
		Severity:   SeverityUrgent,
		Provider:   provider,
		Key:        account.Key,
		Alias:      account.Label,
		Title:      "凭证失效",
		Detail:     label + " 出现明确的认证失败证据，已判定为失效。",
		Advice:     reauthAdvice(provider),
		OccurredAt: now,
	}
}

func channelAlert(p providerInfo, now time.Time) Alert {
	var facts []string
	if p.earliestRecovery != nil {
		facts = append(facts, "最早恢复: "+p.earliestRecovery.Format(time.RFC3339))
	} else {
		facts = append(facts, "无自动恢复时间")
	}
	return Alert{
		Kind:       AlertChannelUnavailable,
		Severity:   SeverityUrgent,
		Provider:   p.provider,
		Title:      providerName(p.provider) + " 渠道不可用",
		Detail:     "该渠道已启用账号全部不可用，最早恢复时间晚于阈值。",
		Facts:      facts,
		Advice:     "检查该渠道账号额度，必要时等待恢复或更换账号",
		OccurredAt: now,
	}
}

func monitorAlert(p providerInfo, now time.Time) Alert {
	facts := []string{"最后有效数据: " + displayTime(p.latestSuccess)}
	return Alert{
		Kind:       AlertMonitorDown,
		Severity:   SeverityWarn,
		Provider:   p.provider,
		Title:      providerName(p.provider) + " 监控停摆",
		Detail:     "该渠道连续超过阈值没有取到有效数据。",
		Facts:      facts,
		Advice:     "检查采集链路与上游可达性",
		OccurredAt: now,
	}
}

// ItemID is the stable pending-item identity: hash(kind, credential key or
// provider, scope).
func ItemID(kind, provider, key, scope string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(provider))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(scope))
	return kind + ":" + strconv.FormatUint(h.Sum64(), 16)
}

// providerInfo is the channel-level reading the policy needs.
type providerInfo struct {
	provider string
	accounts []AccountReport
	states   map[string]string
	// channelUnavailable is true when every enabled account is unusable and the
	// earliest recovery lies beyond the threshold.
	channelUnavailable bool
	// earliestRecovery is the soonest account recovery; nil means no account
	// auto-recovers (or the reset is unknown).
	earliestRecovery *time.Time
	monitorDown      bool
	hasValid         bool
	latestSuccess    time.Time
}

// reportInfo is the per-round projection used for reconciliation.
type reportInfo struct {
	providers   []providerInfo
	present     map[string]bool
	unavailable map[string]bool
	hasValid    map[string]bool
	hasKey      map[string]bool
	unmonitored map[string]bool
	// recoveredKeys are the credentials with positive recovery evidence: a valid
	// reading this round. Absent evidence is not recovery, so an item only resolves
	// on a key that appears here.
	recoveredKeys map[string]bool
}

func analyzeReport(cfg PolicyConfig, rep Report, now time.Time, tracks map[string]ChannelTrack) reportInfo {
	info := reportInfo{
		present:       map[string]bool{},
		unavailable:   map[string]bool{},
		hasValid:      map[string]bool{},
		hasKey:        map[string]bool{},
		unmonitored:   map[string]bool{},
		recoveredKeys: map[string]bool{},
	}
	for _, p := range rep.Providers {
		info.present[p.Provider] = true
		st := map[string]string{}
		pi := providerInfo{provider: p.Provider, accounts: p.Accounts, states: st}
		for _, a := range p.Accounts {
			st[a.Key] = a.State
		}

		enabled, unavail, nonAuthUnavail := 0, 0, 0
		var earliest *time.Time
		authOnly := true
		sawFailure := false
		for _, account := range p.Accounts {
			info.hasKey[account.Key] = true
			if account.Disabled || credentialRecovered(account) {
				// An operator-disabled credential ends the problem, and a valid
				// reading not contradicted by an auth failure is positive proof that
				// a previously auth-broken credential works again.
				info.recoveredKeys[account.Key] = true
			}
			if account.Disabled || account.IsUnmonitored {
				continue
			}
			if isMonitoringTimeValid(account.LastSuccessAt) && account.LastSuccessAt.After(pi.latestSuccess) {
				pi.latestSuccess = account.LastSuccessAt
			}
			enabled++
			if account.OK && observationFresh(account, now, defaultFreshWindow) {
				pi.hasValid = true
			} else {
				sawFailure = true
				if account.State != StateInvalid && !account.AuthFailed {
					authOnly = false
				}
			}
			// An exhausted account reports OK true with a maxed window, so the
			// availability check runs for every enabled account.
			if u, recovery := accountUnavailable(account, now); u {
				unavail++
				if account.State != StateInvalid {
					nonAuthUnavail++
				}
				if recovery != nil {
					earliest = earlierTime(earliest, recovery)
				}
			}
		}

		// A collector-level failure (a channel-wide error with no account at all)
		// is still a channel outage, and it is never auth-only because no
		// credential evidence exists to blame.
		if len(p.Accounts) == 0 && p.Error != "" {
			sawFailure = true
			authOnly = false
		}

		// A channel whose only unavailable accounts are auth-broken is covered by
		// the per-credential credential_invalid alert, not this one.
		if enabled > 0 && unavail == enabled && nonAuthUnavail > 0 {
			pi.channelUnavailable = earliest == nil || earliest.After(now.Add(cfg.ChannelUnavailableAfter))
			pi.earliestRecovery = earliest
		}

		// monitor_down: no valid data, a non-auth failure, and the reference
		// reading is old enough. Use the newest real success (stored or reported),
		// falling back to the first applicable observation only before any success.
		track := tracks[p.Provider]
		if isMonitoringTimeValid(track.LastValid) && track.LastValid.After(pi.latestSuccess) {
			pi.latestSuccess = track.LastValid
		}
		ref := pi.latestSuccess
		if !isMonitoringTimeValid(ref) {
			ref = track.FirstSeen
		}
		tracked := enabled > 0 || (len(p.Accounts) == 0 && p.Error != "")
		if tracked && !pi.hasValid && sawFailure && !authOnly && isMonitoringTimeValid(ref) &&
			now.Sub(ref) > cfg.MonitorDownAfter {
			pi.monitorDown = true
		}

		info.providers = append(info.providers, pi)
		info.unavailable[p.Provider] = pi.channelUnavailable
		info.hasValid[p.Provider] = pi.hasValid
		info.unmonitored[p.Provider] = len(p.Accounts) > 0 && enabled == 0
	}
	return info
}

// resolvedByEvidence reports positive evidence that an open item's condition no
// longer holds. Absent evidence is not recovery, so the item stays open.
func (info reportInfo) resolvedByEvidence(item PendingItem) bool {
	switch item.Kind {
	case AlertCredential:
		if !info.present[item.Provider] || !info.hasKey[item.Key] {
			return false
		}
		// Only a valid reading is positive proof of recovery. A credential that is
		// merely no longer reported invalid (unknown, stale, or absent from the
		// report) is not recovered, so an open item is retained rather than falsely
		// resolved.
		return info.recoveredKeys[item.Key]
	case AlertChannelUnavailable:
		if !info.present[item.Provider] {
			return false
		}
		return !info.unavailable[item.Provider]
	case AlertMonitorDown:
		if !info.present[item.Provider] {
			return false
		}
		return info.hasValid[item.Provider] || info.unmonitored[item.Provider]
	default:
		return false
	}
}

// accountUnavailable decides whether one account is unusable, and when it comes
// back. A model- or group-scoped window running out does NOT make the account
// unusable. nil recovery means no automatic recovery is known.
func accountUnavailable(account AccountReport, now time.Time) (bool, *time.Time) {
	if account.State == StateInvalid {
		return true, nil
	}
	if account.CooldownUntil != nil && account.CooldownUntil.After(now) {
		return true, account.CooldownUntil
	}
	blocked := false
	var reset *time.Time
	for _, window := range account.Windows {
		if window.Scope != ScopeAccount {
			continue
		}
		if window.UsedPercent != nil && *window.UsedPercent >= 100 {
			blocked = true
			if window.ResetAt != nil {
				reset = laterTime(reset, window.ResetAt)
			}
		}
	}
	if blocked {
		return true, reset
	}
	if account.State == StateExhausted {
		return true, nil
	}
	return false, nil
}

// TightChannel reports one channel whose best usable account-level window is
// below the scheduled-digest threshold.
type TightChannel struct {
	Provider      string
	BestRemaining float64
	ResetAt       *time.Time
}

// tightChannels returns the channels that count as tight: the best usable
// account's tightest account-level window is below threshold remaining. Only
// account-level windows are judged; a model/group-only channel is not.
//
// The decision is gated on the reading's real age, not on when the report was
// assembled: an account whose newest reading is older than window is skipped, so
// a stale sample can neither make a channel look nearly out nor make a maxed
// window read as "0 remaining". A zero ObservedAt is unknown and never counts.
func tightChannels(rep Report, threshold float64, now time.Time, window time.Duration) []TightChannel {
	if window <= 0 {
		window = defaultFreshWindow
	}
	var out []TightChannel
	for _, p := range rep.Providers {
		var best *float64
		var bestReset *time.Time
		for _, account := range p.Accounts {
			if account.Disabled || account.IsUnmonitored {
				continue
			}
			if !usableState(account.State) {
				continue
			}
			if !observationFresh(account, now, window) {
				continue
			}
			var minRemaining *float64
			var minReset *time.Time
			for _, window := range account.Windows {
				if window.Scope != ScopeAccount || window.UsedPercent == nil {
					continue
				}
				remaining := 100 - *window.UsedPercent
				if remaining < 0 {
					remaining = 0
				}
				if minRemaining == nil || remaining < *minRemaining {
					value := remaining
					minRemaining, minReset = &value, window.ResetAt
				}
			}
			if minRemaining == nil {
				continue
			}
			if best == nil || *minRemaining > *best {
				best, bestReset = minRemaining, minReset
			}
		}
		if best != nil && *best < threshold {
			out = append(out, TightChannel{Provider: p.Provider, BestRemaining: *best, ResetAt: bestReset})
		}
	}
	return out
}

// observationFresh reports whether an account's newest reading is recent enough
// to decide a threshold. A missing observation time is unknown, never fresh.
func observationFresh(account AccountReport, now time.Time, window time.Duration) bool {
	if !isMonitoringTimeValid(account.ObservedAt) {
		return false
	}
	age := now.Sub(account.ObservedAt)
	if age < 0 {
		age = 0
	}
	return age <= window
}

func isMonitoringTimeValid(observed time.Time) bool {
	return !observed.IsZero() && observed.UnixMilli() > 0
}

// usableState reports an account that can still take traffic. It mirrors the
// renderer so the summary decision and the card agree.
func usableState(state string) bool {
	switch state {
	case StateHealthy, StateLimited, StateNotice, StateWarning:
		return true
	}
	return false
}

// isAbnormal reports whether a state warrants the attention view. It does not
// call a limited model/group window an account-wide failure; the scoped state
// label is retained when rendered.
func isAbnormal(state string) bool {
	switch state {
	case StateHealthy, StateNotice:
		return false
	default:
		return true
	}
}

// credentialRecovered reports positive proof that a previously auth-broken
// credential works again: a valid reading with no unrefuted auth failure. An
// unknown, stale or suspect state is not recovery, so an open item is retained
// rather than falsely closed when the credential merely stops reading as invalid.
func credentialRecovered(account AccountReport) bool {
	if account.AuthFailed || !account.OK {
		return false
	}
	switch account.State {
	case StateInvalid, StateUnknown, StateStale, StateSuspect:
		return false
	}
	return true
}

func earlierTime(a, b *time.Time) *time.Time {
	switch {
	case b == nil:
		return a
	case a == nil:
		return b
	case b.Before(*a):
		return b
	default:
		return a
	}
}

func laterTime(a, b *time.Time) *time.Time {
	switch {
	case b == nil:
		return a
	case a == nil:
		return b
	case b.After(*a):
		return b
	default:
		return a
	}
}

func providerName(provider string) string {
	switch provider {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	case "antigravity":
		return "Antigravity"
	case "gemini-cli":
		return "Gemini CLI"
	case "ollama":
		return "Ollama"
	case "":
		return "未知"
	default:
		return provider
	}
}

func reauthAdvice(provider string) string {
	if provider == "ollama" {
		return "更新 Ollama 的会话 Cookie"
	}
	return "前往 CPA 重新完成登录"
}

func displayTime(t time.Time) string {
	if t.IsZero() {
		return "从未成功"
	}
	return t.Format("2006-01-02 15:04")
}
