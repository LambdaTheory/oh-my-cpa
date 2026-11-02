import assert from 'node:assert/strict';
import test from 'node:test';
import {
  MODEL_GROUP_NAME_LIMIT,
  modelGroupById,
  modelGroupNameError,
  modelGroupRefusalCode,
  moveModelGroupMember,
  reorderModelGroupInList,
  withModelGroup,
  withModelOrder,
  withoutModelGroup,
  type ModelGroup,
} from '../web/src/types/modelGroups.ts';

function group(id: string, name: string, models: readonly string[], isPreset = false): ModelGroup {
  return {
    id,
    name,
    is_preset: isPreset,
    created_at_ms: 1,
    updated_at_ms: 1,
    members: models.map((model, index) => ({ model, position: index + 1, added_at_ms: 100 + index })),
  };
}

test('a name is refused when empty, longer than the limit, or already taken case-insensitively', () => {
  const groups = [group('a', 'Labor', []), group('b', '审查组', [])];
  assert.equal(modelGroupNameError('', groups), 'empty');
  assert.equal(modelGroupNameError('   ', groups), 'empty');
  assert.equal(modelGroupNameError('labor', groups), 'conflict');
  assert.equal(modelGroupNameError('  审查组  ', groups), 'conflict');
  assert.equal(modelGroupNameError('Frontend', groups), null);
});

test('the name limit counts runes, not UTF-16 units, matching the server', () => {
  const groups: ModelGroup[] = [];
  const atLimit = '力'.repeat(MODEL_GROUP_NAME_LIMIT);
  const overLimit = '力'.repeat(MODEL_GROUP_NAME_LIMIT + 1);
  assert.equal([...atLimit].length, MODEL_GROUP_NAME_LIMIT);
  assert.equal(modelGroupNameError(atLimit, groups), null);
  assert.equal(modelGroupNameError(overLimit, groups), 'too_long');
});

test('a rename may keep its own name but may not take another group’s', () => {
  const groups = [group('a', 'Labor', []), group('b', 'Frontend', [])];
  assert.equal(modelGroupNameError('Labor', groups, 'a'), null);
  assert.equal(modelGroupNameError('Frontend', groups, 'a'), 'conflict');
});

test('one move swaps exactly two slots and refuses to leave the list', () => {
  const models = ['a', 'b', 'c'];
  assert.deepEqual(moveModelGroupMember(models, 0, 1), ['b', 'a', 'c']);
  assert.deepEqual(moveModelGroupMember(models, 2, -1), ['a', 'c', 'b']);
  // A boundary move returns the same array, so the caller can treat it as "nothing to save".
  assert.equal(moveModelGroupMember(models, 0, -1), models);
  assert.equal(moveModelGroupMember(models, 2, 1), models);
});

test('applying an order renumbers 1..n and keeps each member’s added time', () => {
  const labor = group('a', 'Labor', ['first', 'second', 'third']);
  const reordered = withModelOrder(labor, ['third', 'first', 'second']);
  assert.deepEqual(reordered.members.map((member) => [member.model, member.position]), [
    ['third', 1],
    ['first', 2],
    ['second', 3],
  ]);
  assert.equal(reordered.members.find((member) => member.model === 'third')?.added_at_ms, 102);
  assert.equal(reordered.members.find((member) => member.model === 'first')?.added_at_ms, 100);
});

test('one group’s order is independent of the same model in another group', () => {
  const labor = group('a', 'Labor', ['gpt-5', 'claude']);
  const review = group('b', 'Review', ['gpt-5', 'claude']);
  const list = { groups: [labor, review] };
  const next = reorderModelGroupInList(list, 'a', ['claude', 'gpt-5']);
  assert.deepEqual(next.groups[0].members.map((member) => member.model), ['claude', 'gpt-5']);
  assert.deepEqual(next.groups[1].members.map((member) => member.model), ['gpt-5', 'claude']);
  assert.deepEqual(next.groups[1].members.map((member) => member.position), [1, 2]);
});

test('a write’s answer replaces the stored group, appends a new one, and a delete removes only it', () => {
  const labor = group('a', 'Labor', []);
  const review = group('b', 'Review', ['gpt-5']);
  const list = { groups: [labor, review] };
  const renamed = { ...labor, name: 'Labor二' };
  assert.deepEqual(withModelGroup(list, renamed).groups.map((item) => item.name), ['Labor二', 'Review']);
  assert.deepEqual(withModelGroup(list, group('c', 'Follow-up', [])).groups.map((item) => item.id), ['a', 'b', 'c']);
  assert.deepEqual(withoutModelGroup(list, 'a').groups.map((item) => item.id), ['b']);
  assert.equal(modelGroupById(list, 'b')?.name, 'Review');
  assert.equal(modelGroupById(list, 'missing'), undefined);
});

test('a refusal code is read from the server’s error body and absent from an unshaped failure', () => {
  assert.equal(modelGroupRefusalCode({ data: { code: 'model_group_name_conflict' } }), 'model_group_name_conflict');
  assert.equal(modelGroupRefusalCode({ data: {} }), undefined);
  assert.equal(modelGroupRefusalCode(new Error('offline')), undefined);
  assert.equal(modelGroupRefusalCode(null), undefined);
});
