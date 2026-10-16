package operations

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
)

// Read-only summaries for the first batch's optional modules.
//
// They exist so the built-in Agent and a read-only MCP caller can see what the
// deployment is monitoring without any write path. Ollama account management and
// Feishu settings are console-only routes behind the operator session; the
// read-only key cannot reach them. Neither summary carries a cookie or an app
// secret.

// OllamaAccountSummary is one monitored Ollama account, without its cookie.
type OllamaAccountSummary struct {
	ID               string          `json:"id"`
	Label            string          `json:"label,omitempty"`
	Enabled          bool            `json:"enabled"`
	Status           string          `json:"status,omitempty"`
	LastSuccessAtMS  *int64          `json:"last_success_at_ms,omitempty"`
	LastObservedAtMS *int64          `json:"last_observed_at_ms,omitempty"`
	NewestError      string          `json:"newest_error,omitempty"`
	NextAttemptAtMS  *int64          `json:"next_attempt_at_ms,omitempty"`
	AttemptCount     int             `json:"attempt_count"`
	HasReading       bool            `json:"has_reading"`
	Quota            json.RawMessage `json:"quota,omitempty"`
}

// OllamaStatus is the Ollama monitoring summary. `collector_enabled` reports the
// process switch, not a CPA credential state: Ollama is not in CPA's control chain.
type OllamaStatus struct {
	CollectorEnabled bool                   `json:"collector_enabled"`
	Accounts         []OllamaAccountSummary `json:"accounts"`
}

// FeishuStatus is the Feishu notification module's read-only state, without the app
// secret.
type FeishuStatus struct {
	Enabled      bool   `json:"enabled"`
	Ready        bool   `json:"ready"`
	Degraded     bool   `json:"degraded"`
	Reason       string `json:"reason,omitempty"`
	Source       string `json:"source"`
	ForcedOff    bool   `json:"forced_off"`
	Connected    bool   `json:"connected"`
	OpenItems    int    `json:"open_items"`
	PendingSends int    `json:"pending_sends"`
	QueuedSends  int    `json:"queued_sends"`
	LastError    string `json:"last_error,omitempty"`
}

func (s *Service) registerOllama(registry *capability.Registry) error {
	return read(registry, "ollama_accounts_status",
		"Read Ollama quota monitoring: whether the collector is enabled and, for each stored account, its label, collection status, newest safe quota reading and retry state. Read-only; the session cookie is never returned.",
		func(ctx context.Context, _ Empty) (OllamaStatus, error) {
			if s.OllamaSummary == nil {
				return OllamaStatus{}, errors.New("capability_unavailable")
			}
			status, err := s.OllamaSummary(ctx)
			if err != nil {
				return OllamaStatus{}, err
			}
			// A low-trust read-only caller sees an email-shaped label masked, the same
			// rule the credential summaries follow. The cookie is never in the summary.
			for index := range status.Accounts {
				status.Accounts[index].Label = accountLabelForCaller(ctx, status.Accounts[index].Label)
			}
			return status, nil
		})
}

func (s *Service) registerFeishu(registry *capability.Registry) error {
	return read(registry, "feishu_notifications_status",
		"Read the Feishu notification module's status: whether it is enabled, ready, degraded or forced off, where its switch comes from, whether the long connection is up, open problems, pending and queued sends, and the last error. Read-only; the app secret is never returned.",
		func(ctx context.Context, _ Empty) (FeishuStatus, error) {
			if s.FeishuSummary == nil {
				return FeishuStatus{}, errors.New("capability_unavailable")
			}
			return s.FeishuSummary(ctx)
		})
}
