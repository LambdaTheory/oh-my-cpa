import assert from 'node:assert/strict';
import test from 'node:test';

import { isRequestGroupingReady } from './acceptance/requestGrouping.mjs';

const expected = { groupTitle: 'Client not recorded', firstRowId: 'req_interaction_0000' };

test('the stale source grouping does not satisfy the client grouping wait', () => {
  assert.equal(isRequestGroupingReady(
    { groupTitleCount: 1, firstGroupTitle: 'openai', firstRowId: expected.firstRowId },
    expected,
  ), false);
});

test('the committed client grouping with its expected title satisfies the wait', () => {
  assert.equal(isRequestGroupingReady(
    { groupTitleCount: 1, firstGroupTitle: 'Client not recorded', firstRowId: expected.firstRowId },
    expected,
  ), true);
});

test('a missing header or a different first row does not satisfy the wait', () => {
  assert.equal(isRequestGroupingReady(
    { groupTitleCount: 0, firstGroupTitle: '', firstRowId: expected.firstRowId },
    expected,
  ), false);
  assert.equal(isRequestGroupingReady(
    { groupTitleCount: 1, firstGroupTitle: expected.groupTitle, firstRowId: 'req_interaction_0001' },
    expected,
  ), false);
});
