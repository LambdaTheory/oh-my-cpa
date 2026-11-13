import assert from 'node:assert/strict';
import { once } from 'node:events';
import test from 'node:test';
import { parse as parseYaml } from 'yaml';
import { createFakeCpaServer, FAKE_CLIENT_SECRET, FAKE_CPA_MANAGEMENT_KEY, FAKE_PROVIDER_SECRET, FAKE_ACCOUNT_SECRET } from './fake-cpa.mjs';

async function startFixture(context, options = {}) {
  const { server } = createFakeCpaServer(options);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  context.after(() => new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve())));
  const baseURL = `http://127.0.0.1:${server.address().port}`;
  return (path, key, options = {}) => fetch(`${baseURL}${path}`, {
    ...options,
    headers: { ...(key ? { Authorization: `Bearer ${key}` } : {}), ...options.headers },
  });
}

// The same fixture with the live controls a browser scenario needs: the host
// generation and the plugin's answer are switched while the server keeps
// listening. Keeping it separate leaves every existing test on the plain helper.
async function startControlledFixture(context, options = {}) {
  const { server, requests, fixture } = createFakeCpaServer(options);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  context.after(() => new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve())));
  const baseURL = `http://127.0.0.1:${server.address().port}`;
  const request = (path, key, options = {}) => fetch(`${baseURL}${path}`, {
    ...options,
    headers: { ...(key ? { Authorization: `Bearer ${key}` } : {}), ...options.headers },
  });
  return { request, requests, fixture };
}

test('gateway directory authenticates client keys independently of management reads', async (context) => {
  const request = await startFixture(context);
  for (const key of [undefined, 'invalid', FAKE_CPA_MANAGEMENT_KEY]) {
    assert.equal((await request('/v1/models', key)).status, 401);
  }
  assert.equal((await request('/v8/management/config', FAKE_CLIENT_SECRET)).status, 401);
  assert.equal((await request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY)).status, 200);
  const reply = await request('/v1/models', FAKE_CLIENT_SECRET);
  assert.equal(reply.status, 200);
  const body = await reply.json();
  assert.equal(body.object, 'list');
  const identities = body.data.map((model) => model.id);
  assert.ok(identities.includes('gpt-e2e'));
  assert.ok(identities.includes('gpt-e2e-preview'));
  assert.ok(identities.includes('sonnet-latest'));
  assert.ok(!identities.includes('claude-3-5-sonnet'));
  assert.equal(new Set(identities).size, identities.length);
  for (const model of body.data) assert.deepEqual(Object.keys(model).sort(), ['id', 'object', 'owned_by']);
  for (const secret of [FAKE_CLIENT_SECRET, FAKE_CPA_MANAGEMENT_KEY, FAKE_PROVIDER_SECRET, FAKE_ACCOUNT_SECRET]) {
    assert.ok(!JSON.stringify(body).includes(secret));
  }
  assert.equal((await request('/v1/models', FAKE_CLIENT_SECRET, { method: 'POST' })).status, 405);
  assert.equal((await request('/v1/models/extra', FAKE_CLIENT_SECRET)).status, 401);
});

test('gateway directory follows client-key rotation and revocation', async (context) => {
  const request = await startFixture(context);
  const replacementKey = 'fixture-replacement-client';
  assert.equal((await request('/v8/management/config/access/api-keys', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'PUT', body: JSON.stringify([replacementKey]),
  })).status, 200);
  assert.equal((await request('/v1/models', FAKE_CLIENT_SECRET)).status, 401);
  assert.equal((await request('/v1/models', replacementKey)).status, 200);
  assert.equal((await request('/v8/management/config/access/api-keys', FAKE_CPA_MANAGEMENT_KEY, { method: 'DELETE' })).status, 200);
  assert.equal((await request('/v1/models', replacementKey)).status, 401);
});

test('gateway directory reads current configured model aliases and prefixes', async (context) => {
  const request = await startFixture(context);
  assert.equal((await request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'PATCH', body: JSON.stringify({ 'api-keys': { codex: [{ name: 'fixture-route', prefix: 'team', models: [{ name: 'gpt-e2e-second', alias: 'client-name' }], keys: [{ 'api-key': FAKE_PROVIDER_SECRET }] }] } }),
  })).status, 200);
  const body = await (await request('/v1/models', FAKE_CLIENT_SECRET)).json();
  const identities = body.data.map((model) => model.id);
  assert.ok(identities.includes('team/client-name'));
  assert.ok(!identities.includes('gpt-e2e-second'));
  assert.ok(identities.includes('gpt-e2e'));
});

