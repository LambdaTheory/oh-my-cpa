package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func modelGroupRepo(t *testing.T) (*Repository, context.Context) {
	t.Helper()
	return usageTestRepository(t), context.Background()
}

// findGroupByName resolves a group by its stored name, because the tests reason in
// names the operator chose rather than in generated ids.
func findGroupByName(t *testing.T, repo *Repository, ctx context.Context, name string) ModelGroup {
	t.Helper()
	groups, err := repo.ListModelGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.Name == name {
			return group
		}
	}
	t.Fatalf("group %q is not listed", name)
	return ModelGroup{}
}

func memberNames(group ModelGroup) []string {
	names := make([]string, 0, len(group.Members))
	for _, member := range group.Members {
		names = append(names, member.Model)
	}
	return names
}

func groupNames(groups []ModelGroup) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.Name)
	}
	return names
}

func TestMigrationSeedsTheFivePresetGroups(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	groups, err := repo.ListModelGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := PresetModelGroupNames()
	if len(groups) != len(want) {
		t.Fatalf("a fresh database has %d groups, want the %d presets: %+v", len(groups), len(want), groups)
	}
	byName := make(map[string]ModelGroup, len(groups))
	for _, group := range groups {
		byName[group.Name] = group
	}
	for _, name := range want {
		group, found := byName[name]
		if !found {
			t.Fatalf("preset group %q is missing from %v", name, byName)
		}
		if !group.IsPreset {
			t.Errorf("preset group %q is not marked as one", name)
		}
		if len(group.Members) != 0 {
			t.Errorf("preset group %q must start empty, has %v", name, memberNames(group))
		}
	}
	// The presets list in the order they are declared, not in byte order, so the console
	// draws 力工组 → 前端组 → 编排组 → 审查组 → 跟进组 every time.
	for index, name := range want {
		if groups[index].Name != name {
			t.Fatalf("preset order = %v, want %v", groupNames(groups), want)
		}
	}
}

