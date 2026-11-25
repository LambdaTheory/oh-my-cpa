// Seeding through the real management API (cases.md 0.7, "repository-key seed RS"): receiver public keys come from the
// built binary's `key gen`, applications are filed on an agent's behalf, approved, handed out, stopped and re-enabled
// exactly as an operator would through the console. Fake data only (`example/...`).
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const BASE = '/api/v1/management/repo-keys';

/** A receiver public key for `repo`, made by the binary's own `key gen` in an isolated, user-only home. */
export function genPublicKey(binary, homeDir, repo) {
  fs.mkdirSync(homeDir, { recursive: true });
  // `key gen` refuses a home that still inherits permissions from its parent (Windows ACL).
  spawnSync('icacls', [homeDir, '/inheritance:r', '/grant:r', `${process.env.USERNAME}:(OI)(CI)F`], { stdio: 'ignore', windowsHide: true });
  const result = spawnSync(binary, ['key', 'gen', '--repo', repo], { env: { ...process.env, OMC_HOME: homeDir }, encoding: 'utf8', windowsHide: true });
  if (result.status !== 0) throw new Error(`key gen failed for ${repo}: ${result.stderr || result.stdout}`);
  const line = result.stdout.split(/\r?\n/).find((entry) => entry.startsWith('x25519:'));
  if (!line) throw new Error(`key gen printed no public key for ${repo}: ${result.stdout}`);
  return line.trim();
}

/** Files an application on an agent's behalf; returns the answer with the one-time link. */
export async function fileApplication(api, { repo, agent = 'agent-a', purpose = 'acceptance seed', publicKey }) {
  const answer = await api.post(`${BASE}/applications`, { repo, purpose, agent, public_key: publicKey });
  if (answer.status !== 200) throw new Error(`apply ${repo} -> ${answer.status} ${JSON.stringify(answer.body)}`);
  return answer.body;
}

export async function approve(api, id, reason = 'acceptance seed') {
  const answer = await api.post(`${BASE}/applications/${encodeURIComponent(id)}/approve`, { reason });
  if (answer.status !== 200) throw new Error(`approve ${id} -> ${answer.status} ${JSON.stringify(answer.body)}`);
  return answer.body;
}

export async function takeCiphertext(api, id) {
  const answer = await api.post(`${BASE}/applications/${encodeURIComponent(id)}/ciphertext`, {});
  if (answer.status !== 200) throw new Error(`ciphertext ${id} -> ${answer.status} ${JSON.stringify(answer.body)}`);
  return answer.body;
}

export async function listKeys(api) {
  const answer = await api.get(BASE);
  if (answer.status !== 200) throw new Error(`list keys -> ${answer.status}`);
  return answer.body;
}

export async function keyIdForRepo(api, repo) {
  const found = (await listKeys(api)).keys.find((key) => key.repo === repo);
  if (!found) throw new Error(`no key for ${repo}`);
  return found.id;
}

/**
 * Target states (cases.md 0.7): pending 2 (one of them a re-enable), awaiting delivery 1, active 2,
 * stopped 3 (rotate, misuse, leaked). Returns what a later case needs: ids and the one-time link token of a pending request.
 */
export async function seedRepoKeys(api, { binary, home }) {
  const steps = [];
  const record = (name, value) => { steps.push({ name, value }); return value; };
  const result = { steps };

  const issue = async (repo) => {
    const publicKey = genPublicKey(binary, home, repo);
    const filed = record(`apply ${repo}`, await fileApplication(api, { repo, publicKey }));
    // A receiver already on the list is approved on filing; otherwise a person approves it.
    if (filed.status !== 'approved') record(`approve ${repo}`, await approve(api, filed.application_id));
    return filed;
  };

  const alpha = await issue('example/alpha');
  record('ciphertext example/alpha', await takeCiphertext(api, alpha.application_id));
  const beta = await issue('example/beta');
  result.awaitingApplicationId = beta.application_id; // stays awaiting delivery
  const rotated = await issue('example/rotated');
  record('ciphertext example/rotated', await takeCiphertext(api, rotated.application_id));
  const misused = await issue('example/misused');
  record('ciphertext example/misused', await takeCiphertext(api, misused.application_id));
  const leaked = await issue('example/leaked');
  record('ciphertext example/leaked', await takeCiphertext(api, leaked.application_id));

  const stop = async (repo, category) => {
    const id = await keyIdForRepo(api, repo);
    const answer = await api.post(`${BASE}/${encodeURIComponent(id)}/stop`, { category, reason: 'acceptance seed' });
    if (answer.status !== 200) throw new Error(`stop ${repo} -> ${answer.status} ${JSON.stringify(answer.body)}`);
    return id;
  };
  result.rotatedKeyId = await stop('example/rotated', 'rotate');
  result.misusedKeyId = await stop('example/misused', 'misuse');
  result.leakedKeyId = await stop('example/leaked', 'leaked');

  const reenable = await api.post(`${BASE}/${encodeURIComponent(result.rotatedKeyId)}/reenable-request`, { reason: 'acceptance seed' });
  if (reenable.status !== 200) throw new Error(`reenable request -> ${reenable.status} ${JSON.stringify(reenable.body)}`);
  record('reenable request', reenable.body);

  // One more plain pending request, left undecided; its one-time link token is what the approval-link cases open.
  const pendingPublicKey = genPublicKey(binary, home, 'example/pending-one');
  const pending = await fileApplication(api, { repo: 'example/pending-one', publicKey: pendingPublicKey });
  record('apply example/pending-one', pending);
  result.pendingToken = pending.approval_token;
  result.pendingApplicationId = pending.application_id;
  result.pendingUrl = pending.approval_url;
  return result;
}

/** Counts the console shows in the three block titles. */
export async function stateCounts(api) {
  const list = await listKeys(api);
  const pending = await api.get(`${BASE}/applications?status=pending`);
  const awaiting = await api.get(`${BASE}/applications?awaiting=true`);
  return {
    keys: list.keys.length,
    active: list.keys.filter((key) => key.status === 'active').length,
    stopped: list.keys.filter((key) => key.status === 'stopped').length,
    stopCategories: list.keys.filter((key) => key.status === 'stopped').map((key) => key.stop_category).sort(),
    pendingApplications: list.pending_applications,
    awaitingDelivery: list.awaiting_delivery,
    pendingListed: pending.body?.applications?.length,
    awaitingListed: awaiting.body?.applications?.length,
    reenablePending: (pending.body?.applications ?? []).filter((entry) => entry.request_kind === 'reenable').length,
  };
}

export async function firstModelGroupId(api, name = '力工组') {
  const answer = await api.get('/api/v1/management/model-groups');
  const groups = answer.body?.groups ?? [];
  const found = groups.find((group) => group.name === name) ?? groups[0];
  if (!found) throw new Error('no model group');
  return found.id;
}

export const workDir = (...parts) => path.join('C:/Users/lhw/AppData/Local/Temp/omc-t9-s8-work', ...parts);
