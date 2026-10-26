import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { automationReasonKey, automationRowReasonKey, matchesAutomationFacet, projectAutomationAccount, projectAutomationRoster } from '../web/src/components/automation/accountAutomationModel.ts';
import type { AccountAutomationAccountDTO, AccountAutomationListResponse } from '../web/src/types/accountAutomation.ts';

const account = (overrides: Partial<AccountAutomationAccountDTO> = {}): AccountAutomationAccountDTO => ({
  auth_index: 'synthetic', is_adopted: true, is_enabled: true, burn_credit: false, removed: false,
  state: 'monitoring', auto_disable_supported: true, disabled_by_automation: false,
  attempt_count: 0, intent_version: 1, has_weekly_evidence: false, ...overrides,
});

test('display precedence preserves pause, capability, error and recovery ahead of Credit', () => {
  for (const [overrides, state] of [
    [{ is_enabled: false, burn_credit: true, state: 'error', disabled_by_automation: true }, 'paused'],
    [{ auto_disable_supported: false, burn_credit: true, state: 'error' }, 'monitor_only'],
    [{ monitor_reason: 'credential_missing_name', burn_credit: true }, 'monitor_only'],
    [{ monitor_reason: 'credential_not_found', burn_credit: true }, 'monitor_only'],
    [{ burn_credit: true, state: 'error', disabled_by_automation: true }, 'error'],
    [{ burn_credit: true, disabled_by_automation: true }, 'disabled'],
    [{ burn_credit: true, recovery_at_ms: 1000, recovery_status: 'scheduled' }, 'monitoring'],
    [{ burn_credit: true }, 'credit'],
    [{ state: 'idle' }, 'monitoring'],
    [{ state: 'future_state' }, 'error'],
  ] as const) assert.equal(projectAutomationAccount(account(overrides)).state, state);
  assert.equal(automationReasonKey('UNRECOGNIZED_CODE'), 'automation.reason_unknown');
  assert.equal(automationRowReasonKey(projectAutomationAccount(account({ auto_disable_supported: false, monitor_reason: 'no_weekly_evidence' }))), 'automation.reason_capability_short');
});

test('identity mismatch retains its specific reason without an execution error', () => {
  const row = projectAutomationAccount(account({ state: 'monitoring', monitor_reason: 'evidence_identity_mismatch' }));
  assert.equal(row.state, 'error');
  assert.equal(automationRowReasonKey(row), 'automation.reason_evidence_identity_mismatch');
});

test('a pending retry independently projects an error from monitoring', () => {
  const row = projectAutomationAccount(account({ state: 'monitoring', next_attempt_at_ms: 2000 }));
  assert.equal(row.hasBackoff, true);
  assert.equal(row.state, 'error');
  assert.equal(matchesAutomationFacet(row, 'error'), true);
  assert.equal(automationRowReasonKey(row), 'automation.reason_retry_count');
});

test('statistics and facets share roster facts, retained plans overlap explicitly and tombstones stay absent', () => {
  const accounts = [account(), account({ is_enabled: false, disabled_by_automation: true, recovery_status: 'scheduled', recovery_at_ms: 1000 }),
    account({ state: 'error', next_attempt_at_ms: 2000, recovery_status: 'overdue', recovery_at_ms: 1000 }),
    account({ auto_disable_supported: false }), account({ removed: true }), account({ is_adopted: false })];
  const data: AccountAutomationListResponse = { accounts, candidates: [], settings: { enabled: true, web_enabled: true, source: 'web', master_from_env: false, accounts: 4 } };
  const { rows, stats } = projectAutomationRoster(data);
  assert.deepEqual(stats, { adopted: 4, credentials: 0, monitorOnly: 1, disabled: 1, waiting: 2, backoff: 1 });
  assert.deepEqual(['all', 'controlled', 'paused', 'disabled', 'monitor_only', 'error'].map(facet => rows.filter(row => matchesAutomationFacet(row, facet as Parameters<typeof matchesAutomationFacet>[1])).length), [4, 2, 1, 2, 1, 1]);
  assert.equal(projectAutomationRoster({ ...data, candidates_degraded: true }).stats.credentials, null);
  assert.equal(projectAutomationAccount(account({ recovery_at_ms: 1000 })).hasRecovery, false);
});