test('gateway directory leaves out models a provider or credential excludes', async (context) => {
  const request = await startFixture(context);
  const route = (excluded) => request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'PATCH',
    body: JSON.stringify({ 'api-keys': { codex: [{ name: 'fixture-route', 'excluded-models': excluded, models: [{ name: 'Route-Kept' }, { name: 'route-hidden', alias: 'hidden-alias' }], keys: [{ 'api-key': FAKE_PROVIDER_SECRET }] }] } }),
  });
  const identities = async () => (await (await request('/v1/models', FAKE_CLIENT_SECRET)).json()).data.map((model) => model.id);

  assert.equal((await route(['ROUTE-hid*'])).status, 200);
  assert.ok((await identities()).includes('Route-Kept'));
  assert.ok(!(await identities()).includes('hidden-alias'));

  assert.equal((await route(['*'])).status, 200);
  assert.ok(!(await identities()).includes('Route-Kept'));
  assert.ok(!(await identities()).includes('hidden-alias'));

  assert.ok((await identities()).includes('sonnet-latest'));
  assert.equal((await request('/v8/management/credentials/fields', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'PATCH', body: JSON.stringify({ name: 'claude-fixture.json', excluded_models: ['claude-*'] }),
  })).status, 200);
  assert.ok(!(await identities()).includes('sonnet-latest'));
});


test('OAuth exclusion snapshots follow provider updates and deletions', async (context) => {
  const request = await startFixture(context);
  const readJson = async (path) => {
    const response = await request(path, FAKE_CPA_MANAGEMENT_KEY);
    assert.equal(response.status, 200);
    return response.json();
  };
  const assertSnapshots = async (expected) => {
    assert.deepEqual(await readJson('/v8/management/config/oauth/excluded-models'), expected);
    const config = await readJson('/v8/management/config');
    assert.deepEqual(config.oauth['excluded-models'], expected);
    assert.equal(config.server.port, 8317);
    assert.equal(config.oauth['custom-setting'], 'preserved');
    for (const path of ['/v8/management/config.yaml', '/v0/management/config.yaml']) {
      const response = await request(path, FAKE_CPA_MANAGEMENT_KEY);
      assert.equal(response.status, 200);
      const snapshot = parseYaml(await response.text());
      assert.deepEqual(snapshot.oauth['excluded-models'], expected);
      assert.equal(snapshot.server.port, 8317);
      assert.equal(snapshot.oauth['custom-setting'], 'preserved');
    }
  };
  const patch = async (oauth) => {
    assert.equal((await request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY, {
      method: 'PATCH', body: JSON.stringify({ oauth }),
    })).status, 200);
  };
  assert.deepEqual((await readJson('/v8/management/config')).oauth['excluded-models'], { codex: ['GPT-E2E-Legacy', 'gpt-e2e-old-*'] });
  await patch({ 'custom-setting': 'preserved' });
  await assertSnapshots({ codex: ['GPT-E2E-Legacy', 'gpt-e2e-old-*'] });
  await patch({ 'excluded-models': { codex: ['GPT-*'], claude: ['claude-*'] } });
  await assertSnapshots({ codex: ['GPT-*'], claude: ['claude-*'] });
  await patch({ 'excluded-models': { codex: ['replacement'] } });
  await assertSnapshots({ codex: ['replacement'], claude: ['claude-*'] });
  for (const provider of ['codex', 'claude']) {
    assert.equal((await request(`/v8/management/config/oauth/excluded-models/${provider}`, FAKE_CPA_MANAGEMENT_KEY, { method: 'DELETE' })).status, 200);
    await assertSnapshots(provider === 'codex' ? { claude: ['claude-*'] } : {});
  }
});

