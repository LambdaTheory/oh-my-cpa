package app

import (
	"context"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/config"
)

func TestRunQuotaRefreshModeStartsOnlyTheSelectedLoop(t *testing.T) {
	for _, test := range []struct {
		name         string
		mode         config.QuotaSchedulerMode
		wantInterval int
		wantAdaptive int
	}{
		{name: "interval", mode: config.QuotaSchedulerModeInterval, wantInterval: 1},
		{name: "adaptive", mode: config.QuotaSchedulerModeAdaptive, wantAdaptive: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			intervalCalls, adaptiveCalls := 0, 0
			err := runQuotaRefreshMode(context.Background(), test.mode,
				func(context.Context) error { intervalCalls++; return nil },
				func(context.Context) error { adaptiveCalls++; return nil },
			)
			if err != nil || intervalCalls != test.wantInterval || adaptiveCalls != test.wantAdaptive {
				t.Fatalf("mode %q: interval=%d adaptive=%d err=%v", test.mode, intervalCalls, adaptiveCalls, err)
			}
		})
	}
}
