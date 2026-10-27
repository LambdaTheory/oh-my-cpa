package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/automation"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// TestAccountAutomationRoutesResolveTheMasterSwitch proves the tri-state master
// switch reaches the wire: unset with the web switch off reports default_off, and a
// web enable reports web.
func TestAccountAutomationRoutesResolveTheMasterSwitch(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		handler.SetAutomation(handler.BuildAutomationService())
	})

	response, payload := getJSON(t, client, base+"/omc/api/v1/account-automation")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.StatusCode, payload)
	}
	var list accountAutomationListResponse
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	if list.Settings.Enabled || list.Settings.Source != "default_off" || list.Settings.MasterFromEnv {
		t.Fatalf("default settings = %+v", list.Settings)
	}
	if len(list.Accounts) != 0 {
		t.Fatalf("an empty roster expected, got %+v", list.Accounts)
	}

	body, _ := json.Marshal(map[string]any{"enabled": true})
	request, err := http.NewRequest(http.MethodPut, base+"/omc/api/v1/account-automation/settings", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updatedResponse, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer updatedResponse.Body.Close()
	if updatedResponse.StatusCode != http.StatusOK {
		t.Fatalf("put settings status = %d", updatedResponse.StatusCode)
	}
	var updated accountAutomationSettingsDTO
	if err := json.NewDecoder(updatedResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if !updated.Enabled || updated.Source != "web" {
		t.Fatalf("web enable = %+v", updated)
	}

	// A cross-origin write is refused by the same-origin guard before it reaches
	// the handler.
	crossOrigin, err := http.NewRequest(http.MethodPut, base+"/omc/api/v1/account-automation/settings", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	crossOrigin.Header.Set("Content-Type", "application/json")
	crossOrigin.Header.Set("Origin", "http://attacker.test")
	crossResponse, err := client.Do(crossOrigin)
	if err != nil {
		t.Fatal(err)
	}
	crossResponse.Body.Close()
	if crossResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", crossResponse.StatusCode)
	}

	// A credential not on the roster cannot be switched.
	accountBody, _ := json.Marshal(map[string]any{"enabled": true})
	accountRequest, err := http.NewRequest(http.MethodPut, base+"/omc/api/v1/account-automation/accounts/not-adopted", bytes.NewReader(accountBody))
	if err != nil {
		t.Fatal(err)
	}
	accountRequest.Header.Set("Content-Type", "application/json")
	accountResponse, err := client.Do(accountRequest)
	if err != nil {
		t.Fatal(err)
	}
	accountResponse.Body.Close()
	if accountResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown account status = %d, want 404", accountResponse.StatusCode)
	}
}

// TestAccountAutomationCandidatesAreReadOnlyAndAdoptable proves the first-load
// contract: GET returns the live gateway credentials as candidates without adopting
// them, the persisted roster stays authoritative, and an adoption selects exactly the
// chosen credentials.
func TestAccountAutomationCandidatesAreReadOnlyAndAdoptable(t *testing.T) {
	const candidateSecret = "candidate-secret-token-value"
	client, base, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[
				{"auth_index":"idx-1","name":"claude-1.json","provider":"claude","email":"one@example.test","disabled":true,"token":"` + candidateSecret + `","raw_content":"` + candidateSecret + `"},
				{"auth_index":"idx-2","name":"codex-2.json","provider":"codex","disabled":false}
			]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		handler.SetAutomation(handler.BuildAutomationService())
	})

	response, payload := getJSON(t, client, base+"/omc/api/v1/account-automation")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.StatusCode, payload)
	}
	if strings.Contains(string(payload), candidateSecret) {
		t.Fatalf("candidates must not carry credential material: %s", payload)
	}
	var list accountAutomationListResponse
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Accounts) != 0 {
		t.Fatalf("an empty roster expected, got %+v", list.Accounts)
	}
	if !list.Inventory.Available {
		t.Fatalf("a complete inventory must read available: %+v", list.Inventory)
	}
	if len(list.Candidates) != 2 {
		t.Fatalf("candidates = %+v", list.Candidates)
	}
	for _, candidate := range list.Candidates {
		if candidate.Adopted || !candidate.Adoptable || !candidate.AutoDisableSupported || !candidate.Controllable {
			t.Fatalf("a verified live credential must be adoptable and auto-disable capable: %+v", candidate)
		}
	}

	// A GET must never write the roster.
	if count, err := repo.CountAutomationAccounts(context.Background()); err != nil || count != 0 {
		t.Fatalf("GET must not adopt: count = %d, err = %v", count, err)
	}

	// Adopt exactly one candidate; only it joins the persisted roster.
	body, _ := json.Marshal(map[string]any{"auth_indexes": []string{"idx-2"}, "enable": true})
	request, err := http.NewRequest(http.MethodPost, base+"/omc/api/v1/account-automation/adopt", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	adoptResponse, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	adoptPayload, _ := io.ReadAll(adoptResponse.Body)
	adoptResponse.Body.Close()
	if adoptResponse.StatusCode != http.StatusOK {
		t.Fatalf("adopt status = %d, body = %s", adoptResponse.StatusCode, adoptPayload)
	}

	_, payload = getJSON(t, client, base+"/omc/api/v1/account-automation")
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Accounts) != 1 || list.Accounts[0].AuthIndex != "idx-2" {
		t.Fatalf("roster after adopt = %+v", list.Accounts)
	}
	var adoptedSeen bool
	for _, candidate := range list.Candidates {
		if candidate.AuthIndex == "idx-2" && candidate.Adopted {
			adoptedSeen = true
		}
	}
	if !adoptedSeen {
		t.Fatalf("the adopted candidate must be marked adopted: %+v", list.Candidates)
	}
	if count, err := repo.CountAutomationAccounts(context.Background()); err != nil || count != 1 {
		t.Fatalf("exactly one account expected: count = %d, err = %v", count, err)
	}
}

