// @vitest-environment node
import { afterEach, expect, it, vi } from 'vitest';
import { api } from '../src/api/client';

afterEach(() => vi.unstubAllGlobals());

it('emits the backend lifecycle methods, encoded paths and independent patch fields through the actual typed client', async () => {
  const calls: { path: string; options: RequestInit }[] = [];
  vi.stubGlobal('window', { __OMCPA_CONFIG__: { basePath: '/omc' } });
  vi.stubGlobal('fetch', async (path: string, options: RequestInit) => {
    calls.push({ path, options });
    return new Response(JSON.stringify(path.endsWith('/account-automation/unadopt') ? { removed: 2 } : { auth_index: 'synthetic/a', removed: path.endsWith('/unadopt'), burn_credit: true }), { headers: { 'Content-Type': 'application/json' } });
  });
  await api.putAccountAutomationAccount('synthetic/a', { burn_credit: true });
  await api.putAccountAutomationAccount('synthetic/a', { enabled: false });
  await api.putAccountAutomationAccount('synthetic/a', { enabled: true, burn_credit: false });
  // A caller that read a lifecycle names its version so a delayed request cannot act on a
  // replacement adopted under the same index.
  await api.putAccountAutomationAccount('synthetic/a', { enabled: false, expected_intent_version: 4 });
  // Every own-frontend mutation names the lifecycle it read, so a delayed request cannot act
  // on a replacement adopted under the same index. The wire carries the version on the body.
  await api.postAccountAutomationCancel('synthetic/a', 4);
  await api.postAccountAutomationManualReset('synthetic/a', { reset_at_ms: 4102444800000, expected_intent_version: 4 });
  expect((await api.postAccountAutomationUnadopt('synthetic/a', 4)).removed).toBe(true);
  expect(await api.postAccountAutomationUnadoptMany({ auth_indexes: ['synthetic/a', 'synthetic/b'], targets: [
    { auth_index: 'synthetic/a', expected_intent_version: 4 },
    { auth_index: 'synthetic/b', expected_intent_version: 7 },
  ] })).toEqual({ removed: 2 });
  const prefix = '/omc/api/v1/account-automation';
  expect(calls.map(call => [call.options.method, call.path, call.options.body ? JSON.parse(call.options.body as string) : null])).toEqual([
    ['PUT', prefix + '/accounts/synthetic%2Fa', { burn_credit: true }],
    ['PUT', prefix + '/accounts/synthetic%2Fa', { enabled: false }],
    ['PUT', prefix + '/accounts/synthetic%2Fa', { enabled: true, burn_credit: false }],
    ['PUT', prefix + '/accounts/synthetic%2Fa', { enabled: false, expected_intent_version: 4 }],
    ['POST', prefix + '/accounts/synthetic%2Fa/cancel', { expected_intent_version: 4 }],
    ['POST', prefix + '/accounts/synthetic%2Fa/manual-reset', { reset_at_ms: 4102444800000, expected_intent_version: 4 }],
    ['POST', prefix + '/accounts/synthetic%2Fa/unadopt', { expected_intent_version: 4 }],
    ['POST', prefix + '/unadopt', { auth_indexes: ['synthetic/a', 'synthetic/b'], targets: [
      { auth_index: 'synthetic/a', expected_intent_version: 4 },
      { auth_index: 'synthetic/b', expected_intent_version: 7 },
    ] }],
  ]);
  expect(calls.every(call => call.options.credentials === 'same-origin')).toBe(true);
});
