package api

import (
	"context"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

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