test('gateway applies global OAuth exclusions before alias mapping without hiding API-key routes', async (context) => {
  const request = await startFixture(context);
  const patch = async (oauth) => {
    assert.equal((await request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY, {
      method: 'PATCH', body: JSON.stringify({ oauth }),
    })).status, 200);
  };
  const identities = async () => {
    const response = await request('/v1/models', FAKE_CLIENT_SECRET);
    assert.equal(response.status, 200);
    return (await response.json()).data.map((model) => model.id);
  };
  const readCatalog = async () => {
    const response = await request('/v8/management/routing/model-definitions/claude', FAKE_CPA_MANAGEMENT_KEY);
    assert.equal(response.status, 200);
    return response.json();
  };
  const catalog = await readCatalog();
  await patch({ 'model-alias': { claude: [{ name: 'claude-3-5-sonnet', alias: 'sonnet-latest', fork: true }] } });
  for (const rule of ['CLAUDE-3-5-SONNET', 'ClAuDe-*', '*']) {
    await patch({ 'excluded-models': { claude: [rule] } });
    const models = await identities();
    assert.ok(!models.includes('claude-3-5-sonnet'));
    assert.ok(!models.includes('sonnet-latest'));
    assert.ok(models.includes('gpt-e2e-preview'));
    assert.deepEqual(await readCatalog(), catalog);
  }
  await patch({ 'excluded-models': { claude: ['sonnet-latest'] } });
  assert.ok((await identities()).includes('claude-3-5-sonnet'));
  assert.ok((await identities()).includes('sonnet-latest'));
  await patch({ 'excluded-models': { claude: [], codex: ['GPT-E2E'] } });
  const sharedModels = await identities();
  assert.ok(sharedModels.includes('gpt-e2e'));
  assert.ok(!sharedModels.includes('gpt-e2e-preview'));
  assert.ok(sharedModels.includes('sonnet-latest'));
  await patch({ 'excluded-models': { codex: [] } });
  assert.ok((await identities()).includes('gpt-e2e-preview'));
  assert.equal((await request('/v8/management/credentials/fields', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'PATCH', body: JSON.stringify({ name: 'claude-fixture.json', excluded_models: ['claude-*'] }),
  })).status, 200);
  assert.ok(!(await identities()).includes('claude-3-5-sonnet'));
  assert.ok(!(await identities()).includes('sonnet-latest'));
});

test('Codex behavior writes use canonical paths and root aliases survive only as canonical readback', async (context) => {
  const request = await startFixture(context);
  const write = (body) => request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  });
  const assertReadback = async (value) => {
    for (const path of ['/v8/management/config', '/v8/management/config.yaml', '/v0/management/config.yaml']) {
      const response = await request(path, FAKE_CPA_MANAGEMENT_KEY);
      assert.equal(response.status, 200);
      const document = path.endsWith('.yaml') ? parseYaml(await response.text()) : await response.json();
      assert.equal(document.client.codex['optimize-multi-agent-v2'], value);
      assert.equal(document.upstream.codex['orphan-delegation-compatibility'], value);
      assert.equal(document.oauth?.providers?.codex?.['optimize-multi-agent-v2'], undefined);
      assert.equal(document.oauth?.providers?.codex?.['orphan-delegation-compatibility'], undefined);
    }
  };
  await assertReadback(true);
  assert.equal((await write({ client: { codex: { 'optimize-multi-agent-v2': false } }, upstream: { codex: { 'orphan-delegation-compatibility': false } } })).status, 200);
  await assertReadback(false);
  assert.equal((await write({ oauth: { providers: { codex: { 'optimize-multi-agent-v2': true, 'orphan-delegation-compatibility': true } } } })).status, 200);
  await assertReadback(true);
  assert.equal((await write({
    client: { codex: { 'optimize-multi-agent-v2': false } },
    upstream: { codex: { 'orphan-delegation-compatibility': false } },
    oauth: { providers: { codex: { 'optimize-multi-agent-v2': true, 'orphan-delegation-compatibility': true } } },
  })).status, 200);
  await assertReadback(false);
});


test('a null historical Codex container resets canonical leaves without masking explicit canonical values', async (context) => {
  const request = await startFixture(context);
  const write = (body) => request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  });
  assert.equal((await write({ oauth: { providers: { codex: null } } })).status, 200);
  let document = await (await request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY)).json();
  assert.equal(document.client.codex['optimize-multi-agent-v2'], null);
  assert.equal(document.upstream.codex['orphan-delegation-compatibility'], null);
  assert.equal(document.oauth?.providers?.codex, undefined);

  assert.equal((await write({
    client: { codex: { 'optimize-multi-agent-v2': false } },
    upstream: { codex: { 'orphan-delegation-compatibility': true } },
    oauth: { providers: { codex: null } },
  })).status, 200);
  document = await (await request('/v8/management/config', FAKE_CPA_MANAGEMENT_KEY)).json();
  assert.equal(document.client.codex['optimize-multi-agent-v2'], false);
  assert.equal(document.upstream.codex['orphan-delegation-compatibility'], true);
  assert.equal(document.oauth?.providers?.codex, undefined);
});

