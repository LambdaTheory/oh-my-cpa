package quota

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/authfailure"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

// ApplyCredentialStatus keeps CPA's model cooldowns beside the account outcome.
// A live successful read supersedes the CPA status fetched before that read.
// Persisted acceptance supersedes undated or older CPA status; only a later CPA
// update can reject it. Model restrictions remain independent of authentication.
func ApplyCredentialStatus(q *NormalizedQuota, file management.AuthFile, nowMS int64) {
	canApplyAccountStatus := !q.HasAuthenticatedRead && (q.LastAuthenticatedReadAtMS <= 0 || cooldownRetryAt(file.UpdatedAt) > q.LastAuthenticatedReadAtMS)
	for _, raw := range file.Cooldowns {
		retry := cooldownRetryAt(raw.RetryAt)
		if retry > 0 && retry <= nowMS {
			continue
		}
		scope := raw.Scope
		if scope != "model" && scope != "credential" && scope != "account" {
			continue
		}
		entry := CredentialCooldown{Scope: scope, ModelKey: security.RedactText(raw.ModelKey), Reason: security.RedactText(raw.Reason), HTTPStatus: raw.HTTPStatus, RetryAtMS: retry}
		q.Cooldowns = append(q.Cooldowns, entry)
		if q.ActiveCooldown == nil {
			q.ActiveCooldown = &ActiveCooldown{IsActive: true, Reason: entry.Reason}
			if retry > 0 {
				q.ActiveCooldown.RecoverAtMS = &retry
			}
		}
		if scope != "model" && canApplyAccountStatus {
			evidence := &authfailure.Evidence{Source: authfailure.CredentialStatus, HTTPStatus: raw.HTTPStatus, Reason: raw.Reason}
			if evidence.NeedsReauth() {
				q.Failure = evidence
				q.Error = evidence.Error()
			}
		}
	}
	if !canApplyAccountStatus {
		return
	}
	// An active file may carry model failure text. Only an account error state
	// admits the legacy status-message fallback.
	if file.Status == "error" || file.Unavailable {
		evidence := authfailure.FromText(file.StatusMessage)
		if evidence.NeedsReauth() || (q.Failure == nil && q.Error == "" && strings.TrimSpace(file.StatusMessage) != "") {
			q.Failure = evidence
			q.Error = evidence.Error()
		}
	}
}

func cooldownRetryAt(raw json.RawMessage) int64 {
	text := strings.Trim(string(raw), "\"")
	if instant, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return instant.UnixMilli()
	}
	if value, err := strconv.ParseInt(text, 10, 64); err == nil && value > 0 {
		if value < 100000000000 {
			return value * 1000
		}
		return value
	}
	return 0
}
