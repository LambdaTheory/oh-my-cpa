package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// These tests pin the P1 the review found: every group write used to commit first and
// record its audit row afterwards, so a 500 meant "the change happened, the record did
// not". Each case below breaks the audit store with a synthetic trigger, issues the
// write, and then asserts BOTH halves of the contract - the caller is told the write
// failed with a stable code, and the database is exactly as it was before. A test that
// only checked the status would pass against the broken ordering this replaces.

// refuseAuditWrites installs a trigger that makes every audit_events insert fail. It is
// the same shape as a store that has lost its disk or been locked by another writer, and
// it fails for the real reason the production path would rather than through a seam the
// handler could be told to skip.
func refuseAuditWrites(t *testing.T, repo *repository.Repository) {
	t.Helper()
	if _, err := repo.SQL().ExecContext(context.Background(), `
		CREATE TRIGGER refuse_audit_writes BEFORE INSERT ON audit_events
		BEGIN SELECT RAISE(ABORT, 'synthetic audit store failure'); END`); err != nil {
		t.Fatalf("install audit refusal trigger: %v", err)
	}
}

// auditWriteRefused is the machine-readable half of the refusal: a caller must be able
// to tell an unavailable audit store from a rejected name without matching prose.
func auditWriteRefused(t *testing.T, status int, payload []byte) {
	t.Helper()
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", status, payload)
	}
	var body map[string]string
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode error body %s: %v", payload, err)
	}
	if body["code"] != auditWriteFailedCode {
		t.Fatalf("error code = %q, want %q; body = %s", body["code"], auditWriteFailedCode, payload)
	}
}

// groupSnapshot reads one group's name and order straight from the database, so an
// assertion about "nothing changed" does not go through the API that just failed.
func modelGroupState(t *testing.T, repo *repository.Repository, id string) (string, []string) {
	t.Helper()
	group, err := repo.GetModelGroup(context.Background(), id)
	if err != nil {
		t.Fatalf("read group %s: %v", id, err)
	}
	order := make([]string, 0, len(group.Members))
	for _, member := range group.Members {
		order = append(order, member.Model)
	}
	return group.Name, order
}

