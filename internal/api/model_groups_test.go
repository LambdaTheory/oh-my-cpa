package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

const modelGroupBase = "/omc/api/v1/management/model-groups"

// modelGroupRequest issues one console call. Writes carry the same-origin header the
// router requires, because these routes go through the shared authentication
// middleware rather than a bare mux.
func modelGroupRequest(t *testing.T, client *http.Client, base, method, path, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", base)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

type modelGroupWire struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IsPreset    bool   `json:"is_preset"`
	CreatedAtMS int64  `json:"created_at_ms"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
	Members     []struct {
		Model    string `json:"model"`
		Position int    `json:"position"`
	} `json:"members"`
}

type modelGroupListWire struct {
	Groups []modelGroupWire `json:"groups"`
}

func decodeModelGroup(t *testing.T, payload []byte) modelGroupWire {
	t.Helper()
	var group modelGroupWire
	if err := json.Unmarshal(payload, &group); err != nil {
		t.Fatalf("decode group %s: %v", payload, err)
	}
	return group
}

func decodeModelGroupList(t *testing.T, payload []byte) modelGroupListWire {
	t.Helper()
	var list modelGroupListWire
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatalf("decode group list %s: %v", payload, err)
	}
	return list
}

func wireMemberNames(group modelGroupWire) []string {
	names := make([]string, 0, len(group.Members))
	for _, member := range group.Members {
		names = append(names, member.Model)
	}
	return names
}

func groupNamed(t *testing.T, list modelGroupListWire, name string) modelGroupWire {
	t.Helper()
	for _, group := range list.Groups {
		if group.Name == name {
			return group
		}
	}
	t.Fatalf("group %q is not in the list", name)
	return modelGroupWire{}
}

func memberPath(groupID, model string) string {
	return modelGroupBase + "/" + groupID + "/members/" + url.PathEscape(model)
}

func requireStatus(t *testing.T, got int, payload []byte, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("status = %d, want %d; body = %s", got, want, payload)
	}
}

func requireCode(t *testing.T, payload []byte, want string) {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode error body %s: %v", payload, err)
	}
	if body["code"] != want {
		t.Fatalf("error code = %q, want %q; body = %s", body["code"], want, payload)
	}
}

// TestModelGroupsStartAsFiveEmptyPresets is the console's first read of a new
// deployment: five named, empty, preset-flagged groups.
func TestModelGroupsStartAsFiveEmptyPresets(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, nil)
	status, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	requireStatus(t, status, payload, http.StatusOK)
	list := decodeModelGroupList(t, payload)
	if len(list.Groups) != 5 {
		t.Fatalf("got %d groups, want 5: %s", len(list.Groups), payload)
	}
	for _, name := range repository.PresetModelGroupNames() {
		group := groupNamed(t, list, name)
		if !group.IsPreset || len(group.Members) != 0 {
			t.Fatalf("preset %q = %+v, want an empty preset group", name, group)
		}
	}
}

func TestModelGroupCreateRenameAndDelete(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, nil)

	status, payload := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase, `{"name":"  我的组  "}`)
	requireStatus(t, status, payload, http.StatusCreated)
	created := decodeModelGroup(t, payload)
	if created.Name != "我的组" || created.IsPreset || len(created.Members) != 0 {
		t.Fatalf("created group = %+v", created)
	}

	// A duplicate name conflicts rather than creating a second group with that name.
	status, payload = modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase, `{"name":"我的组"}`)
	requireStatus(t, status, payload, http.StatusConflict)
	requireCode(t, payload, "model_group_name_conflict")

	// A blank name is a bad request, and an unknown field is refused rather than ignored.
	status, payload = modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase, `{"name":"   "}`)
	requireStatus(t, status, payload, http.StatusBadRequest)
	requireCode(t, payload, "model_group_invalid_name")
	status, payload = modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase, `{"name":"x","is_preset":true}`)
	requireStatus(t, status, payload, http.StatusBadRequest)

	status, payload = modelGroupRequest(t, client, base, http.MethodPatch, modelGroupBase+"/"+created.ID, `{"name":"改名组"}`)
	requireStatus(t, status, payload, http.StatusOK)
	if renamed := decodeModelGroup(t, payload); renamed.Name != "改名组" || renamed.ID != created.ID {
		t.Fatalf("renamed group = %+v", renamed)
	}

	status, payload = modelGroupRequest(t, client, base, http.MethodDelete, modelGroupBase+"/"+created.ID, "")
	requireStatus(t, status, payload, http.StatusNoContent)

	status, payload = modelGroupRequest(t, client, base, http.MethodPatch, modelGroupBase+"/"+created.ID, `{"name":"再改"}`)
	requireStatus(t, status, payload, http.StatusNotFound)
	requireCode(t, payload, "model_group_not_found")
	status, payload = modelGroupRequest(t, client, base, http.MethodDelete, modelGroupBase+"/"+created.ID, "")
	requireStatus(t, status, payload, http.StatusNotFound)
}

// TestModelGroupMemberLifecycle walks the console's add, order and remove calls, with a
// model name that carries a "/" so the path-decoding half is exercised too.
func TestModelGroupMemberLifecycle(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	group := groupNamed(t, decodeModelGroupList(t, payload), "审查组")

	status, payload := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members",
		`{"models":["claude-opus-5","openai/gpt-5-codex","claude-opus-5"]}`)
	requireStatus(t, status, payload, http.StatusOK)
	var added struct {
		Group   modelGroupWire `json:"group"`
		Added   []string       `json:"added"`
		Skipped []string       `json:"skipped"`
	}
	if err := json.Unmarshal(payload, &added); err != nil {
		t.Fatal(err)
	}
	if strings.Join(added.Added, ",") != "claude-opus-5,openai/gpt-5-codex" {
		t.Fatalf("added = %v", added.Added)
	}
	if strings.Join(wireMemberNames(added.Group), ",") != "claude-opus-5,openai/gpt-5-codex" {
		t.Fatalf("members after add = %v", wireMemberNames(added.Group))
	}

	status, payload = modelGroupRequest(t, client, base, http.MethodPut, modelGroupBase+"/"+group.ID+"/order",
		`{"models":["openai/gpt-5-codex","claude-opus-5"]}`)
	requireStatus(t, status, payload, http.StatusOK)
	if ordered := decodeModelGroup(t, payload); strings.Join(wireMemberNames(ordered), ",") != "openai/gpt-5-codex,claude-opus-5" {
		t.Fatalf("members after reorder = %v", wireMemberNames(ordered))
	}

	// A partial order is refused: the rest of the group is not silently dropped.
	status, payload = modelGroupRequest(t, client, base, http.MethodPut, modelGroupBase+"/"+group.ID+"/order",
		`{"models":["claude-opus-5"]}`)
	requireStatus(t, status, payload, http.StatusBadRequest)
	requireCode(t, payload, "model_group_invalid_order")

	// Removing the slash-bearing member by its escaped name closes the gap.
	status, payload = modelGroupRequest(t, client, base, http.MethodDelete, memberPath(group.ID, "openai/gpt-5-codex"), "")
	requireStatus(t, status, payload, http.StatusOK)
	remaining := decodeModelGroup(t, payload)
	if strings.Join(wireMemberNames(remaining), ",") != "claude-opus-5" || remaining.Members[0].Position != 1 {
		t.Fatalf("members after remove = %+v", remaining.Members)
	}
	status, payload = modelGroupRequest(t, client, base, http.MethodDelete, memberPath(group.ID, "openai/gpt-5-codex"), "")
	requireStatus(t, status, payload, http.StatusBadRequest)
	requireCode(t, payload, "model_group_invalid_member")

	status, payload = modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members", `{"models":[]}`)
	requireStatus(t, status, payload, http.StatusBadRequest)
	status, payload = modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/no-such-group/members", `{"models":["x"]}`)
	requireStatus(t, status, payload, http.StatusNotFound)
}

// TestModelGroupSameModelInTwoGroups proves the wire-level claim the PRD makes: one
// model can sit in two groups and each group keeps its own order.
func TestModelGroupSameModelInTwoGroups(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	list := decodeModelGroupList(t, payload)
	labor := groupNamed(t, list, "力工组")
	review := groupNamed(t, list, "审查组")

	for _, group := range []modelGroupWire{labor, review} {
		status, body := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members",
			`{"models":["claude-sonnet-5-5","gpt-5-codex"]}`)
		requireStatus(t, status, body, http.StatusOK)
	}

	status, body := modelGroupRequest(t, client, base, http.MethodPut, modelGroupBase+"/"+labor.ID+"/order",
		`{"models":["gpt-5-codex","claude-sonnet-5-5"]}`)
	requireStatus(t, status, body, http.StatusOK)

	_, payload = modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	list = decodeModelGroupList(t, payload)
	if got := strings.Join(wireMemberNames(groupNamed(t, list, "力工组")), ","); got != "gpt-5-codex,claude-sonnet-5-5" {
		t.Fatalf("力工组 order = %s", got)
	}
	if got := strings.Join(wireMemberNames(groupNamed(t, list, "审查组")), ","); got != "claude-sonnet-5-5,gpt-5-codex" {
		t.Fatalf("审查组 order changed with another group: %s", got)
	}
}

// TestModelGroupRestoreDefaults is the console's "恢复默认组" action, including the
// second press that must create nothing.
func TestModelGroupRestoreDefaults(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	for _, group := range decodeModelGroupList(t, payload).Groups {
		status, body := modelGroupRequest(t, client, base, http.MethodDelete, modelGroupBase+"/"+group.ID, "")
		requireStatus(t, status, body, http.StatusNoContent)
	}

	status, payload := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/restore-defaults", "")
	requireStatus(t, status, payload, http.StatusOK)
	var restored struct {
		Created []modelGroupWire `json:"created"`
		Skipped []string         `json:"skipped"`
	}
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Created) != 5 {
		t.Fatalf("restore created %d groups, want 5: %s", len(restored.Created), payload)
	}
	for _, group := range restored.Created {
		if !group.IsPreset || len(group.Members) != 0 {
			t.Fatalf("restored group = %+v, want an empty preset", group)
		}
	}

	status, payload = modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/restore-defaults", "")
	requireStatus(t, status, payload, http.StatusOK)
	restored = struct {
		Created []modelGroupWire `json:"created"`
		Skipped []string         `json:"skipped"`
	}{}
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Created) != 0 || len(restored.Skipped) != 5 {
		t.Fatalf("second restore = %+v, want nothing created and five skipped", restored)
	}
}

// TestModelGroupRoutesRequireTheConsoleSession proves these are management routes: an
// anonymous caller and a caller presenting a bearer token are both refused before any
// handler runs, even though only the console may write a group.
func TestModelGroupRoutesRequireTheConsoleSession(t *testing.T) {
	_, base, _ := startDashboardTestServer(t, nil)
	anonymous := &http.Client{}
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, modelGroupBase, ""},
		{http.MethodPost, modelGroupBase, `{"name":"x"}`},
		{http.MethodPost, modelGroupBase + "/restore-defaults", ""},
	} {
		request, err := http.NewRequest(route.method, base+route.path, strings.NewReader(route.body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Origin", base)
		if route.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := anonymous.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous %s %s = %d, want 401", route.method, route.path, response.StatusCode)
		}

		// The read-only MCP bearer key reaches read capabilities, not the management
		// surface, so it is not a console session here either.
		bearer, err := http.NewRequest(route.method, base+route.path, strings.NewReader(route.body))
		if err != nil {
			t.Fatal(err)
		}
		bearer.Header.Set("Origin", base)
		bearer.Header.Set("Authorization", "Bearer read-only-key-that-is-long-enough")
		if route.body != "" {
			bearer.Header.Set("Content-Type", "application/json")
		}
		bearerResponse, err := anonymous.Do(bearer)
		if err != nil {
			t.Fatal(err)
		}
		bearerResponse.Body.Close()
		if bearerResponse.StatusCode != http.StatusUnauthorized {
			t.Fatalf("bearer %s %s = %d, want 401", route.method, route.path, bearerResponse.StatusCode)
		}
	}
}

// TestModelGroupWritesAreSameOriginGuarded keeps the cross-site contract: a console
// session alone does not let another origin change a group.
func TestModelGroupWritesAreSameOriginGuarded(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, nil)
	request, err := http.NewRequest(http.MethodPost, base+modelGroupBase, strings.NewReader(`{"name":"cross"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://attacker.test")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin create = %d, want 403", response.StatusCode)
	}
}

