package operations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func quotaObservationFixture(t *testing.T) (*capability.Executor, context.Context) {
	t.Helper()
	ctx := context.Background()
	database, err := repository.Open(ctx, filepath.Join(t.TempDir(), "quota-observations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	now := time.Now().UnixMilli()
	insert := func(eventKey, source string, atMS int64) {
		t.Helper()
		if _, err := repo.SQL().Exec(`INSERT INTO quota_observations (auth_index, provider, window_kind, scope, model, window_minutes, used_percent, reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source)
			VALUES ('c-1', 'codex', 'weekly', 'standard', '', 10080, 10, NULL, '', ?, ?, 0, ?)`, atMS, eventKey, source); err != nil {
			t.Fatal(err)
		}
	}
	insert("b", "passive", now-1000)
	insert("a", "passive", now-2000)
	insert("c", "active", now-500)
	registry := capability.NewRegistry()
	if err := (&Service{Repo: repo}).registerQuotaObservations(registry); err != nil {
		t.Fatal(err)
	}
	executor := &capability.Executor{Registry: registry, Store: repository.AgentStore{Repo: repo}}
	return executor, ctx
}

func invokeQuotaObservations(t *testing.T, executor *capability.Executor, ctx context.Context, arguments string) capability.Result {
	t.Helper()
	result, err := executor.Invoke(ctx, capability.Principal{ID: "administrator", Adapter: "agent", IsAdmin: true}, "quota_observations_list", json.RawMessage(arguments), "")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	return result
}

func TestQuotaObservationsListReadsAscending(t *testing.T) {
	executor, ctx := quotaObservationFixture(t)
	result := invokeQuotaObservations(t, executor, ctx, `{}`)
	if result.Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	var output QuotaObservationList
	if err := json.Unmarshal(result.Data, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Items) != 3 {
		t.Fatalf("items = %+v", output.Items)
	}
	for i := 1; i < len(output.Items); i++ {
		if output.Items[i-1].ObservedAtMS > output.Items[i].ObservedAtMS {
			t.Fatalf("items are not ascending: %+v", output.Items)
		}
	}
	first := output.Items[0]
	if first.Provider != "codex" || first.WindowKind != "weekly" || first.WindowMinutes != 10080 || first.UsedPercent != 10 || first.Scope != "standard" {
		t.Fatalf("row = %+v", first)
	}
	// The oldest row is passive; the newest is the active one, and the source is
	// exposed so an agent can tell a header reading from a refresh.
	if first.Source != "passive" || output.Items[2].Source != "active" {
		t.Fatalf("sources = %+v", output.Items)
	}
}

func TestQuotaObservationsListFiltersBySource(t *testing.T) {
	executor, ctx := quotaObservationFixture(t)
	result := invokeQuotaObservations(t, executor, ctx, `{"source":"active"}`)
	if result.Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	var output QuotaObservationList
	if err := json.Unmarshal(result.Data, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Items) != 1 || output.Items[0].Source != "active" {
		t.Fatalf("active items = %+v", output.Items)
	}
}

func TestQuotaObservationsListRefusesBadInput(t *testing.T) {
	executor, ctx := quotaObservationFixture(t)
	for _, arguments := range []string{
		`{"provider":"OpenAI"}`,
		`{"window_kind":"daily!"}`,
		`{"source":"synthetic"}`,
		`{"limit":1001}`,
		`{"from_ms":5000,"to_ms":1000}`,
	} {
		result := invokeQuotaObservations(t, executor, ctx, arguments)
		if result.Status != "error" || result.Code != "invalid_parameters" && result.Code != "invalid_window" {
			t.Fatalf("accepted %s: %+v", arguments, result)
		}
	}
}

func TestQuotaObservationsListRefusesToTruncateSilently(t *testing.T) {
	executor, ctx := quotaObservationFixture(t)
	result := invokeQuotaObservations(t, executor, ctx, `{"limit":2}`)
	if result.Status != "error" || result.Code != "tool_result_too_large" || result.Detail == "" {
		t.Fatalf("overflow was not reported: %+v", result)
	}
}
