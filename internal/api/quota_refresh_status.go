package api

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/authfailure"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

type quotaRefreshAttemptDTO struct {
	Outcome       string `json:"outcome"`
	AttemptedAtMS int64  `json:"attempted_at_ms"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	RetryAtMS     int64  `json:"retry_at_ms,omitempty"`
	Reason        string `json:"reason,omitempty"`
	ReasonSummary string `json:"reason_summary,omitempty"`
}

// Providers already normalize HTTP errors to this prefix. Persist the code, never
// the accompanying upstream message, which is outside the facade's allowlist.
var QUOTA_HTTP_STATUS = regexp.MustCompile(`\bHTTP ([45][0-9]{2})\b`)

// Non-rate-limit provider errors reach this layer as normalized text. Only the
// transport and parser vocabulary we emit is classified; other text stays unknown.
func quotaRefreshReason(err error, message string, status int) string {
	evidence := authfailure.FromText(message)
	var typed *authfailure.Evidence
	if errors.As(err, &typed) {
		evidence = typed
		message = typed.Message
	}
	if evidence.NeedsReauth() {
		return "auth"
	}
	if status >= 400 && status <= 599 {
		return "http"
	}
	message = strings.ToLower(message)
	var networkErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkErr) && networkErr.Timeout()) || strings.Contains(message, "context deadline exceeded") || strings.Contains(message, "timeout") || strings.Contains(message, "timed out") {
		return "timeout"
	}
	if networkErr != nil || strings.Contains(message, "dial tcp") || strings.Contains(message, "connection refused") || strings.Contains(message, "connection reset") || strings.Contains(message, "no such host") {
		return "network"
	}
	if strings.Contains(message, "invalid ") && strings.Contains(message, "payload:") || strings.Contains(message, "decode ") || strings.Contains(message, "unmarshal") {
		return "parse"
	}
	return "unknown"
}

func quotaRefreshSummary(message string) string {
	// Redact before truncation so a cut token cannot evade the shared redactor.
	summary := []rune(strings.Join(strings.Fields(security.RedactText(message)), " "))
	if len(summary) > 240 {
		summary = append(summary[:239], '…')
	}
	return string(summary)
}

func (h *Handler) recordQuotaRefreshAttempt(ctx context.Context, file management.AuthFile, value *quota.NormalizedQuota, refreshErr error) error {
	if h.repo == nil || file.Disabled {
		return nil
	}
	attempt := repository.QuotaRefreshAttempt{AuthIndex: file.AuthIndex, Outcome: "success", AttemptedAtMS: h.now().UnixMilli()}
	var rateErr *quota.RateLimitError
	switch {
	case errors.As(refreshErr, &rateErr):
		attempt.Outcome, attempt.HTTPStatus = "rate_limited", 429
		attempt.Reason = "http"
		attempt.RetryAtMS = h.now().Add(rateErr.RetryAfter).UnixMilli()
		if blocked, until := h.quotaBackoffBlocked(quota.DetectProvider(file.Type, file.Provider), file.AuthIndex); blocked {
			attempt.RetryAtMS = until.UnixMilli()
		}
	case refreshErr != nil || value == nil || value.Error != "" || value.Status == "error" || value.Status == "stale":
		attempt.Outcome = "failed"
		reason := ""
		if refreshErr != nil {
			reason = refreshErr.Error()
		} else if value != nil {
			reason = value.Error
			if value.Failure != nil {
				refreshErr = value.Failure
			}
		}
		if match := QUOTA_HTTP_STATUS.FindStringSubmatch(reason); len(match) == 2 {
			attempt.HTTPStatus, _ = strconv.Atoi(match[1])
		}
		attempt.Reason = quotaRefreshReason(refreshErr, reason, attempt.HTTPStatus)
		if attempt.Reason == "unknown" {
			attempt.ReasonSummary = quotaRefreshSummary(reason)
		}
	}
	return h.repo.SaveQuotaRefreshAttempt(ctx, attempt)
}

func projectQuotaRefreshAttempt(attempt repository.QuotaRefreshAttempt) *quotaRefreshAttemptDTO {
	if attempt.AttemptedAtMS == 0 {
		return nil
	}
	reason := attempt.Reason
	if attempt.Outcome != "success" {
		switch reason {
		case "timeout", "network", "auth", "http", "parse", "unknown":
		default:
			reason = quotaRefreshReason(nil, "", attempt.HTTPStatus)
		}
	}
	return &quotaRefreshAttemptDTO{
		Outcome: attempt.Outcome, AttemptedAtMS: attempt.AttemptedAtMS,
		HTTPStatus: attempt.HTTPStatus, RetryAtMS: attempt.RetryAtMS,
		Reason: reason, ReasonSummary: quotaRefreshSummary(attempt.ReasonSummary),
	}
}
