import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  automationReasonKey,
  automationRowReasonKey,
  matchesAutomationFacet,
  projectAutomationCredential,
  projectAutomationRoster,
} from '../web/src/components/automation/accountAutomationModel.ts';
import type {
  AccountAutomationAccountDTO,
  AccountAutomationCredentialDTO,
  AccountAutomationListResponse,
} from '../web/src/types/accountAutomation.ts';

const plan = (overrides: Partial<AccountAutomationAccountDTO> = {}): AccountAutomationAccountDTO => ({
  auth_index: 'synthetic', is_adopted: true, is_enabled: true, burn_credit: false, removed: false,
  state: 'monitoring', auto_disable_supported: true, disabled_by_automation: false,
  attempt_count: 0, intent_version: 1, has_weekly_evidence: false, ...overrides,
});

const credential = (overrides: Partial<AccountAutomationCredentialDTO> = {}): AccountAutomationCredentialDTO => {
  const merged = { auth_index: 'synthetic', provider: 'codex', name: 'synthetic.json', disabled: false, controllable: true, managed: false, adoptable: true, auto_disable_supported: true, ...overrides };
  if (merged.managed && !('plan' in overrides)) merged.plan = plan({ auth_index: merged.auth_index });
  return merged;
};

const response = (credentials: AccountAutomationCredentialDTO[], overrides: Partial<AccountAutomationListResponse> = {}): AccountAutomationListResponse => ({
  settings: { enabled: true, web_enabled: true, source: 'web', master_from_env: false, accounts: credentials.filter(row => row.managed).length },
  inventory: {
    available: true,
    credentials: credentials.length,
    managed: credentials.filter(row => row.managed).length,
    unmanaged: credentials.filter(row => !row.managed).length,
    disabled: credentials.filter(row => row.disabled).length,
    pending_absence: 0,
  },
  credentials,
  accounts: credentials.filter(row => row.managed && row.plan).map(row => row.plan as AccountAutomationAccountDTO),
  accounts_last_known: false,
  candidates: [],
  ...overrides,
});

test('the main table is built from current credentials, so a duplicate label stays two rows', () => {
  const credentials = [
    credential({ auth_index: 'idx-a', account_label: 'ops@example.test', managed: true }),
    credential({ auth_index: 'idx-b', account_label: 'ops@example.test', managed: true }),
    credential({ auth_index: 'idx-c', account_label: 'ops@example.test', managed: false }),
  ];
  const { rows, stats } = projectAutomationRoster(response(credentials));
  assert.equal(rows.length, 3);
  assert.equal(new Set(rows.map(row => row.credential.auth_index)).size, 3);
  assert.deepEqual(stats, { credentials: 3, managed: 2, unmanaged: 1, gatewayDisabled: 0, paused: 0, recovery: 0, pendingAbsence: 0 });
});

test('unmanaged and paused are distinct states with localized reasons', () => {
  const unmanaged = projectAutomationCredential(credential({ managed: false }));
  assert.equal(unmanaged.state, 'unmanaged');
  assert.equal(unmanaged.isManaged, false);
  assert.equal(automationRowReasonKey(unmanaged), 'automation.reason_unmanaged');
  assert.equal(matchesAutomationFacet(unmanaged, 'unmanaged'), true);
  assert.equal(matchesAutomationFacet(unmanaged, 'paused'), false);

  const blocked = projectAutomationCredential(credential({ managed: false, adoptable: false, controllable: false }));
  assert.equal(automationRowReasonKey(blocked), 'automation.reason_unmanaged_blocked');

  const paused = projectAutomationCredential(credential({ managed: true, plan: plan({ is_enabled: false, recovery_at_ms: 1000, recovery_status: 'scheduled' }) }));
  assert.equal(paused.state, 'paused');
  assert.equal(automationRowReasonKey(paused), 'automation.reason_paused_recovery');
  assert.equal(matchesAutomationFacet(paused, 'paused'), true);
});

