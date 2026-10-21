package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/reauth"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestAuthClassificationAcrossCollectionPaths(t *testing.T) {
	cases := []struct {
		name, provider string
		status         int
		body           string
		isControl      bool
		wantJobs       int
	}{
		{"payment", "antigravity", 403, `{"error":{"reason":"payment_required"}}`, false, 0},
		{"payment expired account", "antigravity", 403, `{"error":{"reason":"payment_required"}}`, false, 1},
		{"claude expired", "claude", 401, `{"error":{"message":"OAuth access token has expired"}}`, false, 1},
		{"codex 401", "codex", 401, `{}`, false, 1},
		{"scope", "claude", 403, `{"error":{"message":"insufficient scope"}}`, false, 0},
		{"forbidden", "codex", 403, `{"error":"Forbidden"}`, false, 0},
		{"policy", "codex", 403, `{"error":{"reason":"policy"}}`, false, 0},
		{"rate limit", "claude", 429, `{"error":{"type":"rate_limit_error"}}`, false, 0},
		{"server", "codex", 503, `{}`, false, 0},
		{"control401", "codex", 401, `{"error":"invalid_token"}`, true, 0},
		{"control403", "codex", 403, `{"error":"invalid_token"}`, true, 0},
		{"envelope expired", "claude", 200, `{"error":{"message":"token has expired"}}`, false, 1},
		{"empty", "codex", 200, `{}`, false, 0},
	}
	for _, tc := range cases {
		for _, path := range []string{"manual", "batch", "scheduler", "verifier"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				ctx := context.Background()
				var handler *Handler
				cards := 0
				client, base, repo := startDashboardTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v8/management/credentials":
						file := map[string]any{"id": "synthetic-file", "name": "synthetic.json", "auth_index": "synthetic", "type": tc.provider, "provider": tc.provider, "account_type": "oauth", "status": "active", "project_id": "synthetic"}
						if strings.HasPrefix(tc.name, "payment") {
							file["cooldowns"] = []map[string]any{{"scope": "model", "model_key": "synthetic-model", "reason": "payment_required", "http_status": 403, "retry_at": "2030-01-01T00:00:00Z"}}
						}
						if tc.name == "payment expired account" {
							file["status"], file["status_message"] = "error", "OAuth access token has expired"
						}
						writeJSON(w, 200, map[string]any{"files": []any{file}})
					case "/v8/management/requests/api-call":
						if tc.isControl {
							writeJSON(w, tc.status, json.RawMessage(tc.body))
							return
						}
						writeJSON(w, 200, map[string]any{"status_code": tc.status, "body": json.RawMessage(tc.body)})
					default:
						w.WriteHeader(404)
					}
				}, func(h *Handler) {
					handler = h
					h.cfg.Quota.SchedulerEnabled = true
					h.reauth = reauth.New(reauth.Options{Repo: h.repo, Enabled: func() bool { return true }, Notify: func(context.Context, repository.ReauthJob, string) error { cards++; return nil }})
				})
				seedAuthFile(t, repo, "synthetic", "synthetic.json", tc.provider, false, false, time.Now())
				switch path {
				case "manual", "batch":
					body := `{"auth_index":"synthetic"}`
					if path == "batch" {
						body = `{"auth_indexes":["synthetic"]}`
					}
					response, err := client.Post(base+"/omc/api/v1/management/quota/refresh", "application/json", strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					response.Body.Close()
					if response.StatusCode != 200 && !(tc.status == 429 && response.StatusCode == 429) {
						t.Fatalf("status=%d", response.StatusCode)
					}
				case "scheduler":
					scheduler := handler.QuotaScheduler(nil)
					files, err := scheduler.ListFiles(ctx)
					if err != nil {
						t.Fatal(err)
					}
					value, refreshErr := scheduler.Refresh(ctx, files[0], nil)
					if err := scheduler.ObserveResult(ctx, files[0], value, refreshErr); err != nil {
						t.Fatal(err)
					}
					if err := scheduler.Persist(ctx, value); err != nil {
						t.Fatal(err)
					}
				case "verifier":
					state, _ := handler.refreshReauthCredential(ctx, "synthetic")
					if state == "healthy" {
						t.Fatal("failure counted as recovery")
					}
				}
				var count int
				if err := repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != tc.wantJobs {
					t.Fatalf("jobs=%d want=%d", count, tc.wantJobs)
				}
				cpa, err := handler.capabilityClient(ctx)
				if err != nil {
					t.Fatal(err)
				}
				overview, err := handler.buildQuotaOverview(ctx, cpa)
				if err != nil || len(overview.Quotas) != 1 {
					t.Fatalf("overview %v", err)
				}
				if (overview.Quotas[0].Recommendation.Status == "needs_reauth") != (tc.wantJobs == 1) {
					t.Fatalf("reload recommendation=%+v", overview.Quotas[0].Recommendation)
				}
				if strings.HasPrefix(tc.name, "payment") && (len(overview.Quotas[0].Cooldowns) != 1 || overview.Quotas[0].Cooldowns[0].Reason != "payment_required") {
					t.Fatal("model payment cooldown lost")
				}
				response, err := client.Get(base + "/omc/api/v1/management/quota/synthetic")
				if err != nil {
					t.Fatal(err)
				}
				var detail map[string]any
				_ = json.NewDecoder(response.Body).Decode(&detail)
				response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("detail status %d", response.StatusCode)
				}
				encoded, _ := json.Marshal(detail)
				if strings.Contains(string(encoded), `"needs_reauth"`) != (tc.wantJobs == 1) {
					t.Fatalf("detail recommendation mismatch: %s", encoded)
				}
				report, _ := handler.BuildFeishuReportSource().ReadReport(ctx)
				provider := reportProvider(t, report, tc.provider)
				if len(provider.Accounts) != 1 || (provider.Accounts[0].State == feishu.StateInvalid) != (tc.wantJobs == 1) {
					t.Fatalf("report state=%s", fmt.Sprint(provider.Accounts))
				}
				if tc.wantJobs == 0 {
					if err := handler.reauth.Tick(ctx); err != nil {
						t.Fatal(err)
					}
					if cards != 0 {
						t.Fatal("false OAuth card")
					}
				}
			})
		}
	}
}