// TestModelGroupWritesAreAudited proves each change leaves its trace: the trail is
// where an operator later finds out who renamed or deleted a group.
func TestModelGroupWritesAreAudited(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	group := groupNamed(t, decodeModelGroupList(t, payload), "编排组")

	status, body := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members", `{"models":["gpt-5-codex"]}`)
	requireStatus(t, status, body, http.StatusOK)
	status, body = modelGroupRequest(t, client, base, http.MethodPatch, modelGroupBase+"/"+group.ID, `{"name":"编排二组"}`)
	requireStatus(t, status, body, http.StatusOK)

	page, err := repo.QueryAuditEvents(context.Background(), repository.AuditQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	actions := make(map[string]bool, len(page.Events))
	for _, event := range page.Events {
		if event.TargetType == "model_group" {
			actions[event.Action] = true
		}
	}
	for _, want := range []string{"model_group.members.add", "model_group.rename"} {
		if !actions[want] {
			t.Fatalf("audit trail has no %s entry: %+v", want, actions)
		}
	}
}

// TestModelGroupAuditKeepsTheCallersCorrelationID covers the regression the incremental
// review found: the repository replaced the event's details with its own, so a request
// carrying X-Request-ID lost the only field that ties the row to what the client asked
// about. The business detail and the caller's id have to survive together.
func TestModelGroupAuditKeepsTheCallersCorrelationID(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	const callerID = "caller-correlation-1234"

	request, err := http.NewRequest(http.MethodPost, base+modelGroupBase, strings.NewReader(`{"name":"带关联ID的组"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", base)
	request.Header.Set("X-Request-ID", callerID)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, response.StatusCode, payload, http.StatusCreated)
	created := decodeModelGroup(t, payload)

	page, err := repo.QueryAuditEvents(context.Background(), repository.AuditQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var row *repository.AuditEvent
	for index := range page.Events {
		if page.Events[index].Action == "model_group.create" && page.Events[index].TargetID == created.ID {
			row = &page.Events[index]
			break
		}
	}
	if row == nil {
		t.Fatal("the create recorded no audit row")
	}
	if row.Details["client_request_id"] != callerID {
		t.Fatalf("audit details = %+v, want the caller's X-Request-ID preserved beside the business detail", row.Details)
	}
	if row.Details["name"] != "带关联ID的组" {
		t.Fatalf("audit details = %+v, want the created group's name", row.Details)
	}
	if row.RequestID == "" || row.RequestID == callerID {
		t.Fatalf("request_id = %q: the server-side id must be its own, distinct from the caller's", row.RequestID)
	}
}