func modelGroupCount(t *testing.T, repo *repository.Repository) int {
	t.Helper()
	var count int
	if err := repo.SQL().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM model_groups`).Scan(&count); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	return count
}

// TestModelGroupCreateIsRolledBackWhenTheAuditFails covers the create path.
func TestModelGroupCreateIsRolledBackWhenTheAuditFails(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	before := modelGroupCount(t, repo)
	refuseAuditWrites(t, repo)

	status, payload := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase, `{"name":"不该出现的组"}`)
	auditWriteRefused(t, status, payload)

	if after := modelGroupCount(t, repo); after != before {
		t.Fatalf("a refused create left a group behind: count %d, want %d", after, before)
	}
	_, list := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	if strings.Contains(string(list), "不该出现的组") {
		t.Fatalf("the refused name is readable: %s", list)
	}
}

// TestModelGroupRenameIsRolledBackWhenTheAuditFails covers rename, including that the
// name the caller sees afterwards is the original one.
func TestModelGroupRenameIsRolledBackWhenTheAuditFails(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	group := groupNamed(t, decodeModelGroupList(t, payload), "审查组")
	refuseAuditWrites(t, repo)

	status, body := modelGroupRequest(t, client, base, http.MethodPatch, modelGroupBase+"/"+group.ID, `{"name":"改过的名字"}`)
	auditWriteRefused(t, status, body)

	if name, _ := modelGroupState(t, repo, group.ID); name != "审查组" {
		t.Fatalf("a refused rename changed the stored name to %q", name)
	}
}

// TestModelGroupDeleteIsRolledBackWhenTheAuditFails covers delete: the group and all of
// its members must still be there.
func TestModelGroupDeleteIsRolledBackWhenTheAuditFails(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	group := groupNamed(t, decodeModelGroupList(t, payload), "编排组")
	status, body := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members", `{"models":["keep-me"]}`)
	requireStatus(t, status, body, http.StatusOK)
	before := modelGroupCount(t, repo)
	refuseAuditWrites(t, repo)

	status, body = modelGroupRequest(t, client, base, http.MethodDelete, modelGroupBase+"/"+group.ID, "")
	auditWriteRefused(t, status, body)

	if after := modelGroupCount(t, repo); after != before {
		t.Fatalf("a refused delete removed a group: count %d, want %d", after, before)
	}
	if name, order := modelGroupState(t, repo, group.ID); name != "编排组" || strings.Join(order, ",") != "keep-me" {
		t.Fatalf("a refused delete changed the group: name %q, order %v", name, order)
	}
}

// TestModelGroupAddMembersIsRolledBackWhenTheAuditFails covers add: no member may be
// appended and the group's updated_at must not move.
func TestModelGroupAddMembersIsRolledBackWhenTheAuditFails(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	group := groupNamed(t, decodeModelGroupList(t, payload), "力工组")
	status, body := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members", `{"models":["first"]}`)
	requireStatus(t, status, body, http.StatusOK)
	_, beforeOrder := modelGroupState(t, repo, group.ID)
	beforeStamp := modelGroupUpdatedAt(t, repo, group.ID)
	refuseAuditWrites(t, repo)

	status, body = modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members", `{"models":["second","third"]}`)
	auditWriteRefused(t, status, body)

	if _, order := modelGroupState(t, repo, group.ID); strings.Join(order, ",") != strings.Join(beforeOrder, ",") {
		t.Fatalf("a refused add changed the members: %v, want %v", order, beforeOrder)
	}
	if stamp := modelGroupUpdatedAt(t, repo, group.ID); stamp != beforeStamp {
		t.Fatalf("a refused add touched updated_at: %d, want %d", stamp, beforeStamp)
	}
}

// TestModelGroupRemoveMemberIsRolledBackWhenTheAuditFails covers remove, including that
// the surviving positions are not renumbered.
func TestModelGroupRemoveMemberIsRolledBackWhenTheAuditFails(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	group := groupNamed(t, decodeModelGroupList(t, payload), "跟进组")
	status, body := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members", `{"models":["a","b","c"]}`)
	requireStatus(t, status, body, http.StatusOK)
	before := modelGroupPositions(t, repo, group.ID)
	refuseAuditWrites(t, repo)

	status, body = modelGroupRequest(t, client, base, http.MethodDelete, memberPath(group.ID, "b"), "")
	auditWriteRefused(t, status, body)

	if after := modelGroupPositions(t, repo, group.ID); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("a refused remove changed the members or their positions: %v, want %v", after, before)
	}
}

// TestModelGroupReorderIsRolledBackWhenTheAuditFails covers reorder: the stored order
// must be the one that was there before the refused call.
func TestModelGroupReorderIsRolledBackWhenTheAuditFails(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	group := groupNamed(t, decodeModelGroupList(t, payload), "前端组")
	status, body := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/"+group.ID+"/members", `{"models":["a","b","c"]}`)
	requireStatus(t, status, body, http.StatusOK)
	_, beforeOrder := modelGroupState(t, repo, group.ID)
	refuseAuditWrites(t, repo)

	status, body = modelGroupRequest(t, client, base, http.MethodPut, modelGroupBase+"/"+group.ID+"/order", `{"models":["c","b","a"]}`)
	auditWriteRefused(t, status, body)

	if _, order := modelGroupState(t, repo, group.ID); strings.Join(order, ",") != strings.Join(beforeOrder, ",") {
		t.Fatalf("a refused reorder changed the order: %v, want %v", order, beforeOrder)
	}
}

// TestModelGroupRestoreIsRolledBackWhenTheAuditFails covers restore, which creates
// several rows at once: a partial restore would be a state no request explains.
func TestModelGroupRestoreIsRolledBackWhenTheAuditFails(t *testing.T) {
	client, base, repo := startDashboardTestServer(t, nil)
	_, payload := modelGroupRequest(t, client, base, http.MethodGet, modelGroupBase, "")
	for _, group := range decodeModelGroupList(t, payload).Groups {
		status, body := modelGroupRequest(t, client, base, http.MethodDelete, modelGroupBase+"/"+group.ID, "")
		requireStatus(t, status, body, http.StatusNoContent)
	}
	refuseAuditWrites(t, repo)

	status, body := modelGroupRequest(t, client, base, http.MethodPost, modelGroupBase+"/restore-defaults", "")
	auditWriteRefused(t, status, body)

	if after := modelGroupCount(t, repo); after != 0 {
		t.Fatalf("a refused restore created %d groups", after)
	}
}

func modelGroupUpdatedAt(t *testing.T, repo *repository.Repository, id string) int64 {
	t.Helper()
	var stamp int64
	if err := repo.SQL().QueryRowContext(context.Background(),
		`SELECT updated_at_ms FROM model_groups WHERE id = ?`, id).Scan(&stamp); err != nil {
		t.Fatalf("read updated_at for %s: %v", id, err)
	}
	return stamp
}

func modelGroupPositions(t *testing.T, repo *repository.Repository, id string) []string {
	t.Helper()
	rows, err := repo.SQL().QueryContext(context.Background(),
		`SELECT model || '@' || position FROM model_group_members WHERE group_id = ? ORDER BY position`, id)
	if err != nil {
		t.Fatalf("read members for %s: %v", id, err)
	}
	defer rows.Close()
	positions := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("scan member: %v", err)
		}
		positions = append(positions, value)
	}
	return positions
}