func TestAuthenticatedRecoverySurvivesReload(t *testing.T) {
	for _, hasTimestamp := range []bool{false, true} {
		t.Run(fmt.Sprint("timestamp=", hasTimestamp), func(t *testing.T) {
			ctx := context.Background()
			var handler *Handler
			var updatedAt atomic.Int64
			var isRejected atomic.Bool
			if hasTimestamp {
				updatedAt.Store(time.Now().Add(-time.Hour).UnixMilli())
			}
			client, base, repo := startDashboardTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v8/management/credentials":
					writeJSON(w, 200, map[string]any{"files": []any{map[string]any{"id": "synthetic-file", "name": "synthetic.json", "auth_index": "synthetic", "type": "claude", "provider": "claude", "account_type": "oauth", "status": "error", "status_message": "OAuth access token has expired", "updated_at": updatedAt.Load()}}})
				case "/v8/management/requests/api-call":
					status, body := 200, `{"five_hour":{"utilization":20}}`
					if isRejected.Load() {
						status, body = 403, `{"error":{"code":"payment_required"}}`
					}
					writeJSON(w, 200, map[string]any{"status_code": status, "body": json.RawMessage(body)})
				default:
					w.WriteHeader(404)
				}
			}, func(h *Handler) {
				handler = h
				h.reauth = reauth.New(reauth.Options{Repo: h.repo, Enabled: func() bool { return true }})
			})
			seedAuthFile(t, repo, "synthetic", "synthetic.json", "claude", false, false, time.Now())
			refresh := func() {
				t.Helper()
				response, err := client.Post(base+"/omc/api/v1/management/quota/refresh", "application/json", strings.NewReader(`{"auth_index":"synthetic"}`))
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("refresh status=%d", response.StatusCode)
				}
			}
			assertReload := func(wantAuth bool) {
				t.Helper()
				cpa, err := handler.capabilityClient(ctx)
				if err != nil {
					t.Fatal(err)
				}
				overview, err := handler.buildQuotaOverview(ctx, cpa)
				if err != nil || len(overview.Quotas) != 1 {
					t.Fatalf("overview: %v", err)
				}
				if (overview.Quotas[0].Recommendation.Status == "needs_reauth") != wantAuth {
					t.Fatalf("overview status=%s", overview.Quotas[0].Recommendation.Status)
				}
				if !wantAuth && overview.Quotas[0].Status != "healthy" {
					t.Fatalf("recovery status=%s", overview.Quotas[0].Status)
				}
				response, err := client.Get(base + "/omc/api/v1/management/quota/synthetic")
				if err != nil {
					t.Fatal(err)
				}
				var detail map[string]any
				err = json.NewDecoder(response.Body).Decode(&detail)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 {
					t.Fatalf("detail status=%d error=%v", response.StatusCode, err)
				}
				encoded, _ := json.Marshal(detail)
				if strings.Contains(string(encoded), `"needs_reauth"`) != wantAuth {
					t.Fatalf("detail mismatch: %s", encoded)
				}
			}
			assertReport := func(wantAuth bool) {
				t.Helper()
				report, _ := handler.BuildFeishuReportSource().ReadReport(ctx)
				provider := reportProvider(t, report, "claude")
				wantState := feishu.StateHealthy
				if wantAuth {
					wantState = feishu.StateInvalid
				}
				if len(provider.Accounts) != 1 || provider.Accounts[0].State != wantState {
					t.Fatalf("report=%v", provider.Accounts)
				}
			}
			refresh()
			assertReload(false)
			assertReport(false)
			var initialJobs int
			if err := repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&initialJobs); err != nil || initialJobs != 0 {
				t.Fatalf("recovered jobs=%d error=%v", initialJobs, err)
			}
			snapshots, err := repo.GetLatestQuotaSnapshots(ctx, []string{"synthetic"})
			if err != nil || snapshots["synthetic"].ObservedAtMS <= 0 {
				t.Fatalf("snapshot: %v", err)
			}
			updatedAt.Store(snapshots["synthetic"].ObservedAtMS + 1)
			assertReload(true)
			isRejected.Store(true)
			refresh()
			assertReload(true)
			assertReport(true)
			var jobs int
			if err := repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&jobs); err != nil || jobs != 1 {
				t.Fatalf("jobs=%d error=%v", jobs, err)
			}
		})
	}
}
