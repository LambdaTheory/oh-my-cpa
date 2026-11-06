package api

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func repoKeyPublicKey(t *testing.T) string {
	t.Helper()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return "x25519:" + base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
}

func countRepoKeyAudit(t *testing.T, fixture providerTestFixture, action string) int {
	t.Helper()
	var count int
	if err := fixture.handler.repo.SQL().QueryRowContext(context.Background(),
		`SELECT COUNT(1) FROM audit_events WHERE target_type = 'repo_api_key' AND action = ?`, action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// The console can put a waiting application's link back in front of a person: it issues a
// fresh one-time link, the earlier one stops working, and a decided application is refused
// (AC-8).
func TestManagementRepoKeyApprovalLinkEndpoint(t *testing.T) {
	fixture := newProviderTestFixture(t)
	client, baseURL := fixture.client, fixture.baseURL

	publicKey := repoKeyPublicKey(t)
	applyBody := fmt.Sprintf(`{"repo":"LambdaTheory/oh-my-cpa","purpose":"write release notes","agent":"claude","public_key":%q}`, publicKey)
	resp, payload := doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/repo-keys/applications", applyBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status = %d body %s", resp.StatusCode, payload)
	}
	var applied struct {
		ApplicationID string `json:"application_id"`
		ApprovalToken string `json:"approval_token"`
	}
	if err := json.Unmarshal(payload, &applied); err != nil || applied.ApplicationID == "" {
		t.Fatalf("apply response = %s", payload)
	}

	resp, payload = doJSON(t, client, http.MethodPost,
		baseURL+"/omc/api/v1/management/repo-keys/applications/"+applied.ApplicationID+"/approval-link", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approval link status = %d body %s", resp.StatusCode, payload)
	}
	var issued struct {
		Application       RepoKeyApplicationDTO `json:"application"`
		ApprovalToken     string                `json:"approval_token"`
		ApprovalURL       string                `json:"approval_url"`
		ApprovalExpiresAt int64                 `json:"approval_expires_at_ms"`
	}
	if err := json.Unmarshal(payload, &issued); err != nil {
		t.Fatalf("approval link response is not JSON: %s", payload)
	}
	if issued.ApprovalToken == "" || issued.ApprovalExpiresAt == 0 ||
		!strings.HasSuffix(issued.ApprovalURL, "/repo-key-apply/"+issued.ApprovalToken) ||
		issued.Application.Status != "pending" {
		t.Fatalf("issued link = %+v", issued)
	}

	// The issued link opens the same application; the one the applicant received no longer does.
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/apply-link/"+issued.ApprovalToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the issued link did not open: %d %s", resp.StatusCode, payload)
	}
	resp, _ = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/apply-link/"+applied.ApprovalToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the superseded link answered %d, want 404", resp.StatusCode)
	}

	// Once decided, the application gets no new link.
	resp, payload = doJSON(t, client, http.MethodPost,
		baseURL+"/omc/api/v1/management/repo-keys/applications/"+applied.ApplicationID+"/approve", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d body %s", resp.StatusCode, payload)
	}
	resp, payload = doJSON(t, client, http.MethodPost,
		baseURL+"/omc/api/v1/management/repo-keys/applications/"+applied.ApplicationID+"/approval-link", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a decided application answered %d, want 409: %s", resp.StatusCode, payload)
	}
}