test('gateway disabled is CPA truth and never the last automation action', () => {
  const row = projectAutomationCredential(credential({ managed: true, disabled: true, plan: plan({ disabled_by_automation: false }) }));
  assert.equal(row.isGatewayDisabled, true);
  assert.equal(matchesAutomationFacet(row, 'disabled'), true);
  const recovered = projectAutomationCredential(credential({ managed: true, disabled: false, plan: plan({ recovery_at_ms: 1000, recovery_status: 'overdue' }) }));
  assert.equal(recovered.isGatewayDisabled, false);
  assert.equal(matchesAutomationFacet(recovered, 'disabled'), true);
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
  ] as const) {
    const row = projectAutomationCredential(credential({ managed: true, plan: plan(overrides) }));
    assert.equal(row.state, state);
  }
  assert.equal(automationReasonKey('UNRECOGNIZED_CODE'), 'automation.reason_unknown');
  assert.equal(automationRowReasonKey(projectAutomationCredential(credential({
    managed: true, auto_disable_supported: false, plan: plan({ auto_disable_supported: false, monitor_reason: 'no_weekly_evidence' }),
  }))), 'automation.reason_capability_short');
});

test('identity mismatch retains its specific reason without an execution error', () => {
  const row = projectAutomationCredential(credential({ managed: true, plan: plan({ state: 'monitoring', monitor_reason: 'evidence_identity_mismatch' }) }));
  assert.equal(row.state, 'error');
  assert.equal(automationRowReasonKey(row), 'automation.reason_evidence_identity_mismatch');
});

test('a pending retry independently projects an error from monitoring', () => {
  const row = projectAutomationCredential(credential({ managed: true, plan: plan({ state: 'monitoring', next_attempt_at_ms: 2000 }) }));
  assert.equal(row.hasBackoff, true);
  assert.equal(row.state, 'error');
  assert.equal(matchesAutomationFacet(row, 'error'), true);
  assert.equal(automationRowReasonKey(row), 'automation.reason_retry_count');
});

test('an unavailable inventory reports unknown counts and never an empty gateway', () => {
  const data = response([], {
    inventory: { available: false, error: 'credential list unavailable' },
    accounts: [plan({ auth_index: 'last-known', state: 'waiting', next_attempt_at_ms: 3000 })],
    accounts_last_known: true,
  });
  const { rows, stats, available, lastKnownAccounts } = projectAutomationRoster(data);
  assert.equal(available, false);
  assert.deepEqual(rows, []);
  for (const key of ['credentials', 'managed', 'unmanaged', 'gatewayDisabled', 'paused', 'recovery', 'pendingAbsence'] as const) {
    assert.equal(stats[key], null, key);
  }
  assert.equal(lastKnownAccounts.length, 1);
  assert.equal(automationReasonKey(undefined), 'automation.reason_unknown');
});

test('facets partition the current rows and counts share the same facts', () => {
  const credentials = [
    credential({ auth_index: 'a', managed: true }),
    credential({ auth_index: 'b', managed: true, plan: plan({ is_enabled: false }) }),
    credential({ auth_index: 'c', managed: true, plan: plan({ state: 'error', next_attempt_at_ms: 2000 }) }),
    credential({ auth_index: 'd', managed: false }),
    credential({ auth_index: 'e', managed: false, disabled: true }),
    credential({ auth_index: 'f', managed: true, auto_disable_supported: false, plan: plan({ auto_disable_supported: false }) }),
  ];
  const { rows, stats } = projectAutomationRoster(response(credentials));
  const count = (facet) => rows.filter(row => matchesAutomationFacet(row, facet)).length;
  assert.deepEqual(['all', 'managed', 'unmanaged', 'paused', 'disabled', 'monitor_only', 'error'].map(count), [6, 4, 2, 1, 1, 1, 1]);
  assert.equal(stats.managed + stats.unmanaged, stats.credentials);
  assert.equal(stats.paused, 1);
});
