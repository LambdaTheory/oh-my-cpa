package feishu

import (
	"context"
	"sync"
	"time"
)

// Retention and bound constants for the persisted runtime state.
const (
	// maxOutboxPerChannel bounds the outbox so a permanently broken channel
	// cannot grow the store without limit. The oldest sent rows are dropped
	// first; pending rows are never dropped.
	maxOutboxPerChannel = 200
	// sentRetention is how long a delivered notification is kept as an
	// acknowledgement before it is pruned.
	sentRetention = 24 * time.Hour
	// resolvedItemRetention is how long a resolved pending item is kept before it
	// is pruned. Keeping it briefly lets a digest show that a problem was fixed.
	resolvedItemRetention = 7 * 24 * time.Hour
)

// State is the whole persisted runtime document. It is deliberately secret-free:
// it carries opaque credential keys, display aliases and rendered-alert data, but
// never a cookie, token or app secret.
type State struct {
	// PendingItems are the structured open/resolved problems.
	PendingItems []PendingItem
	// Outbox is the delivery queue plus recently sent rows (the acknowledgements).
	// A notification stays here until delivery is confirmed.
	Outbox []Notification
	// ChannelTracks backs monitor_down: the last-valid-data time per provider.
	ChannelTracks map[string]ChannelTrack
	// Bootstrapped records that the one-time startup summary has been sent. It is
	// persisted so a settings reload does not resend it.
	Bootstrapped bool
	// LastSummaryAt is when the newest scheduled digest was sent.
	LastSummaryAt time.Time
	UpdatedAt     time.Time
}

// Store loads and saves the runtime state. The integration owner implements it
// over OMC's SQLite repository; MemoryStore backs the tests.
type Store interface {
	Load(ctx context.Context) (*State, error)
	Save(ctx context.Context, state *State) error
}

// EmptyState returns a valid, empty state with initialised maps.
func EmptyState() *State {
	return &State{ChannelTracks: map[string]ChannelTrack{}}
}

// pendingByID returns the item with id and whether it was found.
func (s *State) pendingByID(id string) (PendingItem, bool) {
	if s == nil {
		return PendingItem{}, false
	}
	for _, item := range s.PendingItems {
		if item.ID == id {
			return item, true
		}
	}
	return PendingItem{}, false
}

// queueNotification appends a notification to the outbox, trimming the oldest
// sent rows when the outbox grows past its bound. Pending and queued rows are
// never dropped.
func (s *State) queueNotification(notification Notification) {
	if s == nil || notification.ID == "" {
		return
	}
	if notification.State == "" {
		notification.State = NotifyPending
	}
	for i := range s.Outbox {
		if s.Outbox[i].ID != notification.ID {
			continue
		}
		// Preserve delivery progress when the same notification is re-queued.
		existing := s.Outbox[i]
		notification.Attempts = existing.Attempts
		notification.SentAt = existing.SentAt
		s.Outbox[i] = notification
		return
	}
	s.Outbox = append(s.Outbox, notification)
	s.trimOutbox()
}

// trimOutbox bounds the outbox by dropping the oldest sent rows first.
func (s *State) trimOutbox() {
	if len(s.Outbox) <= maxOutboxPerChannel {
		return
	}
	kept := make([]Notification, 0, len(s.Outbox))
	overflow := len(s.Outbox) - maxOutboxPerChannel
	for _, notification := range s.Outbox {
		if overflow > 0 && notification.State == NotifySent {
			overflow--
			continue
		}
		kept = append(kept, notification)
	}
	s.Outbox = kept
}

// takeQueued moves every queued (quiet-hours) notification back to pending so the
// quiet-end merge sends them, and returns them. A queued notification that has
// since recovered is expected to have been dropped by the policy before this is
// called.
func (s *State) takeQueued() []Notification {
	if s == nil {
		return nil
	}
	var out []Notification
	for i := range s.Outbox {
		if s.Outbox[i].State == NotifyQueued {
			s.Outbox[i].State = NotifyPending
			out = append(out, s.Outbox[i])
		}
	}
	return out
}