// The console's half of the loop: an operator stands in for a caller, approves the
// application, sees the key appear masked, and the plaintext reaches no console response
// except the deliberate reveal (AC-24, AC-25, AC-35).
func TestManagementRepoKeyEndpoints(t *testing.T) {
	fixture := newProviderTestFixture(t)
	client, baseURL, state := fixture.client, fixture.baseURL, fixture.state

	resp, payload := getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list repo keys status = %d body %s", resp.StatusCode, payload)
	}
	var listed struct {
		Keys                []RepoKeyBindingDTO `json:"keys"`
		PendingApplications int                 `json:"pending_applications"`
		AwaitingDelivery    int                 `json:"awaiting_delivery"`
	}
	if err := json.Unmarshal(payload, &listed); err != nil {
		t.Fatalf("list response is not JSON: %s", payload)
	}
	if len(listed.Keys) != 0 || listed.PendingApplications != 0 {
		t.Fatalf("a fresh deployment lists %+v", listed)
	}

	// 1. The operator files an application on an agent's behalf. It is queued, not issued.
	publicKey := repoKeyPublicKey(t)
	applyBody := fmt.Sprintf(`{"repo":"LambdaTheory/oh-my-cpa","purpose":"write release notes","agent":"claude","public_key":%q}`, publicKey)
	resp, payload = doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/repo-keys/applications", applyBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status = %d body %s", resp.StatusCode, payload)
	}
	var applied struct {
		ApplicationID string `json:"application_id"`
		Status        string `json:"status"`
		Kind          string `json:"kind"`
		ApprovalToken string `json:"approval_token"`
	}
	if err := json.Unmarshal(payload, &applied); err != nil {
		t.Fatalf("apply response is not JSON: %s", payload)
	}
	if applied.Status != "pending" || applied.Kind != "issue" || applied.ApplicationID == "" {
		t.Fatalf("an application from the console was not queued: %+v", applied)
	}
	if len(state.clientKeys) != 2 {
		t.Fatalf("an application alone must not create a key: %v", state.clientKeys)
	}

	// 2. The queue carries it, and the answer names the applicant's identity without any
	// key material.
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/applications?status=pending")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("queue status = %d body %s", resp.StatusCode, payload)
	}
	if body := string(payload); strings.Contains(body, publicKey) || strings.Contains(body, `"ciphertext"`) {
		t.Fatalf("the queue leaks key material: %s", body)
	}
	var queue struct {
		Applications []RepoKeyApplicationDTO `json:"applications"`
	}
	if err := json.Unmarshal(payload, &queue); err != nil || len(queue.Applications) != 1 {
		t.Fatalf("queue = %s", payload)
	}
	if queue.Applications[0].Source != "console" || queue.Applications[0].Device != "console" ||
		queue.Applications[0].PublicKeyFingerprint == "" {
		t.Fatalf("queued application = %+v", queue.Applications[0])
	}

	// 3. Approving issues the key into the gateway.
	resp, payload = doJSON(t, client, http.MethodPost,
		baseURL+"/omc/api/v1/management/repo-keys/applications/"+applied.ApplicationID+"/approve", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d body %s", resp.StatusCode, payload)
	}
	var approved struct {
		Application RepoKeyApplicationDTO `json:"application"`
	}
	if err := json.Unmarshal(payload, &approved); err != nil {
		t.Fatalf("approve response is not JSON: %s", payload)
	}
	if approved.Application.Status != "approved" || approved.Application.KeyMask == "" || !approved.Application.CiphertextReady {
		t.Fatalf("approved application = %+v", approved.Application)
	}
	if approved.Application.DecidedBy != "console" {
		t.Fatalf("the decision names %q, want the console", approved.Application.DecidedBy)
	}
	if len(state.clientKeys) != 3 {
		t.Fatalf("approval did not issue exactly one key: %v", state.clientKeys)
	}
	issued := state.clientKeys[2]

	// The list reports the key by its mask, and the payload as a whole carries no plaintext.
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list after approve status = %d body %s", resp.StatusCode, payload)
	}
	if strings.Contains(string(payload), issued) {
		t.Fatalf("the key list carries a plaintext key: %s", payload)
	}
	if err := json.Unmarshal(payload, &listed); err != nil || len(listed.Keys) != 1 {
		t.Fatalf("listed keys = %s", payload)
	}
	if listed.Keys[0].Status != "active" || listed.Keys[0].Repo != "LambdaTheory/oh-my-cpa" ||
		listed.Keys[0].KeyMask == "" || listed.Keys[0].KeyMask == issued {
		t.Fatalf("listed key = %+v", listed.Keys[0])
	}
	bindingID := listed.Keys[0].ID

	// A second approval (a stale page, a second tab) reads the stored outcome instead of
	// deciding again (AC-8).
	resp, payload = doJSON(t, client, http.MethodPost,
		baseURL+"/omc/api/v1/management/repo-keys/applications/"+applied.ApplicationID+"/approve", "")
	if resp.StatusCode != http.StatusOK || len(state.clientKeys) != 3 {
		t.Fatalf("a repeated approval changed the outcome: %d %s (%v)", resp.StatusCode, payload, state.clientKeys)
	}

	// 4. The detail view carries the binding, its applications and the usage this
	// deployment observed; none of it is key material.
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/"+bindingID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d body %s", resp.StatusCode, payload)
	}
	if strings.Contains(string(payload), issued) {
		t.Fatalf("the detail view carries a plaintext key: %s", payload)
	}
	var detail struct {
		Key          RepoKeyBindingDTO       `json:"key"`
		Stops        []RepoKeyStopDTO        `json:"stops"`
		Deliveries   []RepoKeyDeliveryDTO    `json:"deliveries"`
		Applications []RepoKeyApplicationDTO `json:"applications"`
		Usage        struct {
			Requests int64 `json:"requests"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(payload, &detail); err != nil {
		t.Fatalf("detail response is not JSON: %s", payload)
	}
	if detail.Key.ID != bindingID || len(detail.Stops) != 0 || len(detail.Deliveries) != 0 || len(detail.Applications) != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Usage.Requests != 0 {
		t.Fatalf("a key nobody used reports %d requests", detail.Usage.Requests)
	}

	// 5. A second application from another agent is queued, and a rejection needs a reason.
	secondBody := fmt.Sprintf(`{"repo":"LambdaTheory/oh-my-cpa","purpose":"run evaluations","agent":"codex","public_key":%q}`, repoKeyPublicKey(t))
	resp, payload = doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/repo-keys/applications", secondBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second apply status = %d body %s", resp.StatusCode, payload)
	}
	var second struct {
		ApplicationID string `json:"application_id"`
		Kind          string `json:"kind"`
	}
	if err := json.Unmarshal(payload, &second); err != nil || second.Kind != "reuse" {
		t.Fatalf("second application = %s", payload)
	}
	resp, payload = doJSON(t, client, http.MethodPost,
		baseURL+"/omc/api/v1/management/repo-keys/applications/"+second.ApplicationID+"/reject", `{"reason":""}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a reasonless rejection answered %d: %s", resp.StatusCode, payload)
	}
	resp, payload = doJSON(t, client, http.MethodPost,
		baseURL+"/omc/api/v1/management/repo-keys/applications/"+second.ApplicationID+"/reject", `{"reason":"用途写清楚"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject status = %d body %s", resp.StatusCode, payload)
	}
	if err := json.Unmarshal(payload, &approved); err != nil {
		t.Fatalf("reject response is not JSON: %s", payload)
	}
	if approved.Application.Status != "rejected" || approved.Application.RejectReason != "用途写清楚" {
		t.Fatalf("rejected application = %+v", approved.Application)
	}

	// 6. Stopping takes the key out of the gateway and keeps the record (AC-10, AC-12).
	resp, payload = doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/repo-keys/"+bindingID+"/stop",
		`{"category":"rotate","reason":"quarterly rotation"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d body %s", resp.StatusCode, payload)
	}
	var stopped struct {
		Key RepoKeyBindingDTO `json:"key"`
	}
	if err := json.Unmarshal(payload, &stopped); err != nil {
		t.Fatalf("stop response is not JSON: %s", payload)
	}
	if stopped.Key.Status != "stopped" || stopped.Key.StopCategory != "rotate" || stopped.Key.StopReason != "quarterly rotation" {
		t.Fatalf("stopped key = %+v", stopped.Key)
	}
	for _, key := range state.clientKeys {
		if key == issued {
			t.Fatal("the stopped key is still in the gateway")
		}
	}
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys?status=stopped")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stopped list status = %d body %s", resp.StatusCode, payload)
	}
	if err := json.Unmarshal(payload, &listed); err != nil || len(listed.Keys) != 1 {
		t.Fatalf("stopped keys = %s", payload)
	}

	// The detail view keeps the stop history and the application that used the key.
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/"+bindingID)
	if err := json.Unmarshal(payload, &detail); err != nil {
		t.Fatalf("detail response is not JSON: %s", payload)
	}
	if len(detail.Stops) != 1 || detail.Stops[0].Category != "rotate" || len(detail.Applications) != 1 {
		t.Fatalf("detail after stop = %+v", detail)
	}

	// 7. Re-enabling is a request a person approves (AC-31): the console files it with a
	// reason, it waits in the same queue, and the key comes back from the sealed copy so
	// the operator never pastes a secret and OMC keeps no plaintext (AC-35).
	resp, payload = doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/repo-keys/"+bindingID+"/reenable-request",
		`{"reason":""}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a reasonless re-enable request answered %d: %s", resp.StatusCode, payload)
	}
	resp, payload = doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/repo-keys/"+bindingID+"/reenable-request",
		`{"reason":"stopped by mistake"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reenable request status = %d body %s", resp.StatusCode, payload)
	}
	var reenableRequest struct {
		Application RepoKeyApplicationDTO `json:"application"`
	}
	if err := json.Unmarshal(payload, &reenableRequest); err != nil {
		t.Fatalf("reenable request response is not JSON: %s", payload)
	}
	if reenableRequest.Application.Status != "pending" || reenableRequest.Application.RequestKind != "reenable" ||
		reenableRequest.Application.TargetBindingID != bindingID {
		t.Fatalf("re-enable request = %+v", reenableRequest.Application)
	}
	// Filing it changed nothing yet: the key is still stopped and out of the gateway.
	for _, key := range state.clientKeys {
		if key == issued {
			t.Fatal("the key came back into the gateway before the approval")
		}
	}
	resp, payload = doJSON(t, client, http.MethodPost,
		baseURL+"/omc/api/v1/management/repo-keys/applications/"+reenableRequest.Application.ID+"/approve",
		`{"reason":"stopped by mistake"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reenable approval status = %d body %s", resp.StatusCode, payload)
	}
	if err := json.Unmarshal(payload, &approved); err != nil ||
		approved.Application.Status != "approved" || approved.Application.DecisionReason != "stopped by mistake" {
		t.Fatalf("reenable approval = %s", payload)
	}
	found := false
	for _, key := range state.clientKeys {
		if key == issued {
			found = true
		}
	}
	if !found {
		t.Fatal("the re-enabled key is not back in the gateway")
	}
	// The same key, not a fresh one: a re-enable never issues.
	if len(state.clientKeys) != 3 {
		t.Fatalf("re-enable issued a new key: %v", state.clientKeys)
	}
	// It is active again, and it did not land in the delivery queue. (A fresh value: an
	// omitted field would otherwise keep the stopped_at_ms decoded into the earlier one.)
	var active struct {
		Key RepoKeyBindingDTO `json:"key"`
	}
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/"+bindingID)
	if err := json.Unmarshal(payload, &active); err != nil || active.Key.Status != "active" || active.Key.StoppedAtMS != 0 {
		t.Fatalf("key after re-enable = %v %q %d %s", err, active.Key.Status, active.Key.StoppedAtMS, payload)
	}
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/applications?status=approved&awaiting=true")
	var awaiting struct {
		Applications []RepoKeyApplicationDTO `json:"applications"`
	}
	if err := json.Unmarshal(payload, &awaiting); err != nil {
		t.Fatalf("awaiting list = %s", payload)
	}
	for _, application := range awaiting.Applications {
		if application.ID == reenableRequest.Application.ID {
			t.Fatalf("the re-enable request waits for a delivery a caller never asked for: %+v", application)
		}
	}

	// 8. The reveal is the one place the plaintext leaves, and it is audited on its own.
	resp, payload = doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/repo-keys/"+bindingID+"/reveal",
		`{"reason":"checking why a client fails"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reveal status = %d body %s", resp.StatusCode, payload)
	}
	var revealed struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(payload, &revealed); err != nil || revealed.Key != issued {
		t.Fatalf("reveal = %s", payload)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the reveal is cacheable: %q", resp.Header.Get("Cache-Control"))
	}
	if count := countRepoKeyAudit(t, fixture, "repo_key.reveal"); count != 1 {
		t.Fatalf("reveal audit events = %d, want 1", count)
	}
	// A reveal needs a reason of its own; the empty one is refused.
	resp, payload = doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/repo-keys/"+bindingID+"/reveal", `{"reason":""}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a reasonless reveal answered %d: %s", resp.StatusCode, payload)
	}

	// 9. Unknown records answer 404 rather than an empty success.
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/does-not-exist")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown binding answered %d: %s", resp.StatusCode, payload)
	}
	resp, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/applications?status=someday")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown status filter answered %d: %s", resp.StatusCode, payload)
	}
}

// The proxy application form reads a repository's state before filing, to say whether the
// approval would reuse, re-issue or issue a key (AC-29). The handler answers under a `repo`
// key; this pins that envelope so a flat fixture cannot stand in for it again and hide a
// client that reads the fields one level too high.
func TestManagementRepoKeyRepoStatusEndpointShape(t *testing.T) {
	fixture := newProviderTestFixture(t)
	client, baseURL := fixture.client, fixture.baseURL

	resp, payload := getJSON(t, client, baseURL+"/omc/api/v1/management/repo-keys/repo-status?repo="+url.QueryEscape("LambdaTheory/oh-my-cpa"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repo status = %d body %s", resp.StatusCode, payload)
	}
	var envelope struct {
		Repo  *RepoKeyRepoViewDTO `json:"repo"`
		State *string             `json:"state"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("repo status is not JSON: %s", payload)
	}
	if envelope.Repo == nil || envelope.Repo.State != "none" || envelope.Repo.Repo != "LambdaTheory/oh-my-cpa" {
		t.Fatalf("repo status = %s", payload)
	}
	if envelope.State != nil {
		t.Fatalf("repo status carries a flat state beside the envelope: %s", payload)
	}
}
