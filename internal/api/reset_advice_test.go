package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestQuotaConsoleResetAdviceBeforeAndAfterAnnouncement(t *testing.T) {
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	client, base, repo := startDashboardTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v8/management/credentials" {
			t.Errorf("unexpected CPA call: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]any{"files": []map[string]any{{"name": "synthetic.json", "auth_index": "synthetic-auth", "type": "codex", "provider": "codex", "disabled": false}}})
	}, func(h *Handler) { h.nowFn = func() time.Time { return now } })
	ctx := context.Background()
	if _, err := repo.UpsertDiscoveredResources(ctx, "default", []domain.DiscoveredResource{{ResourceKey: "synthetic", CPAResourceType: "auth-file", CPAAuthIndex: "synthetic-auth", CPAResourceName: "Synthetic", CPADriver: "codex", Status: domain.ResourceStatusUnclaimed}}, now, false); err != nil {
		t.Fatal(err)
	}
	snapshot := repository.QuotaSnapshotRecord{AuthIndex: "synthetic-auth", Provider: "codex", Status: "healthy", ObservedAtMS: now.UnixMilli(), WindowsJSON: fmt.Sprintf(`[{"id":"five_hour","kind":"five_hour","scope":"standard","used_percent":95,"reset_at_ms":%d}]`, now.Add(time.Hour).UnixMilli()), ResetCreditsJSON: fmt.Sprintf(`{"available_count":1,"credits":[{"status":"available","expires_at_ms":%d}]}`, now.Add(120*time.Hour).UnixMilli())}
	if err := repo.SaveQuotaSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	read := func(path string, isDetail bool) *resetAdviceDTO {
		t.Helper()
		response, body := getJSON(t, client, base+"/omc/api/v1/management/quota"+path)
		if response.StatusCode != 200 {
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
		if isDetail {
			var result struct {
				Quota QuotaItemDTO `json:"quota"`
			}
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			return result.Quota.ResetAdvice
		}
		var result quotaOverviewData
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Quotas) != 1 {
			t.Fatalf("quotas=%+v", result)
		}
		return result.Quotas[0].ResetAdvice
	}
	before := read("", false)
	if before == nil || before.Verdict != "keep_for_emergency" {
		t.Fatalf("before=%+v", before)
	}
	reset := now.Add(2 * time.Hour).UnixMilli()
	if _, err := repo.CreateResetAnnouncement(ctx, repository.ResetAnnouncementInput{Provider: "codex", Source: "manual", Scope: "all", Confidence: "confirmed", ExpectedResetAtMS: &reset, OriginalText: "Synthetic public announcement"}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	after := read("", false)
	detail := read("/synthetic-auth", true)
	if after == nil || after.Verdict != "use_before_expiry" || after.LocalEvidence.Verdict != "keep_for_emergency" || len(after.Announcements) != 1 || !after.Announcements[0].HasConflict {
		t.Fatalf("after=%+v", after)
	}
	if !reflect.DeepEqual(after, detail) {
		t.Fatalf("overview/detail mismatch: %+v vs %+v", after, detail)
	}
}
