package quota

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

// SchedulerWindow is the per-window state the tier planner reads for one
// credential. It is deliberately small: the planner never touches the database.
type SchedulerWindow struct {
	Kind        string
	ResetAtMS   *int64
	UsedPercent *float64
	// HasTrafficInCycle is true when the credential served a request inside this
	// window's current cycle.
	HasTrafficInCycle bool
	// HasRecentEnoughSnapshot is true when the newest snapshot of this window
	// already records at least the minimum used share in the current cycle, so a
	// forced end-of-cycle read would add nothing.
	HasRecentEnoughSnapshot bool
}

// SchedulerState is everything the tier planner needs about one credential.
type SchedulerState struct {
	// RequestsWatchWindow and RequestsNormalWindow count the credential's
	// requests in the watch and normal windows.
	RequestsWatchWindow  int64
	RequestsNormalWindow int64
	// LastSnapshotAtMS is when the newest snapshot was taken.
	LastSnapshotAtMS int64
	// LatestPassiveAtMS is the newest passive observation that covers a standard
	// window, or zero when there is none.
	LatestPassiveAtMS int64
	Windows           []SchedulerWindow
}

// SchedulerConfig is the resolved tier rules.
type SchedulerConfig struct {
	WatchInterval        time.Duration
	ActiveInterval       time.Duration
	NormalInterval       time.Duration
	IdleInterval         time.Duration
	WatchUsedPercent     float64
	WatchWindow          time.Duration
	NormalWindow         time.Duration
	WindowEndMargin      time.Duration
	PassiveFreshInterval time.Duration
	// CredentialFloor is the fallback shortest time between two active reads of
	// the same credential. CredentialFloorByProvider overrides it for the
	// providers it names.
	CredentialFloor time.Duration
	// CredentialFloorByProvider holds a provider-specific floor keyed by the
	// normalized provider key. A provider absent from the map uses
	// CredentialFloor.
	CredentialFloorByProvider map[string]time.Duration
	GlobalConcurrency         int
	ResetGrace                time.Duration
	ResetLookback             time.Duration
}

// CredentialFloorFor returns the shortest time between two active reads of a
// credential of the given provider. A provider with its own floor uses it; every
// other provider, including an unknown one, uses the shared fallback.
func (c SchedulerConfig) CredentialFloorFor(provider string) time.Duration {
	if floor, ok := c.CredentialFloorByProvider[normalizeProviderKey(provider)]; ok && floor > 0 {
		return floor
	}
	return c.CredentialFloor
}

