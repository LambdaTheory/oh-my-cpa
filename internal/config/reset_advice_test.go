package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadResetAdviceExpiryWindow(t *testing.T) {
	for _, test := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", 72 * time.Hour, false}, {"24h", 24 * time.Hour, false},
		{"0s", 0, true}, {"-1h", 0, true}, {"soon", 0, true},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("OMCPA_RESET_ADVICE_EXPIRY_WINDOW", test.value)
			cfg, err := Load()
			if test.invalid {
				if err == nil || !strings.Contains(err.Error(), "OMCPA_RESET_ADVICE_EXPIRY_WINDOW") {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil || cfg.ResetAdviceExpiryWindow != test.want {
				t.Fatalf("duration=%v error=%v", cfg.ResetAdviceExpiryWindow, err)
			}
		})
	}
}
