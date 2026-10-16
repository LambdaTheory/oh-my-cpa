package automation

import (
	"context"
	"log/slog"
	"time"
)

// Manager runs the account auto-management pass on its own cadence. It is
// best-effort like the other background loops: losing it leaves plans in the
// database for the next pass, and it never breaks request capture or the HTTP
// server.
type Manager struct {
	Service  *Service
	Interval time.Duration
	Logger   *slog.Logger
}

// Run evaluates once immediately, so a restart resumes its plans at once, then
// every Interval until the context is cancelled. It never sleeps in a test: the
// interval is injected and the pass itself is clock-driven.
func (m *Manager) Run(ctx context.Context) error {
	if m == nil || m.Service == nil {
		return nil
	}
	interval := m.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("account auto-management started", "interval", interval.String())
	run := func() {
		if err := ctx.Err(); err != nil {
			return
		}
		m.Service.RunOnce(ctx)
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			run()
		}
	}
}