// normalizeProviderKey lowercases and trims a provider name so a configured floor
// matches the key DetectProvider produces.
func normalizeProviderKey(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// RefreshPlan is the planner's decision for one credential.
type RefreshPlan struct {
	ShouldRefresh bool
	Interval      time.Duration
	Reason        string
}

// PlanRefresh is the pure tier rule. It returns the interval the credential is
// currently on and whether that interval has elapsed.
//
// The order of the rules is the order of their urgency: a watched credential
// outranks an active one, an active one outranks a normal one, and an idle one
// is the slowest. A fresh passive observation widens the interval to the passive
// floor, because the credential's own traffic already keeps a standard window
// current and the active probe is only there for the model-scoped windows and
// the plan. A window within WindowEndMargin of its reset, with traffic this
// cycle and no recorded reading yet, forces a read so the next cycle has a
// previous-cycle figure to estimate from. The provider's own credential floor
// (CredentialFloorFor) caps how often any of these rules may read.
func PlanRefresh(state SchedulerState, cfg SchedulerConfig, provider string, now time.Time, lastRefresh time.Time) RefreshPlan {
	interval := cfg.IdleInterval
	switch {
	case state.RequestsWatchWindow > 0:
		if maxUsedPercent(state.Windows) >= cfg.WatchUsedPercent {
			interval = cfg.WatchInterval
		} else {
			interval = cfg.ActiveInterval
		}
	case state.RequestsNormalWindow > 0:
		interval = cfg.NormalInterval
	}
	if state.LatestPassiveAtMS > state.LastSnapshotAtMS && state.LatestPassiveAtMS > 0 &&
		now.Sub(time.UnixMilli(state.LatestPassiveAtMS)) <= interval {
		interval = max(interval, cfg.PassiveFreshInterval)
	}

	sinceLast := now.Sub(lastRefresh)
	if sinceLast < cfg.CredentialFloorFor(provider) {
		return RefreshPlan{Interval: interval}
	}

	if windowEndDue(state.Windows, cfg, now) {
		return RefreshPlan{ShouldRefresh: true, Interval: interval, Reason: "window_end"}
	}
	if lastRefresh.IsZero() || sinceLast >= interval {
		return RefreshPlan{ShouldRefresh: true, Interval: interval, Reason: "tier"}
	}
	return RefreshPlan{Interval: interval}
}

func windowEndDue(windows []SchedulerWindow, cfg SchedulerConfig, now time.Time) bool {
	for _, window := range windows {
		if window.ResetAtMS == nil || *window.ResetAtMS <= 0 {
			continue
		}
		resetAt := time.UnixMilli(*window.ResetAtMS)
		if resetAt.Before(now) || resetAt.Sub(now) > cfg.WindowEndMargin {
			continue
		}
		if !window.HasTrafficInCycle || window.HasRecentEnoughSnapshot {
			continue
		}
		return true
	}
	return false
}

func maxUsedPercent(windows []SchedulerWindow) float64 {
	highest := 0.0
	for _, window := range windows {
		if window.UsedPercent == nil {
			continue
		}
		if *window.UsedPercent > highest {
			highest = *window.UsedPercent
		}
	}
	return highest
}

// Scheduler actively reads each refreshable credential's quota on a per-account
// cadence. Its callbacks are the console's own reads and writes, so the
// scheduler adds no second implementation of any of them.
type Scheduler struct {
	// Now is injectable so tests do not sleep.
	Now     func() time.Time
	Config  SchedulerConfig
	Logger  *slog.Logger
	Backoff *Backoff

	ListFiles func(ctx context.Context) ([]management.AuthFile, error)
	// RequestCounts returns, per auth_index, the number of requests since the
	// given instant. It is called once per tick per window, never per credential.
	RequestCounts func(ctx context.Context, sinceMS int64) (map[string]int64, error)
	// State gathers one credential's windows and passive freshness.
	State func(ctx context.Context, file management.AuthFile, prior *NormalizedQuota, requestsWatch, requestsNormal int64) (SchedulerState, error)
	Prior func(ctx context.Context, authIndex string) *NormalizedQuota
	// Refresh performs the active read; Persist stores the snapshot and records
	// the active observations, exactly as a manual refresh does.
	Refresh func(ctx context.Context, file management.AuthFile, prior *NormalizedQuota) (*NormalizedQuota, error)
	Persist func(ctx context.Context, value *NormalizedQuota) error

	// evaluationInterval is how often the loop wakes; tests shorten it.
	evaluationInterval time.Duration

	mu        sync.Mutex
	lastQuery map[string]time.Time
	resetSeen map[string]bool
}

// NewScheduler builds a scheduler with the given rules and backoff registry.
func NewScheduler(cfg SchedulerConfig, logger *slog.Logger, backoff *Backoff) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.GlobalConcurrency < 1 {
		cfg.GlobalConcurrency = 1
	}
	if cfg.ResetGrace <= 0 {
		cfg.ResetGrace = time.Minute
	}
	if cfg.ResetLookback <= 0 {
		cfg.ResetLookback = time.Hour
	}
	return &Scheduler{
		Now:                time.Now,
		Config:             cfg,
		Logger:             logger,
		Backoff:            backoff,
		evaluationInterval: time.Minute,
		lastQuery:          map[string]time.Time{},
		resetSeen:          map[string]bool{},
	}
}

