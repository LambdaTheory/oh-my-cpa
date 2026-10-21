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
// A fresh successful quota read supersedes old account status, never its model
// restrictions; a model restriction never clears an account authentication error.
func ApplyCredentialStatus(q *NormalizedQuota, file management.AuthFile, nowMS int64) {
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
		if scope != "model" && !q.HasAuthenticatedRead && q.Failure == nil {
			evidence := &authfailure.Evidence{Source: authfailure.CredentialStatus, HTTPStatus: raw.HTTPStatus, Reason: raw.Reason}
			if evidence.NeedsReauth() {
				q.Failure = evidence
				q.Error = evidence.Error()
			}
		}
	}
	if q.HasAuthenticatedRead || q.Failure != nil || q.Error != "" {
		return
	}
	// An active file may carry model failure text. Only an account error state
	// admits the legacy status-message fallback.
	if file.Status == "error" || file.Unavailable {
		evidence := authfailure.FromText(file.StatusMessage)
		if strings.TrimSpace(file.StatusMessage) != "" {
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
