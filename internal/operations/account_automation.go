package operations

import (
	"context"
	"errors"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
)

// Account-level auto-management, read-only.
//
// The status is a summary of what OMC currently plans for each adopted account.
// It carries no credential material, no auth index-to-secret mapping and no write
// path: automation is controlled only from the console's session-authenticated
// routes, which the read-only MCP key cannot reach.
type AutomationAccountSummary struct {
	AuthIndex            string   `json:"auth_index"`
	Provider             string   `json:"provider,omitempty"`
	Name                 string   `json:"name,omitempty"`
	State                string   `json:"state"`
	MonitorReason        string   `json:"monitor_reason,omitempty"`
	IsEnabled            bool     `json:"is_enabled"`
	DisabledByAutomation bool     `json:"disabled_by_automation"`
	RecoveryAtMS         *int64   `json:"recovery_at_ms,omitempty"`
	HasWeeklyEvidence    bool     `json:"has_weekly_evidence"`
	WeeklyUsedPercent    *float64 `json:"weekly_used_percent,omitempty"`
}

type AutomationStatus struct {
	Enabled  bool                       `json:"enabled"`
	Source   string                     `json:"source"`
	Accounts []AutomationAccountSummary `json:"accounts"`
}

// registerAccountAutomation exposes the summary read. It is declared read/low so
// the read-only MCP key reaches it and it can be offered to the agent from the
// first round; the write routes remain console-only.
func (s *Service) registerAccountAutomation(registry *capability.Registry) error {
	return read(registry, "account_automation_status",
		"Read account-level auto-management: the resolved master switch and, for each adopted credential, its plan (state, why it has no automatic control, whether it is disabled by OMC, the scheduled recovery instant, and the newest standard weekly reading). Read-only summary without credential material.",
		func(ctx context.Context, _ Empty) (AutomationStatus, error) {
			if s.AutomationSummary == nil {
				return AutomationStatus{}, errors.New("capability_unavailable")
			}
			return s.AutomationSummary(ctx)
		})
}