// Run evaluates the fleet every minute until the context is cancelled. The first
// evaluation runs immediately, so a restart does not wait a full minute.
func (s *Scheduler) Run(ctx context.Context) error {
	s.runOnce(ctx)
	ticker := time.NewTicker(s.evaluationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

// runOnce evaluates every credential once. It is not concurrent with itself: Run
// waits for a tick's work before sleeping again.
func (s *Scheduler) runOnce(ctx context.Context) {
	if s.ListFiles == nil || s.Refresh == nil {
		return
	}
	files, err := s.ListFiles(ctx)
	if err != nil {
		s.Logger.Warn("quota scheduler: credential list failed", "error", err)
		return
	}
	now := s.Now()
	watchCounts, normalCounts := map[string]int64{}, map[string]int64{}
	if s.RequestCounts != nil {
		if counts, err := s.RequestCounts(ctx, now.Add(-s.Config.WatchWindow).UnixMilli()); err == nil {
			watchCounts = counts
		} else {
			s.Logger.Warn("quota scheduler: recent-request count failed", "error", err)
		}
		if counts, err := s.RequestCounts(ctx, now.Add(-s.Config.NormalWindow).UnixMilli()); err == nil {
			normalCounts = counts
		} else {
			s.Logger.Warn("quota scheduler: request count failed", "error", err)
		}
	}

	semaphore := make(chan struct{}, s.Config.GlobalConcurrency)
	var wait sync.WaitGroup
	for _, file := range files {
		authIndex := strings.TrimSpace(file.AuthIndex)
		if authIndex == "" || file.Disabled {
			continue
		}
		if !CapabilitiesForFile(file).RefreshSupported {
			continue
		}
		provider := DetectProvider(file.Type, file.Provider)
		if blocked, until := s.Backoff.Blocked(provider, authIndex, now); blocked {
			s.Logger.Debug("quota scheduler: credential parked", "provider", provider, "auth_index", authIndex, "until", until)
			continue
		}
		prior := s.Prior(ctx, authIndex)
		state, err := s.State(ctx, file, prior, watchCounts[authIndex], normalCounts[authIndex])
		if err != nil {
			s.Logger.Warn("quota scheduler: state read failed", "auth_index", authIndex, "error", err)
			continue
		}
		plan := PlanRefresh(state, s.Config, provider, now, s.lastQueryAt(authIndex))
		resetKey, resetDue := s.resetDue(authIndex, provider, state, now)
		if !plan.ShouldRefresh && !resetDue {
			continue
		}
		if plan.ShouldRefresh && now.Sub(s.lastQueryAt(authIndex)) < s.Config.CredentialFloorFor(provider) {
			continue
		}
		wait.Add(1)
		semaphore <- struct{}{}
		go func(file management.AuthFile, provider, authIndex, resetKey string, prior *NormalizedQuota, isReset bool) {
			defer wait.Done()
			defer func() { <-semaphore }()
			unlock := s.Backoff.LockProvider(provider)
			defer unlock()
			s.query(ctx, file, provider, authIndex, prior)
			if isReset {
				s.markReset(resetKey)
			}
		}(file, provider, authIndex, resetKey, prior, resetDue)
	}
	wait.Wait()
}

func (s *Scheduler) query(ctx context.Context, file management.AuthFile, provider, authIndex string, prior *NormalizedQuota) {
	now := s.Now()
	if blocked, _ := s.Backoff.Blocked(provider, authIndex, now); blocked {
		return
	}
	if since := now.Sub(s.lastQueryAt(authIndex)); since < s.Config.CredentialFloorFor(provider) {
		return
	}
	s.markQuery(authIndex, now)
	value, err := s.Refresh(ctx, file, prior)
	if err != nil {
		if rateErr := rateLimitFrom(err); rateErr != nil {
			s.Backoff.Record(provider, authIndex, rateErr.RetryAfter, rateErr.HasRetryAfter, now)
			s.Logger.Warn("quota scheduler: upstream rate limited", "provider", provider, "auth_index", authIndex, "retry_after", rateErr.RetryAfter)
			return
		}
		s.Logger.Warn("quota scheduler: refresh failed", "provider", provider, "auth_index", authIndex, "error", err)
		return
	}
	s.Backoff.Clear(provider, authIndex)
	if value != nil && s.Persist != nil {
		if persistErr := s.Persist(ctx, value); persistErr != nil {
			s.Logger.Warn("quota scheduler: snapshot write failed", "auth_index", authIndex, "error", persistErr)
		}
	}
}

func (s *Scheduler) lastQueryAt(authIndex string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastQuery[authIndex]
}

func (s *Scheduler) markQuery(authIndex string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastQuery[authIndex] = at
}

// resetDue reports whether a window's reset has passed recently and has not been
// followed by a read yet. The key is scoped to the credential and the exact reset
// instant, so each reset is read once and a later cycle is not mistaken for it.
func (s *Scheduler) resetDue(authIndex, provider string, state SchedulerState, now time.Time) (string, bool) {
	if now.Sub(s.lastQueryAt(authIndex)) < s.Config.CredentialFloorFor(provider) {
		return "", false
	}
	for _, window := range state.Windows {
		if window.ResetAtMS == nil || *window.ResetAtMS <= 0 {
			continue
		}
		resetAt := time.UnixMilli(*window.ResetAtMS)
		if now.Before(resetAt.Add(s.Config.ResetGrace)) || now.Sub(resetAt) > s.Config.ResetLookback {
			continue
		}
		key := authIndex + "|" + window.Kind + "|" + strconv.FormatInt(*window.ResetAtMS, 10)
		if s.hasReset(key) {
			continue
		}
		return key, true
	}
	return "", false
}

func (s *Scheduler) hasReset(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resetSeen[key]
}

func (s *Scheduler) markReset(key string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetSeen[key] = true
}