// TestAccountAutomationWritesStayBehindTheConsoleSession keeps the lane-A write routes
// console-only: an anonymous caller and a caller presenting the read-only MCP key are
// both refused before any handler runs.
func TestAccountAutomationWritesStayBehindTheConsoleSession(t *testing.T) {
	_, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"files":[]}`))
	}, func(handler *Handler) {
		handler.SetAutomation(handler.BuildAutomationService())
	})

	anonymous := &http.Client{}
	getResponse, err := anonymous.Get(base + "/omc/api/v1/account-automation")
	if err != nil {
		t.Fatal(err)
	}
	getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401", getResponse.StatusCode)
	}

	body, _ := json.Marshal(map[string]any{"auth_indexes": []string{"idx-1"}, "enable": true})
	request, err := http.NewRequest(http.MethodPost, base+"/omc/api/v1/account-automation/adopt", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer read-only-key-that-is-long-enough")
	response, err := anonymous.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("read-only bearer adopt = %d, want 401", response.StatusCode)
	}
}

// TestAccountAutomationCandidateSeparatesAdoptionFromAutoDisable proves the candidate
// contract keeps CPA controllability separate from verified provider capability. A
// controllable unverified credential remains adoptable for monitoring, while a
// credential that cannot be addressed by CPA is blocked from adoption.
func TestAccountAutomationCandidateSeparatesAdoptionFromAutoDisable(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[
				{"auth_index":"idx-unverified","name":"openrouter-1.json","provider":"openrouter","disabled":false},
				{"auth_index":"idx-verified","name":"claude-1.json","provider":"claude","disabled":false},
				{"auth_index":"idx-uncontrollable","provider":"openrouter","disabled":false}
			]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		handler.SetAutomation(handler.BuildAutomationService())
	})

	response, payload := getJSON(t, client, base+"/omc/api/v1/account-automation")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.StatusCode, payload)
	}
	var list accountAutomationListResponse
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	byIndex := make(map[string]accountAutomationCandidateDTO, len(list.Candidates))
	for _, candidate := range list.Candidates {
		byIndex[candidate.AuthIndex] = candidate
	}
	if candidate := byIndex["idx-verified"]; !candidate.Adoptable || !candidate.AutoDisableSupported || candidate.AdoptionBlockedReason != "" {
		t.Fatalf("a verified controllable provider must support adoption and auto-disable: %+v", candidate)
	}
	if candidate := byIndex["idx-unverified"]; !candidate.Adoptable || candidate.AutoDisableSupported || candidate.AdoptionBlockedReason != "" {
		t.Fatalf("an unverified controllable provider must be adoptable for monitoring only: %+v", candidate)
	}
	if candidate := byIndex["idx-uncontrollable"]; candidate.Adoptable || candidate.AutoDisableSupported || candidate.AdoptionBlockedReason != automation.ReasonCredentialMissingName {
		t.Fatalf("an uncontrollable provider must stay blocked with its reason: %+v", candidate)
	}

	body, _ := json.Marshal(map[string]any{"auth_indexes": []string{"idx-unverified", "idx-verified"}, "enable": true})
	request, err := http.NewRequest(http.MethodPost, base+"/omc/api/v1/account-automation/adopt", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	adoptResponse, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	adoptResponse.Body.Close()
	if adoptResponse.StatusCode != http.StatusOK {
		t.Fatalf("adopt status = %d", adoptResponse.StatusCode)
	}
	_, payload = getJSON(t, client, base+"/omc/api/v1/account-automation")
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	roster := make(map[string]accountAutomationAccountDTO, len(list.Accounts))
	for _, account := range list.Accounts {
		roster[account.AuthIndex] = account
	}
	if !roster["idx-verified"].AutoDisableSupported {
		t.Fatalf("verified roster capability = %+v", roster["idx-verified"])
	}
	if roster["idx-unverified"].AutoDisableSupported {
		t.Fatalf("unverified roster capability = %+v", roster["idx-unverified"])
	}
}

// TestAccountAutomationRecoveryProjection keeps the console and read-only Agent/MCP
// summary on one clock-aware contract: future active plans are scheduled, overdue
// disabled plans are explicit retry work, and a recovered row never exposes history.
func TestAccountAutomationRecoveryProjection(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	var handlerRef *Handler
	client, base, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		handler.nowFn = func() time.Time { return now }
		handler.SetAutomation(handler.BuildAutomationService())
		handlerRef = handler
	})

	ctx := context.Background()
	if _, _, err := repo.AdoptAutomationAccounts(ctx, repository.AutomationBatch{ID: "recovery", CreatedAtMS: now.UnixMilli()}, []repository.AutomationAccount{
		{AuthIndex: "scheduled", Provider: "codex", Name: "scheduled.json"},
		{AuthIndex: "overdue", Provider: "codex", Name: "overdue.json"},
		{AuthIndex: "recovered", Provider: "codex", Name: "recovered.json"},
	}, true, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	disable := func(authIndex string, resetAtMS int64) repository.AutomationAccount {
		t.Helper()
		window := repository.AutomationWeeklyWindow{AuthIndex: authIndex, Provider: "codex", ObservedAtMS: now.Add(-time.Hour).UnixMilli(), ResetAtMS: &resetAtMS}
		account, err := repo.RecordAutomationDisable(ctx, authIndex, 1, window, authIndex+":codex:cycle", now.Add(-time.Hour).UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		return account
	}
	future := now.Add(time.Hour).UnixMilli()
	past := now.Add(-time.Minute).UnixMilli()
	disable("scheduled", future)
	disable("overdue", past)
	recovered := disable("recovered", past)
	if _, err := repo.RecordAutomationEnable(ctx, "recovered", recovered.IntentVersion, now.UnixMilli(), false); err != nil {
		t.Fatal(err)
	}
	// Simulate a row written by the old implementation: projection and decisions
	// must still treat this as history after the successful enable.
	if _, err := repo.SQL().ExecContext(ctx, `UPDATE account_automation_accounts SET cycle_reset_at_ms = ? WHERE auth_index = 'recovered'`, past); err != nil {
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
	assertRecoveryProjection(t, list.Accounts)

	status, err := handlerRef.operationsService().AutomationSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	agentAccounts := make([]accountAutomationAccountDTO, 0, len(status.Accounts))
	for _, account := range status.Accounts {
		agentAccounts = append(agentAccounts, accountAutomationAccountDTO{
			AuthIndex: account.AuthIndex, RecoveryAtMS: account.RecoveryAtMS, RecoveryStatus: account.RecoveryStatus,
		})
	}
	assertRecoveryProjection(t, agentAccounts)
}

func assertRecoveryProjection(t *testing.T, accounts []accountAutomationAccountDTO) {
	t.Helper()
	byIndex := make(map[string]accountAutomationAccountDTO, len(accounts))
	for _, account := range accounts {
		byIndex[account.AuthIndex] = account
	}
	if account := byIndex["scheduled"]; account.RecoveryAtMS == nil || account.RecoveryStatus != string(automation.RecoveryScheduled) {
		t.Fatalf("scheduled projection = %+v", account)
	}
	if account := byIndex["overdue"]; account.RecoveryAtMS == nil || account.RecoveryStatus != string(automation.RecoveryOverdue) {
		t.Fatalf("overdue projection = %+v", account)
	}
	if account := byIndex["recovered"]; account.RecoveryAtMS != nil || account.RecoveryStatus != "" {
		t.Fatalf("recovered projection must omit history: %+v", account)
	}
}

func TestAccountAutomationOptionsAndRemovalContract(t *testing.T) {
	ctx := context.Background()
	var handlerRef *Handler
	gatewayWrites := 0
	client, base, repo := startDashboardTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			gatewayWrites++
		}
		if strings.HasPrefix(r.URL.Path, "/v8/management/credentials") {
			_, _ = w.Write([]byte(`{"files":[{"auth_index":"synthetic-a","name":"synthetic-a.json","provider":"codex","disabled":true},{"auth_index":"synthetic-b","name":"synthetic-b.json","provider":"claude"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}, func(h *Handler) { h.SetAutomation(h.BuildAutomationService()); handlerRef = h })
	send := func(method, path, body string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, base+"/omc/api/v1/account-automation"+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		payload, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: status=%d body=%s", method, path, response.StatusCode, payload)
		}
		return payload
	}
	send(http.MethodPost, "/adopt", `{"auth_indexes":["synthetic-a","synthetic-b"]}`, http.StatusOK)
	account, _, err := repo.GetAutomationAccount(ctx, "synthetic-a")
	if err != nil || !account.IsEnabled {
		t.Fatalf("adoption must default on: %+v %v", account, err)
	}
	payload := send(http.MethodPut, "/accounts/synthetic-a", `{"burn_credit":true}`, http.StatusOK)
	var dto accountAutomationAccountDTO
	if err := json.Unmarshal(payload, &dto); err != nil {
		t.Fatal(err)
	}
	if !dto.BurnCredit || !dto.IsEnabled || dto.Removed {
		t.Fatalf("Credit-only patch: %+v", dto)
	}
	payload = send(http.MethodPut, "/accounts/synthetic-a", `{"enabled":false,"burn_credit":false}`, http.StatusOK)
	if err := json.Unmarshal(payload, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.BurnCredit || dto.IsEnabled {
		t.Fatalf("combined patch: %+v", dto)
	}
	send(http.MethodPut, "/accounts/synthetic-a", `{}`, http.StatusBadRequest)
	send(http.MethodPut, "/accounts/synthetic-a", `{"burn_credit":"yes"}`, http.StatusBadRequest)
	send(http.MethodPut, "/accounts/absent", `{"burn_credit":true}`, http.StatusNotFound)
	// Legacy persisted reasons remain readable without manufacturing new legacy values.
	if _, err := repo.SQL().ExecContext(ctx, `UPDATE account_automation_accounts SET monitor_reason = 'no_controllable_credential' WHERE auth_index = 'synthetic-a'`); err != nil {
		t.Fatal(err)
	}
	_, payload = getJSON(t, client, base+"/omc/api/v1/account-automation")
	if !strings.Contains(string(payload), "no_controllable_credential") {
		t.Fatal("legacy reason must remain readable")
	}
	status, err := handlerRef.operationsService().AutomationSummary(ctx)
	if err != nil || len(status.Accounts) != 2 {
		t.Fatalf("read-only summary: %+v %v", status, err)
	}
	payload = send(http.MethodPost, "/accounts/synthetic-a/unadopt", "", http.StatusOK)
	if err := json.Unmarshal(payload, &dto); err != nil {
		t.Fatal(err)
	}
	if !dto.Removed || dto.IsAdopted {
		t.Fatalf("single removal DTO: %+v", dto)
	}
	send(http.MethodPost, "/accounts/synthetic-a/unadopt", "", http.StatusNotFound)
	send(http.MethodPost, "/unadopt", `{"auth_indexes":[]}`, http.StatusBadRequest)
	payload = send(http.MethodPost, "/unadopt", `{"auth_indexes":["synthetic-b","synthetic-b","absent"]}`, http.StatusOK)
	if string(bytes.TrimSpace(payload)) != `{"removed":1}` {
		t.Fatalf("batch removal: %s", payload)
	}
	_, payload = getJSON(t, client, base+"/omc/api/v1/account-automation")
	var list accountAutomationListResponse
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Accounts) != 0 || list.Settings.Accounts != 0 || len(list.Candidates) != 2 || list.Candidates[0].Adopted || list.Candidates[1].Adopted {
		t.Fatalf("removed roster: %+v", list)
	}
	if gatewayWrites != 0 {
		t.Fatalf("roster writes must never change credential state: %d", gatewayWrites)
	}
}

func TestAutomationIncompleteInventoryIsDegraded(t *testing.T) {
	for _, body := range []string{`{}`, `{"files":null}`, `{"files":[{"name":"synthetic.json"}]}`} {
		t.Run(body, func(t *testing.T) {
			client, base, _ := startDashboardTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}, func(h *Handler) { h.SetAutomation(h.BuildAutomationService()) })
			response, payload := getJSON(t, client, base+"/omc/api/v1/account-automation")
			var list accountAutomationListResponse
			if err := json.Unmarshal(payload, &list); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || list.Inventory.Available {
				t.Fatalf("a partial inventory must read unavailable: %s", payload)
			}
		})
	}
}
