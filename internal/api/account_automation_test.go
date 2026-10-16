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
	if list.CandidatesDegraded {
		t.Fatalf("candidates unexpectedly degraded: %s", list.CandidatesError)
	}
	if len(list.Candidates) != 2 {
		t.Fatalf("candidates = %+v", list.Candidates)
	}
	for _, candidate := range list.Candidates {
		if candidate.Adopted || !candidate.Eligible || !candidate.Controllable {
			t.Fatalf("a live credential must be an eligible, unadopted candidate: %+v", candidate)
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

// TestAccountAutomationCandidateMarksUnverifiedProviderIneligible proves the candidate
// contract reflects capability: a provider whose account-level weekly window OMC has not
// verified is offered ineligible with the unverified-capability reason, while a verified
// one stays eligible.
func TestAccountAutomationCandidateMarksUnverifiedProviderIneligible(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[
				{"auth_index":"idx-unverified","name":"openrouter-1.json","provider":"openrouter","disabled":false},
				{"auth_index":"idx-verified","name":"claude-1.json","provider":"claude","disabled":false}
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
	if candidate := byIndex["idx-verified"]; !candidate.Eligible || candidate.IneligibleReason != "" {
		t.Fatalf("a verified provider must stay eligible: %+v", candidate)
	}
	if candidate := byIndex["idx-unverified"]; candidate.Eligible || candidate.IneligibleReason != automation.ReasonUnverifiedCapability {
		t.Fatalf("an unverified provider must be ineligible with the capability reason: %+v", candidate)
	}
}
