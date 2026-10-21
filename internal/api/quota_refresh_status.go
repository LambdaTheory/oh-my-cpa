package api

import (
	"context"
	"errors"
	"regexp"
	"strconv"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type quotaRefreshAttemptDTO struct {
	Outcome       string `json:"outcome"`
	AttemptedAtMS int64  `json:"attempted_at_ms"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	RetryAtMS     int64  `json:"retry_at_ms,omitempty"`
}

// Providers already normalize HTTP errors to this prefix. Persist the code, never
// the accompanying upstream message, which is outside the facade's allowlist.
var QUOTA_HTTP_STATUS = regexp.MustCompile(`\bHTTP ([45][0-9]{2})\b`)

func (h *Handler) recordQuotaRefreshAttempt(ctx context.Context, file management.AuthFile, value *quota.NormalizedQuota, refreshErr error) error {
	if h.repo == nil || file.Disabled {
		return nil
	}
	attempt := repository.QuotaRefreshAttempt{AuthIndex: file.AuthIndex, Outcome: "success", AttemptedAtMS: h.now().UnixMilli()}
	var rateErr *quota.RateLimitError
	switch {
	case errors.As(refreshErr, &rateErr):
		attempt.Outcome, attempt.HTTPStatus = "rate_limited", 429
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
		}
		if match := QUOTA_HTTP_STATUS.FindStringSubmatch(reason); len(match) == 2 {
			attempt.HTTPStatus, _ = strconv.Atoi(match[1])
		}
	}
	return h.repo.SaveQuotaRefreshAttempt(ctx, attempt)
}

func projectQuotaRefreshAttempt(attempt repository.QuotaRefreshAttempt) *quotaRefreshAttemptDTO {
	if attempt.AttemptedAtMS == 0 {
		return nil
	}
	return &quotaRefreshAttemptDTO{attempt.Outcome, attempt.AttemptedAtMS, attempt.HTTPStatus, attempt.RetryAtMS}
}