func TestModelGroupNameIsUniqueAndBounded(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	if _, err := repo.CreateModelGroup(ctx, "   ", 1000); !errors.Is(err, ErrModelGroupName) {
		t.Fatalf("blank name = %v, want ErrModelGroupName", err)
	}
	if _, err := repo.CreateModelGroup(ctx, strings.Repeat("组", ModelGroupNameLimit+1), 1000); !errors.Is(err, ErrModelGroupName) {
		t.Fatalf("name over the rune limit = %v, want ErrModelGroupName", err)
	}
	// The limit counts runes, so a name of exactly the ceiling is accepted even though
	// it is more than forty bytes.
	if _, err := repo.CreateModelGroup(ctx, strings.Repeat("组", ModelGroupNameLimit), 1000); err != nil {
		t.Fatalf("name at the rune limit: %v", err)
	}
	// A preset's name is taken; a collision reports a conflict rather than a bad name,
	// because the name is well formed and only the uniqueness refused it.
	if _, err := repo.CreateModelGroup(ctx, "力工组", 1000); !errors.Is(err, ErrModelGroupNameConflict) {
		t.Fatalf("duplicate preset name = %v, want ErrModelGroupNameConflict", err)
	}
	// Case-insensitively, so "Review" cannot shadow "review".
	if _, err := repo.CreateModelGroup(ctx, "Review", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateModelGroup(ctx, "REVIEW", 1000); !errors.Is(err, ErrModelGroupNameConflict) {
		t.Fatalf("case-variant duplicate = %v, want ErrModelGroupNameConflict", err)
	}
}

func TestModelGroupRenameAndDelete(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	group := findGroupByName(t, repo, ctx, "力工组")

	renamed, err := repo.RenameModelGroup(ctx, group.ID, "主力组", 2000)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "主力组" {
		t.Fatalf("renamed to %q", renamed.Name)
	}
	if renamed.CreatedAtMS != group.CreatedAtMS || renamed.UpdatedAtMS != 2000 {
		t.Fatalf("timestamps after rename = %+v", renamed)
	}
	// The id is the identity; a rename does not move the group.
	if renamed.ID != group.ID || !renamed.IsPreset {
		t.Fatalf("rename changed identity or preset flag: %+v", renamed)
	}
	if _, err := repo.RenameModelGroup(ctx, group.ID, "前端组", 2000); !errors.Is(err, ErrModelGroupNameConflict) {
		t.Fatalf("rename onto a taken name = %v, want ErrModelGroupNameConflict", err)
	}
	if _, err := repo.RenameModelGroup(ctx, "no-such-group", "x", 2000); !errors.Is(err, ErrModelGroupNotFound) {
		t.Fatalf("rename of a missing group = %v, want ErrModelGroupNotFound", err)
	}
	if _, err := repo.RenameModelGroup(ctx, group.ID, "", 2000); !errors.Is(err, ErrModelGroupName) {
		t.Fatalf("rename to an empty name = %v, want ErrModelGroupName", err)
	}

	removed, err := repo.DeleteModelGroup(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("deleting an existing group reported nothing removed")
	}
	if _, err := repo.GetModelGroup(ctx, group.ID); !errors.Is(err, ErrModelGroupNotFound) {
		t.Fatalf("deleted group is still readable: %v", err)
	}
	if removed, err := repo.DeleteModelGroup(ctx, group.ID); err != nil || removed {
		t.Fatalf("second delete = (%v, %v), want (false, nil)", removed, err)
	}
}

func TestModelGroupMembersAppendAndCloseTheGap(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	group := findGroupByName(t, repo, ctx, "审查组")

	added, err := repo.AddModelGroupMembers(ctx, group.ID, []string{"claude-opus-5", "gpt-5-codex", "claude-opus-5"}, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := added.Added, []string{"claude-opus-5", "gpt-5-codex"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("added = %v, want %v (the repeated name is one member)", got, want)
	}
	if len(added.Skipped) != 0 {
		t.Fatalf("skipped = %v, want none in the first add", added.Skipped)
	}
	assertMembers(t, added.Group, []string{"claude-opus-5", "gpt-5-codex"})

	// Re-adding a present model reports it as skipped and appends only the new one,
	// after the current last member.
	again, err := repo.AddModelGroupMembers(ctx, group.ID, []string{"gpt-5-codex", "gemini-3-pro"}, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := again.Added, []string{"gemini-3-pro"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("added on re-add = %v, want %v", got, want)
	}
	if got, want := again.Skipped, []string{"gpt-5-codex"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("skipped on re-add = %v, want %v", got, want)
	}
	assertMembers(t, again.Group, []string{"claude-opus-5", "gpt-5-codex", "gemini-3-pro"})

	// Removing a middle member closes the gap: positions stay 1..n with no hole.
	afterRemove, err := repo.RemoveModelGroupMember(ctx, group.ID, "gpt-5-codex", 5000)
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, afterRemove, []string{"claude-opus-5", "gemini-3-pro"})
	if afterRemove.Members[1].Position != 2 {
		t.Fatalf("position after removing the middle member = %d, want 2", afterRemove.Members[1].Position)
	}
	if afterRemove.UpdatedAtMS != 5000 {
		t.Fatalf("updated_at after member removal = %d, want 5000", afterRemove.UpdatedAtMS)
	}

	if _, err := repo.RemoveModelGroupMember(ctx, group.ID, "not-in-group", 6000); !errors.Is(err, ErrModelGroupMember) {
		t.Fatalf("removing an absent member = %v, want ErrModelGroupMember", err)
	}
	if _, err := repo.AddModelGroupMembers(ctx, "no-such-group", []string{"x"}, 6000); !errors.Is(err, ErrModelGroupNotFound) {
		t.Fatalf("adding to a missing group = %v, want ErrModelGroupNotFound", err)
	}
	if _, err := repo.AddModelGroupMembers(ctx, group.ID, nil, 6000); !errors.Is(err, ErrModelGroupMember) {
		t.Fatalf("adding nothing = %v, want ErrModelGroupMember", err)
	}
}

// TestOneModelInTwoGroupsOrdersIndependently is the acceptance claim: the same model
// can be in both groups, and reordering one group leaves the other's order alone.
func TestOneModelInTwoGroupsOrdersIndependently(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	labor := findGroupByName(t, repo, ctx, "力工组")
	review := findGroupByName(t, repo, ctx, "审查组")
	shared := []string{"claude-sonnet-5-5", "gpt-5-codex", "gemini-3-pro"}

	if _, err := repo.AddModelGroupMembers(ctx, labor.ID, shared, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddModelGroupMembers(ctx, review.ID, shared, 1000); err != nil {
		t.Fatal(err)
	}

	// The same model in a second group is an ordinary member there, not a conflict.
	reordered, err := repo.ReorderModelGroupMembers(ctx, labor.ID,
		[]string{"gemini-3-pro", "claude-sonnet-5-5", "gpt-5-codex"}, 2000)
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	assertMembers(t, reordered, []string{"gemini-3-pro", "claude-sonnet-5-5", "gpt-5-codex"})

	untouched, err := repo.GetModelGroup(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, untouched, shared)
	if untouched.UpdatedAtMS != 1000 {
		t.Fatalf("reordering another group touched this one's updated_at (%d)", untouched.UpdatedAtMS)
	}

	// Removing the model from one group leaves it in the other.
	if _, err := repo.RemoveModelGroupMember(ctx, labor.ID, "gpt-5-codex", 3000); err != nil {
		t.Fatal(err)
	}
	stillThere, err := repo.GetModelGroup(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, stillThere, shared)
}

func TestModelGroupReorderRefusesAPartialOrUnknownOrder(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	group := findGroupByName(t, repo, ctx, "编排组")
	if _, err := repo.AddModelGroupMembers(ctx, group.ID, []string{"a", "b", "c"}, 1000); err != nil {
		t.Fatal(err)
	}
	for _, order := range [][]string{
		{"a", "b"},            // one member omitted would silently drop it
		{"a", "b", "c", "d"},  // an unknown name would silently add it
		{"a", "b", "b"},       // a repeat would leave one member unpinned
		{"a", "b", "missing"}, // a name the group does not hold
	} {
		if _, err := repo.ReorderModelGroupMembers(ctx, group.ID, order, 2000); !errors.Is(err, ErrModelGroupOrder) {
			t.Fatalf("order %v = %v, want ErrModelGroupOrder", order, err)
		}
	}
	// A refused reorder changes nothing.
	unchanged, err := repo.GetModelGroup(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, unchanged, []string{"a", "b", "c"})

	// An empty order is valid for an empty group and refused for a populated one.
	empty := findGroupByName(t, repo, ctx, "跟进组")
	if _, err := repo.ReorderModelGroupMembers(ctx, empty.ID, nil, 2000); err != nil {
		t.Fatalf("reordering an empty group: %v", err)
	}
}

func TestDeleteModelGroupRemovesMembersButNotTheOtherGroup(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	labor := findGroupByName(t, repo, ctx, "力工组")
	review := findGroupByName(t, repo, ctx, "审查组")
	for _, group := range []ModelGroup{labor, review} {
		if _, err := repo.AddModelGroupMembers(ctx, group.ID, []string{"shared-model"}, 1000); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := repo.DeleteModelGroup(ctx, labor.ID); err != nil {
		t.Fatal(err)
	}
	var orphaned int
	if err := repo.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM model_group_members WHERE group_id = ?`, labor.ID).Scan(&orphaned); err != nil {
		t.Fatal(err)
	}
	if orphaned != 0 {
		t.Fatalf("%d member rows survived their group", orphaned)
	}
	remaining, err := repo.GetModelGroup(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, remaining, []string{"shared-model"})
}

// TestRestoreDefaultGroupsBringsBackWhatWasDeleted pins the acceptance claim: after
// deleting the presets, one restore brings all five back empty, and a second restore
// creates nothing because they are all present again.
func TestRestoreDefaultGroupsBringsBackWhatWasDeleted(t *testing.T) {
	repo, ctx := modelGroupRepo(t)

	// One preset is filled and renamed first, so restore has to leave an existing,
	// modified preset exactly as it is rather than re-seeding it.
	filled := findGroupByName(t, repo, ctx, "力工组")
	if _, err := repo.AddModelGroupMembers(ctx, filled.ID, []string{"claude-opus-5"}, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RenameModelGroup(ctx, filled.ID, "我的力工组", 1100); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"前端组", "编排组", "审查组", "跟进组"} {
		group := findGroupByName(t, repo, ctx, name)
		if _, err := repo.DeleteModelGroup(ctx, group.ID); err != nil {
			t.Fatal(err)
		}
	}

	restored, err := repo.RestoreDefaultModelGroups(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Created) != 4 {
		t.Fatalf("restored %d groups, want 4: %v", len(restored.Created), restored.Created)
	}
	for _, group := range restored.Created {
		if !group.IsPreset || len(group.Members) != 0 {
			t.Fatalf("restored group is not an empty preset: %+v", group)
		}
	}
	// The renamed preset keeps its name and its member; restore recognised it by id.
	kept, err := repo.GetModelGroup(ctx, filled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Name != "我的力工组" {
		t.Fatalf("restore overwrote a renamed preset: %q", kept.Name)
	}
	assertMembers(t, kept, []string{"claude-opus-5"})

	groups, err := repo.ListModelGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 5 {
		t.Fatalf("after restore there are %d groups, want 5: %v", len(groups), groups)
	}

	// Idempotent: a second call creates nothing and reports all five as present.
	second, err := repo.RestoreDefaultModelGroups(ctx, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Created) != 0 {
		t.Fatalf("second restore created %v", second.Created)
	}
	if len(second.Skipped) != 5 {
		t.Fatalf("second restore skipped %v, want all five", second.Skipped)
	}
	var count int
	if err := repo.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM model_groups`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("after two restores there are %d groups, want 5", count)
	}
}

// TestRestoreDefaultGroupsLeavesAUserGroupWithAPresetNameAlone covers the one collision
// restore cannot fix by inserting: the operator made their own group called 力工组 after
// deleting the preset, so the name is taken by a group restore must not touch.
func TestRestoreDefaultGroupsLeavesAUserGroupWithAPresetNameAlone(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	preset := findGroupByName(t, repo, ctx, "力工组")
	if _, err := repo.DeleteModelGroup(ctx, preset.ID); err != nil {
		t.Fatal(err)
	}
	own, err := repo.CreateModelGroup(ctx, " 力工组 ", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if own.Name != "力工组" || own.IsPreset {
		t.Fatalf("operator group = %+v", own)
	}
	if _, err := repo.AddModelGroupMembers(ctx, own.ID, []string{"mine"}, 1000); err != nil {
		t.Fatal(err)
	}

	restored, err := repo.RestoreDefaultModelGroups(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Created) != 0 {
		t.Fatalf("restore created %v over a name the operator holds", restored.Created)
	}
	kept, err := repo.GetModelGroup(ctx, own.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMembers(t, kept, []string{"mine"})
	groups, err := repo.ListModelGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 5 {
		t.Fatalf("group count = %d, want 5 (the operator's own plus four presets)", len(groups))
	}
}

func assertMembers(t *testing.T, group ModelGroup, want []string) {
	t.Helper()
	if got := memberNames(group); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("group %q members = %v, want %v", group.Name, got, want)
	}
	for index, member := range group.Members {
		if member.Position != index+1 {
			t.Fatalf("group %q member %q at position %d, want %d", group.Name, member.Model, member.Position, index+1)
		}
	}
}

// TestModelGroupWritesSurviveAReopen proves the rows are committed rather than held in
// the connection's imagination, which is what makes a group exist across a restart.
func TestModelGroupWritesSurviveAReopen(t *testing.T) {
	path := fmt.Sprintf("%s/model-groups.db", t.TempDir())
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	group, err := repo.CreateModelGroup(ctx, "持久组", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddModelGroupMembers(ctx, group.ID, []string{"m1", "m2"}, 1000); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reread, err := New(reopened).GetModelGroup(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Name != "持久组" {
		t.Fatalf("name after reopen = %q", reread.Name)
	}
	assertMembers(t, reread, []string{"m1", "m2"})
}