test('native plugin startup reads effective client keys and complete provider groups', async (context) => {
  const request = await startFixture(context);
  for (const route of ['/v0/management/config', '/v8/management/config/api-keys']) {
    assert.equal((await request(route, 'wrong-page-key')).status, 401);
  }
  const effective = await (await request('/v0/management/config', FAKE_CPA_MANAGEMENT_KEY)).json();
  assert.deepEqual(effective['api-keys'], [FAKE_CLIENT_SECRET]);
  assert.ok(effective['codex-api-key'].some(key => key['api-key'] === FAKE_PROVIDER_SECRET));
  const groupsReply = await request('/v8/management/config/api-keys', FAKE_CPA_MANAGEMENT_KEY);
  assert.equal(groupsReply.status, 200);
  const groups = await groupsReply.json();
  const codexGroups = await (await request('/v8/management/config/api-keys/codex', FAKE_CPA_MANAGEMENT_KEY)).json();
  assert.deepEqual(groups.codex, codexGroups);
  assert.ok(groups.codex.some(group => group.keys.some(key => key['api-key'] === FAKE_PROVIDER_SECRET)));
  const modelReply = await request('/v1/models', effective['api-keys'][0]);
  assert.equal(modelReply.status, 200);
  assert.ok((await modelReply.json()).data.length > 0);
  const sync = await request('/v0/management/plugins/fixture-logger/credentials/sync', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'POST', body: JSON.stringify({ credentials: Object.keys(groups), model_count: 1 }),
  });
  assert.equal(sync.status, 200);
  assert.equal((await request('/v0/management/plugins/fixture-logger/state', FAKE_CPA_MANAGEMENT_KEY)).status, 200);
});

const QUOTA_PROVIDER_KEYS = ['ollama', 'openai-compatible-command code', 'openai-compatible-opencode'];

test('quota plugin advertises the raw provider keys and claims their credentials', async (context) => {
  const request = await startFixture(context);
  const reply = await request('/v8/management/credentials', FAKE_CPA_MANAGEMENT_KEY);
  assert.equal(reply.status, 200);
  const files = new Map((await reply.json()).files.map((file) => [file.auth_index, file]));
  const expected = [
    ['auth-index-e2e-ollama', 'ollama-fixture.json', 'ollama', undefined],
    ['auth-index-e2e-command-code', 'command-code-fixture.json', 'openai-compatible-command code', 'https://commandcode.example.test/v1'],
    ['auth-index-e2e-opencode', 'opencode-go-fixture.json', 'openai-compatible-opencode', 'https://opencode.example.test/v1'],
  ];
  for (const [authIndex, name, type, baseURL] of expected) {
    const file = files.get(authIndex);
    assert.ok(file, `credential ${authIndex} is listed`);
    assert.equal(file.name, name);
    assert.equal(file.type, type);
    assert.equal(file.provider, type);
    assert.equal(file.base_url, baseURL);
    assert.equal(file.supports_quota, true);
    assert.equal(file.quota_provider, type);
    assert.equal(file.quota_plugin_id, 'ollama-quota');
  }
  const pluginsReply = await request('/v8/management/plugins', FAKE_CPA_MANAGEMENT_KEY);
  assert.equal(pluginsReply.status, 200);
  const quotaPlugin = (await pluginsReply.json()).plugins.find((plugin) => plugin.id === 'ollama-quota');
  assert.ok(quotaPlugin, 'ollama-quota is installed');
  assert.equal(quotaPlugin.registered, true);
  assert.equal(quotaPlugin.effective_enabled, true);
  assert.equal(quotaPlugin.supports_quota, true);
  assert.equal(quotaPlugin.quota_provider, 'ollama');
  assert.deepEqual(quotaPlugin.supported_providers, QUOTA_PROVIDER_KEYS);
});

