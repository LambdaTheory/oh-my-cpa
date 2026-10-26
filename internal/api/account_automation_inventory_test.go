package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/automation"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// TestAccountAutomationProjectsCurrentCredentials is the API half of the approved rule: the
// list response describes the gateway's current credentials, each independently of whether a
// plan matched it. A managed plan without a current credential is a pending-absence count, not
// a row, and the GET itself changes nothing.
func TestAccountAutomationProjectsCurrentCredentials(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[
				{"auth_index":"idx-managed","name":"managed.json","provider":"codex","email":"ops@example.test","disabled":false},
				{"auth_index":"idx-paused","name":"paused.json","provider":"claude","email":"ops@example.test","disabled":true},
				{"auth_index":"idx-unmanaged","name":"unmanaged.json","provider":"gemini","email":"ops@example.test","disabled":true},
				{"auth_index":"idx-noname","provider":"xai","disabled":false}
			]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) { handler.SetAutomation(handler.BuildAutomationService()) })

	ctx := context.Background()
	if _, _, err := repo.AdoptAutomationAccounts(ctx, repository.AutomationBatch{ID: "synthetic", CreatedAtMS: 1}, []repository.AutomationAccount{
		{AuthIndex: "idx-managed", Provider: "codex", Name: "managed.json"},
		{AuthIndex: "idx-paused", Provider: "claude", Name: "paused.json"},
	}, true, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetAutomationSwitch(ctx, "idx-paused", false, 1, 2); err != nil {
		t.Fatal(err)
	}
	// A plan whose credential is gone stays an active row until two inventories confirm it.
	if _, _, err := repo.AdoptAutomationAccounts(ctx, repository.AutomationBatch{ID: "synthetic-2", CreatedAtMS: 1}, []repository.AutomationAccount{
		{AuthIndex: "idx-absent", Provider: "codex", Name: "absent.json"},
	}, true, 1); err != nil {
		t.Fatal(err)
	}

	response, payload := getJSON(t, client, base+"/omc/api/v1/account-automation")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.StatusCode, payload)
	}
	var list accountAutomationListResponse
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	if !list.Inventory.Available || list.Inventory.Credentials == nil || *list.Inventory.Credentials != 4 {
		t.Fatalf("a complete inventory must report its current total: %+v", list.Inventory)
	}
	if list.Inventory.Managed == nil || *list.Inventory.Managed != 2 || list.Inventory.Unmanaged == nil || *list.Inventory.Unmanaged != 2 {
		t.Fatalf("managed and unmanaged refer to current rows: %+v", list.Inventory)
	}
	if list.Inventory.Disabled == nil || *list.Inventory.Disabled != 2 || list.Inventory.PendingAbsence == nil || *list.Inventory.PendingAbsence != 1 {
		t.Fatalf("disabled is the gateway flag and absence is a count: %+v", list.Inventory)
	}
	rows := make(map[string]accountAutomationCredentialDTO, len(list.Credentials))
	for _, row := range list.Credentials {
		rows[row.AuthIndex] = row
	}
	if len(list.Credentials) != 4 {
		t.Fatalf("every current credential is a row: %+v", list.Credentials)
	}
	if row := rows["idx-managed"]; !row.Managed || row.Plan == nil || !row.Plan.IsEnabled || row.Disabled {
		t.Fatalf("managed row: %+v", row)
	}
	if row := rows["idx-paused"]; !row.Managed || row.Plan == nil || row.Plan.IsEnabled || !row.Disabled {
		t.Fatalf("paused row keeps its current disabled flag: %+v", row)
	}
	if row := rows["idx-unmanaged"]; row.Managed || row.Plan != nil || !row.Adoptable {
		t.Fatalf("unmanaged row: %+v", row)
	}
	if row := rows["idx-noname"]; row.Managed || row.Adoptable || row.AdoptionBlockedReason != automation.ReasonCredentialMissingName {
		t.Fatalf("a present credential without a name is a row but not adoptable: %+v", row)
	}
	for _, row := range list.Credentials {
		if row.AuthIndex == "idx-absent" {
			t.Fatalf("a plan without a current credential is not a row: %+v", list.Credentials)
		}
	}

	// A GET may not adopt, clean up, or move an absence counter.
	if count, err := repo.CountAutomationAccounts(ctx); err != nil || count != 3 {
		t.Fatalf("GET must not write the roster: count=%d err=%v", count, err)
	}
}

