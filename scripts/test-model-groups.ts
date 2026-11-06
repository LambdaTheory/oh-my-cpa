import assert from 'node:assert/strict';
import test from 'node:test';
import {
  MODEL_GROUP_NAME_LIMIT,
  countModelGroupAvailability,
  emptyModelGroupAvailabilityCounts,
  formatModelGroupBalance,
  modelGroupById,
  modelGroupNameError,
  modelGroupReading,
  modelGroupRefusalCode,
  moveModelGroupMember,
  reorderModelGroupInList,
  restoreModelOrder,
  withModelGroup,
  withModelOrder,
  withoutModelGroup,
  type ModelGroup,
  type ModelGroupReadings,
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

test('a refused order restores only the order, not the membership that changed since', () => {
  const labor = group('a', 'Labor', ['gpt-5', 'claude']);
  const review = group('b', 'Review', ['gpt-5']);
  // The order was captured when the group held gpt-5 and claude; since then gpt-5 was removed and
  // kimi was added. A whole-snapshot rollback would resurrect gpt-5 and drop kimi.
  labor.members = labor.members.filter((member) => member.model !== 'gpt-5');
  labor.members.push({ model: 'kimi', position: 2, added_at_ms: 9 });
  const restored = restoreModelOrder({ groups: [labor, review] }, 'a', ['gpt-5', 'claude']);
  assert.deepEqual(restored.groups[0].members.map((member) => member.model), ['claude', 'kimi']);
  assert.deepEqual(restored.groups[0].members.map((member) => member.position), [1, 2]);
  // The other group is untouched.
  assert.deepEqual(restored.groups[1].members.map((member) => member.model), ['gpt-5']);
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

function readings(): ModelGroupReadings {
  return {
    models: [
      { model: 'gpt-5', availability: 'available', balance: { amount: '12.5', currency: 'USD' } },
      { model: 'claude-sonnet', availability: 'tight', balance: null },
      { model: 'kimi', availability: 'unavailable', balance: { amount: '0', currency: 'USD' } },
    ],
  };
}

test('a name joins its reading, and a name the server did not report is drawn as unknown', () => {
  assert.deepEqual(modelGroupReading(readings(), 'gpt-5'), {
    model: 'gpt-5',
    availability: 'available',
    balance: { amount: '12.5', currency: 'USD' },
  });
  // No reading at all and no reading for this name are the same answer: unknown, no balance.
  assert.deepEqual(modelGroupReading(readings(), 'gemini'), { model: 'gemini', availability: 'unknown', balance: null });
  assert.deepEqual(modelGroupReading(undefined, 'gpt-5'), { model: 'gpt-5', availability: 'unknown', balance: null });
});

test('the band counts add up to the group, with an unreported member counted as unknown', () => {
  const counts = countModelGroupAvailability(['gpt-5', 'claude-sonnet', 'kimi', 'gemini'], readings());
  assert.deepEqual(counts, { available: 1, tight: 1, unknown: 1, unavailable: 1 });
  assert.equal(Object.values(counts).reduce((sum, count) => sum + count, 0), 4);
  // An empty group and a page that could not read readings both start from all zeros.
  assert.deepEqual(countModelGroupAvailability([], readings()), emptyModelGroupAvailabilityCounts());
  assert.deepEqual(countModelGroupAvailability(['gpt-5'], undefined), {
    available: 0,
    tight: 0,
    unknown: 1,
    unavailable: 0,
  });
});

test('each count is its own record, so counting one group cannot leak into the next', () => {
  const first = emptyModelGroupAvailabilityCounts();
  first.available = 7;
  assert.equal(emptyModelGroupAvailabilityCounts().available, 0);
});

test('a balance is drawn in its own currency, and an absent or empty one as the blank cell', () => {
  // The page draws its own dash for an empty string, so an absent balance must be empty, not "—".
  assert.equal(formatModelGroupBalance(null), '');
  assert.equal(formatModelGroupBalance(undefined), '');
  assert.equal(formatModelGroupBalance({ amount: '', currency: 'USD' }), '');
  assert.equal(formatModelGroupBalance({ amount: '12.5', currency: 'USD' }), '$12.50');
  assert.equal(formatModelGroupBalance({ amount: '150', currency: 'USD' }), '$150.00');
  assert.equal(formatModelGroupBalance({ amount: '12.5', currency: 'EUR' }), new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'EUR',
  }).format(12.5));
  // An amount the page cannot parse, or a currency it cannot format, is shown verbatim rather
  // than swallowed: an unreadable number is still a fact the server stated.
  assert.equal(formatModelGroupBalance({ amount: 'lots', currency: 'USD' }), 'lots USD');
  // A currency `Intl` refuses is stated as the server sent it, not dropped.
  assert.equal(formatModelGroupBalance({ amount: '12.5', currency: 'US' }), '12.5 US');
});