test('plugin quota route serves each provider shape and refuses what it cannot answer', async (context) => {
  const request = await startFixture(context);
  const quota = (authIndex, key = FAKE_CPA_MANAGEMENT_KEY) => request('/v8/management/plugins/ollama-quota/quota', key, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ auth_index: authIndex }),
  });

  assert.equal((await quota('auth-index-e2e-ollama', null)).status, 401);
  assert.equal((await quota('auth-index-e2e-ollama', 'wrong-management-key')).status, 401);

  const ollama = await quota('auth-index-e2e-ollama');
  assert.equal(ollama.status, 200);
  const ollamaBody = await ollama.json();
  assert.deepEqual(ollamaBody.subscription, { plan: '' });
  assert.deepEqual(ollamaBody.summary, [{ key: 'balance', label: 'Balance', value: 12.34, format: 'currency', currency: 'USD' }]);
  assert.equal(ollamaBody.groups.length, 1);
  assert.equal(ollamaBody.groups[0].displayName, 'Ollama');
  assert.deepEqual(ollamaBody.groups[0].buckets.map((bucket) => bucket.window), ['5h', '7d']);
  assert.deepEqual(ollamaBody.groups[0].buckets.map((bucket) => bucket.remainingFraction), [0.6, 0.8]);
  for (const bucket of ollamaBody.groups[0].buckets) {
    assert.ok(!Number.isNaN(Date.parse(bucket.resetTime)), `resetTime ${bucket.resetTime} is RFC3339`);
    assert.match(bucket.description, /read at/);
  }

  const commandCode = await (await quota('auth-index-e2e-command-code')).json();
  assert.deepEqual(commandCode.subscription, { plan: 'Pro' });
  assert.deepEqual(commandCode.summary, [{ key: 'balance', label: 'Balance', value: 321, format: 'number', unit: 'credits' }]);
  assert.equal(commandCode.groups[0].displayName, 'Command Code');
  assert.deepEqual(commandCode.groups[0].buckets.map((bucket) => bucket.window), ['5h', '7d']);
  assert.deepEqual(commandCode.groups[0].buckets.map((bucket) => bucket.remainingFraction), [0.8, 0.55]);

  const openCodeReply = await quota('auth-index-e2e-opencode');
  assert.equal(openCodeReply.status, 200);
  const openCode = await openCodeReply.json();
  assert.equal(Object.hasOwn(openCode, 'summary'), false, 'OpenCode Go sends no balance');
  assert.equal(Object.hasOwn(openCode, 'subscription'), false, 'OpenCode Go sends no plan');
  assert.equal(openCode.groups[0].displayName, 'OpenCode Go');
  assert.deepEqual(openCode.groups[0].buckets.map((bucket) => bucket.window), ['rolling', 'weekly', 'monthly']);
  assert.deepEqual(openCode.groups[0].buckets.map((bucket) => bucket.remainingFraction), [0, 0.42, 0.21]);

  assert.equal((await quota('auth-index-e2e-unknown')).status, 404);
  assert.equal((await quota('auth-index-e2e-1')).status, 400);
  assert.equal((await request('/v8/management/plugins/unknown-quota/quota', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'POST', body: JSON.stringify({ auth_index: 'auth-index-e2e-ollama' }),
  })).status, 404);
  assert.equal((await request('/v8/management/plugins/ollama-quota/quota', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'POST', body: JSON.stringify({}),
  })).status, 400);
});

test('quota providers fallback route lists the plugin and its raw provider keys', async (context) => {
  const request = await startFixture(context);
  assert.equal((await request('/v8/management/credentials/quota/providers')).status, 401);
  const reply = await request('/v8/management/credentials/quota/providers', FAKE_CPA_MANAGEMENT_KEY);
  assert.equal(reply.status, 200);
  assert.deepEqual(await reply.json(), {
    providers: [{
      plugin_id: 'ollama-quota',
      provider: 'ollama',
      display_name: 'Ollama',
      supported_providers: QUOTA_PROVIDER_KEYS,
      supports_reset: false,
    }],
  });
});

test('a legacy host omits quota_plugin_id while keeping the provider advertisement', async (context) => {
  const request = await startFixture(context, { legacyHost: true });
  const reply = await request('/v8/management/credentials', FAKE_CPA_MANAGEMENT_KEY);
  assert.equal(reply.status, 200);
  const files = new Map((await reply.json()).files.map((file) => [file.auth_index, file]));
  for (const [authIndex, type] of [
    ['auth-index-e2e-ollama', 'ollama'],
    ['auth-index-e2e-command-code', 'openai-compatible-command code'],
    ['auth-index-e2e-opencode', 'openai-compatible-opencode'],
  ]) {
    const file = files.get(authIndex);
    assert.ok(file);
    assert.equal(file.supports_quota, true);
    assert.equal(file.quota_provider, type);
    assert.equal(Object.hasOwn(file, 'quota_plugin_id'), false, `${authIndex} must let the client resolve the plugin`);
  }
});