// markSent stamps a notification as delivered.
func (s *State) markSent(id string, at time.Time) {
	if s == nil {
		return
	}
	for i := range s.Outbox {
		if s.Outbox[i].ID == id {
			s.Outbox[i].State = NotifySent
			s.Outbox[i].SentAt = at
			return
		}
	}
}

// prune drops resolved items and sent acknowledgements past their retention. Open
// items and undelivered notifications are never pruned.
func (s *State) prune(now time.Time) {
	if s == nil {
		return
	}
	items := s.PendingItems[:0]
	for _, item := range s.PendingItems {
		origin := item.ResolvedAt
		if origin.IsZero() {
			origin = item.LastSeen
			if origin.IsZero() {
				origin = item.Since
			}
			if origin.IsZero() {
				origin = item.NotifiedAt
			}
			if origin.IsZero() {
				origin = now
			}
		}
		if item.Status == PendingResolved && item.ResolvedAt.IsZero() && item.Kind != predictionKindPrefix {
			item.ResolvedAt = origin
		}
		if item.CycleResetAt.After(origin) {
			origin = item.CycleResetAt
		}
		if item.Status == PendingResolved && now.Sub(origin) > resolvedItemRetention {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		s.PendingItems = nil
	} else {
		s.PendingItems = items
	}

	outbox := s.Outbox[:0]
	for _, notification := range s.Outbox {
		if notification.State == NotifySent && !notification.SentAt.IsZero() && now.Sub(notification.SentAt) > sentRetention {
			continue
		}
		outbox = append(outbox, notification)
	}
	if len(outbox) == 0 {
		s.Outbox = nil
	} else {
		s.Outbox = outbox
	}
}

// MemoryStore is an in-memory Store for tests and for a process that has not yet
// been wired to a repository.
type MemoryStore struct {
	mu    sync.Mutex
	state *State
}

// NewMemoryStore builds a store starting from EmptyState.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: EmptyState()}
}

// Load returns a deep copy of the stored state.
func (m *MemoryStore) Load(_ context.Context) (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneState(m.state), nil
}

// Save replaces the stored state with a deep copy of state.
func (m *MemoryStore) Save(_ context.Context, state *State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state == nil {
		return nil
	}
	m.state = cloneState(state)
	return nil
}

// cloneState deep-copies the slices and maps so a caller cannot mutate the store
// through a returned pointer.
func cloneState(state *State) *State {
	if state == nil {
		return EmptyState()
	}
	out := &State{
		Bootstrapped:  state.Bootstrapped,
		LastSummaryAt: state.LastSummaryAt,
		UpdatedAt:     state.UpdatedAt,
	}
	out.PendingItems = append([]PendingItem(nil), state.PendingItems...)
	out.Outbox = make([]Notification, len(state.Outbox))
	for i, notification := range state.Outbox {
		out.Outbox[i] = notification
		out.Outbox[i].ItemIDs = append([]string(nil), notification.ItemIDs...)
		out.Outbox[i].Pending = append([]PendingItem(nil), notification.Pending...)
		out.Outbox[i].Tight = append([]TightChannel(nil), notification.Tight...)
		out.Outbox[i].Alerts = append([]Alert(nil), notification.Alerts...)
		for j := range out.Outbox[i].Alerts {
			out.Outbox[i].Alerts[j].Facts = append([]string(nil), notification.Alerts[j].Facts...)
		}
	}
	if state.ChannelTracks != nil {
		out.ChannelTracks = make(map[string]ChannelTrack, len(state.ChannelTracks))
		for key, track := range state.ChannelTracks {
			out.ChannelTracks[key] = track
		}
	} else {
		out.ChannelTracks = map[string]ChannelTrack{}
	}
	return out
}
