package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

func TestQuotaRefreshReasonsPersistWithoutRawErrors(t *testing.T) {
	handler := newQuotaAutoRefreshHandler(t, &quotaAutoRefreshFixture{reads: map[string]int{}, isSingle: true})
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		err     error
		message string
		want    string
	}{
		{"typed-timeout", context.DeadlineExceeded, "", "timeout"},
		{"normalized-timeout", nil, "CPA request failed: context deadline exceeded", "timeout"},
		{"network", nil, "dial tcp: connection refused", "network"},
		{"parse", nil, "invalid codex payload: unexpected end of JSON input", "parse"},
		{"auth", nil, "HTTP 401", "auth"},
		{"forbidden", nil, "HTTP 403 Forbidden", "http"},
		{"management", nil, "CPA returned HTTP 401", "http"},
		{"expired", nil, "OAuth access token has expired", "auth"},
		{"http", nil, "HTTP 483", "http"},
		{"unknown", errors.New("provider rejected request token=synthetic-sensitive-value " + strings.Repeat("detail ", 60)), "", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := management.AuthFile{AuthIndex: tc.name}
			value := &quota.NormalizedQuota{Status: "error", Error: tc.message}
			if err := handler.recordQuotaRefreshAttempt(ctx, file, value, tc.err); err != nil {
				t.Fatal(err)
			}
			attempts, err := handler.repo.GetQuotaRefreshAttempts(ctx, []string{file.AuthIndex})
			if err != nil {
				t.Fatal(err)
			}
			attempt := attempts[file.AuthIndex]
			dto := projectQuotaRefreshAttempt(attempt)
			if dto.Reason != tc.want || dto.Outcome != "failed" {
				t.Fatalf("reason = %+v", dto)
			}
			if strings.Contains(attempt.ReasonSummary, "synthetic-sensitive-value") || len([]rune(dto.ReasonSummary)) > 240 {
				t.Fatal("unsafe summary")
			}
			if tc.want == "unknown" && !strings.Contains(dto.ReasonSummary, "[redacted]") {
				t.Fatalf("missing redacted summary: %q", dto.ReasonSummary)
			}
			if err := handler.recordQuotaRefreshAttempt(ctx, file, &quota.NormalizedQuota{Status: "healthy"}, nil); err != nil {
				t.Fatal(err)
			}
			attempts, err = handler.repo.GetQuotaRefreshAttempts(ctx, []string{file.AuthIndex})
			if err != nil {
				t.Fatal(err)
			}
			if attempt = attempts[file.AuthIndex]; attempt.Reason != "" || attempt.ReasonSummary != "" {
				t.Fatal("successful attempt kept failure reason")
			}
		})
	}
}

func TestQuotaOverviewRetainsSuccessfulReadingAfterFailedRefresh(t *testing.T) {
	fixture := &quotaAutoRefreshFixture{reads: map[string]int{}, isSingle: true}
	handler := newQuotaAutoRefreshHandler(t, fixture)
	ctx := context.Background()
	now := time.Now().UTC()
	handler.nowFn = func() time.Time { return now }
	client, err := handler.capabilityClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := client.AuthFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file := files.Files[0]
	refresh := func() {
		t.Helper()
		value, err := handler.readCredentialQuota(ctx, quota.NewService(client), file, handler.loadPriorNormalizedQuota(ctx, file.AuthIndex))
		if err != nil {
			t.Fatal(err)
		}
		if err := handler.persistNormalizedQuotaSnapshot(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	overview := func() QuotaItemDTO {
		t.Helper()
		result, err := handler.buildQuotaOverview(ctx, client)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Quotas) != 1 {
			t.Fatalf("quotas = %+v", result.Quotas)
		}
		return result.Quotas[0]
	}
	refresh()
	first := overview()
	if first.LastRefresh == nil || first.LastRefresh.Outcome != "success" || first.LastSuccessAtMS == 0 {
		t.Fatalf("first = %+v", first)
	}
	fixture.mu.Lock()
	fixture.isUsageFailed = true
	fixture.mu.Unlock()
	now = now.Add(time.Minute)
	refresh()
	failed := overview()
	if failed.LastRefresh == nil || failed.LastRefresh.Outcome != "failed" || failed.LastRefresh.HTTPStatus != 500 || failed.LastRefresh.AttemptedAtMS != now.UnixMilli() {
		t.Fatalf("failure = %+v", failed.LastRefresh)
	}
	if failed.LastSuccessAtMS != first.LastSuccessAtMS || failed.ObservedAtMS != first.ObservedAtMS || len(failed.Windows) != len(first.Windows) {
		t.Fatal("failure replaced the successful reading")
	}
	fixture.mu.Lock()
	fixture.isUsageFailed = false
	fixture.isRateLimited = true
	fixture.mu.Unlock()
	_, err = handler.readCredentialQuota(ctx, quota.NewService(client), file, handler.loadPriorNormalizedQuota(ctx, file.AuthIndex))
	if err == nil {
		t.Fatal("expected rate limit")
	}
	limited := overview()
	if limited.LastRefresh == nil || limited.LastRefresh.Outcome != "rate_limited" || limited.LastRefresh.RetryAtMS <= now.UnixMilli() || limited.LastSuccessAtMS != first.LastSuccessAtMS {
		t.Fatalf("limited = %+v", limited)
	}
	fixture.mu.Lock()
	fixture.isRateLimited = false
	fixture.mu.Unlock()
	now = now.Add(3 * time.Minute)
	refresh()
	recovered := overview()
	if recovered.LastRefresh.Outcome != "success" || recovered.LastSuccessAtMS <= first.LastSuccessAtMS {
		t.Fatalf("recovery = %+v", recovered)
	}
}