test('a credentials read can override the host generation it is answered from', async (context) => {
  const request = await startFixture(context);
  const read = async (query) => {
    const reply = await request(`/v8/management/credentials${query}`, FAKE_CPA_MANAGEMENT_KEY);
    assert.equal(reply.status, 200);
    return (await reply.json()).files.find((file) => file.auth_index === 'auth-index-e2e-ollama');
  };
  assert.equal((await read('')).quota_plugin_id, 'ollama-quota');
  assert.equal(Object.hasOwn(await read('?legacy_host=1'), 'quota_plugin_id'), false);
  assert.equal((await read('?legacy_host=0')).quota_plugin_id, 'ollama-quota');
});

test('the quota plugin can answer from its stale cache', async (context) => {
  const { request, requests, fixture } = await startControlledFixture(context);
  fixture.setQuotaMode('stale');
  const reply = await request('/v8/management/plugins/ollama-quota/quota', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ auth_index: 'auth-index-e2e-command-code' }),
  });
  assert.equal(reply.status, 200);
  const body = await reply.json();
  assert.deepEqual(body.subscription, { plan: 'Pro' });
  assert.deepEqual(body.summary, [{ key: 'balance', label: 'Balance · stale', value: 321, format: 'number', unit: 'credits' }]);
  const [fiveHour, weekly] = body.groups[0].buckets;
  assert.match(fiveHour.description, /^5-hour session · stale · last read at \d{4}-\d{2}-\d{2}T[^ ]+ · upstream timeout$/);
  assert.match(weekly.description, /^Weekly · stale · last read at \d{4}-\d{2}-\d{2}T[^ ]+ · upstream timeout$/);
  // A cached reading keeps its real figures; only the freshness is old.
  assert.equal(fiveHour.remainingFraction, 0.8);
  assert.equal(weekly.remainingFraction, 0.55);
  const logged = requests.filter((entry) => entry.method === 'POST' && entry.path === '/v8/management/plugins/ollama-quota/quota');
  assert.equal(logged.length, 1);
  assert.deepEqual(JSON.parse(logged[0].body), { auth_index: 'auth-index-e2e-command-code' });
});

test('the quota plugin can report an upstream rate limit and failure', async (context) => {
  const { request, fixture } = await startControlledFixture(context);
  const quota = () => request('/v8/management/plugins/ollama-quota/quota', FAKE_CPA_MANAGEMENT_KEY, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ auth_index: 'auth-index-e2e-command-code' }),
  });
  fixture.setQuotaMode('rate-limit');
  const limited = await quota();
  assert.equal(limited.status, 429);
  assert.equal(limited.headers.get('retry-after'), '120');
  fixture.setQuotaMode('upstream-error');
  const failed = await quota();
  assert.equal(failed.status, 502);
  fixture.setQuotaMode('ok');
  assert.equal((await quota()).status, 200);
});

test('the host generation can be switched on a running server', async (context) => {
  const { request, fixture } = await startControlledFixture(context);
  const read = async () => {
    const reply = await request('/v8/management/credentials', FAKE_CPA_MANAGEMENT_KEY);
    assert.equal(reply.status, 200);
    return (await reply.json()).files.find((file) => file.auth_index === 'auth-index-e2e-command-code');
  };
  assert.equal((await read()).quota_plugin_id, 'ollama-quota');
  fixture.setLegacyHost(true);
  const legacyFile = await read();
  assert.equal(legacyFile.supports_quota, true);
  assert.equal(Object.hasOwn(legacyFile, 'quota_plugin_id'), false);
  fixture.setLegacyHost(false);
  assert.equal((await read()).quota_plugin_id, 'ollama-quota');
});

test('the legacy host default can come from FAKE_CPA_LEGACY_HOST', async (context) => {
  const previous = process.env.FAKE_CPA_LEGACY_HOST;
  process.env.FAKE_CPA_LEGACY_HOST = '1';
  try {
    const request = await startFixture(context);
    const reply = await request('/v8/management/credentials', FAKE_CPA_MANAGEMENT_KEY);
    assert.equal(reply.status, 200);
    const file = (await reply.json()).files.find((candidate) => candidate.auth_index === 'auth-index-e2e-ollama');
    assert.ok(file);
    assert.equal(Object.hasOwn(file, 'quota_plugin_id'), false);
  } finally {
    if (previous === undefined) delete process.env.FAKE_CPA_LEGACY_HOST;
    else process.env.FAKE_CPA_LEGACY_HOST = previous;
  }
});