// TestAccountAutomationUnavailableInventoryIsNotAnEmptyGateway keeps the outage contract: a
// failed or incomplete read reports the inventory unavailable, keeps the counts unknown and
// never presents the gateway as empty. The last-known plans are still readable.
func TestAccountAutomationUnavailableInventoryIsNotAnEmptyGateway(t *testing.T) {
	for _, body := range []string{`{}`, `{"files":null}`, `{"files":[{"name":"synthetic.json"}]}`, `{"files":[{"auth_index":"a"},{"auth_index":"a"}]}`} {
		t.Run(body, func(t *testing.T) {
			client, base, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(body))
			}, func(handler *Handler) { handler.SetAutomation(handler.BuildAutomationService()) })
			if _, _, err := repo.AdoptAutomationAccounts(context.Background(), repository.AutomationBatch{ID: "b", CreatedAtMS: 1}, []repository.AutomationAccount{{AuthIndex: "last-known", Provider: "codex", Name: "known.json"}}, true, 1); err != nil {
				t.Fatal(err)
			}
			response, payload := getJSON(t, client, base+"/omc/api/v1/account-automation")
			var list accountAutomationListResponse
			if err := json.Unmarshal(payload, &list); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || list.Inventory.Available {
				t.Fatalf("an incomplete inventory must read unavailable: %s", payload)
			}
			if list.Inventory.Credentials != nil || list.Inventory.Managed != nil || list.Inventory.Unmanaged != nil ||
				list.Inventory.Disabled != nil || list.Inventory.PendingAbsence != nil {
				t.Fatalf("unavailable counts must be unknown, not zero: %+v", list.Inventory)
			}
			if len(list.Credentials) != 0 {
				t.Fatalf("no credential may be invented from a failed read: %+v", list.Credentials)
			}
			if len(list.Accounts) != 1 || !list.AccountsLastKnown {
				t.Fatalf("last-known plans stay readable and are marked last-known: %+v", list)
			}
		})
	}
}

// TestAccountAutomationRejectsAStaleIncarnation proves a delayed or duplicated mutation that
// names an older intent version conflicts instead of acting on a newly adopted lifecycle.
func TestAccountAutomationRejectsAStaleIncarnation(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[{"auth_index":"idx-1","name":"synthetic.json","provider":"codex","disabled":true}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) { handler.SetAutomation(handler.BuildAutomationService()) })

	ctx := context.Background()
	post := func(path, body string, want int) []byte {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, base+"/omc/api/v1/account-automation"+path, bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		payload, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s: status=%d body=%s", path, response.StatusCode, payload)
		}
		return payload
	}
	post("/adopt", `{"auth_indexes":["idx-1"],"enable":true}`, http.StatusOK)
	stale, _, err := repo.GetAutomationAccount(ctx, "idx-1")
	if err != nil {
		t.Fatal(err)
	}
	// The credential disappears, is removed, and is adopted again: a new incarnation.
	post("/accounts/idx-1/unadopt", ``, http.StatusOK)
	post("/adopt", `{"auth_indexes":["idx-1"],"enable":true}`, http.StatusOK)
	current, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if current.IntentVersion <= stale.IntentVersion {
		t.Fatalf("fresh adoption must advance the incarnation: %d -> %d", stale.IntentVersion, current.IntentVersion)
	}

	request, err := http.NewRequest(http.MethodPut, base+"/omc/api/v1/account-automation/accounts/idx-1",
		bytes.NewReader([]byte(`{"enabled":false,"expected_intent_version":`+jsonInt(stale.IntentVersion)+`}`)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale incarnation: status=%d body=%s", response.StatusCode, payload)
	}
	unchanged, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if !unchanged.IsEnabled {
		t.Fatalf("a refused stale mutation must not change the switch: %+v", unchanged)
	}
}

func jsonInt(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
