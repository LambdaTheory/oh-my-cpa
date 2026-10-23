import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

export const ORIGIN = 'https://github.com/LambdaTheory/oh-my-cpa.git';
export const DEPLOY_REF = 'refs/heads/deploy/prebuilt';
export const MANIFEST = 'deploy/prebuilt-manifest.json';
const DIST = 'internal/web/dist/';
const SHA = /^[a-f0-9]{40}$/;
const ROOT_FILES = new Set(['package.json', 'pnpm-lock.yaml', 'pnpm-workspace.yaml', 'go.mod', 'go.sum',
  'Dockerfile', '.dockerignore', 'Dockerfile.prebuilt', 'Dockerfile.prebuilt.dockerignore', '.gitignore', 'LICENSE']);
const DEPLOY_FILES = new Set(['deploy/base-path.sh', 'deploy/healthcheck.sh',
  'deploy/dokploy/compose.yml', 'deploy/dokploy/compose.prebuilt.yml']);
export const hash = bytes => createHash('sha256').update(bytes).digest('hex');
export const digest = files => hash(Object.keys(files).sort().map(name => `${name}\0${files[name]}\n`).join(''));
const run = (cwd, command, args, options = {}) => execFileSync(command, args, { cwd, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, ...options });
const git = (cwd, args, options) => run(cwd, 'git', args, options).trim();

export function isSource(name) {
  if (name.startsWith(DIST)) return false;
  const isAllowed = ROOT_FILES.has(name) || DEPLOY_FILES.has(name) || /^(cmd|internal|migrations|scripts|web)\//.test(name);
  if (!isAllowed) return false;
  if (/(^|\/)(node_modules|dist|\.orchestration|\.git|\.env[^/]*|credentials?)(\/|$)/i.test(name)
      || /\.(db(?:-wal|-shm)?|sqlite3?|pem|key|exe|tsbuildinfo)$/i.test(name)
      || name.startsWith('web/public/lobe-icons/')) throw new Error(`Forbidden source payload path: ${name}`);
  return true;
}

function sourceEntries(root, revision) {
  return git(root, ['ls-tree', '-rz', '--full-tree', revision]).split('\0').filter(Boolean).flatMap(record => {
    const [metadata, name] = record.split('\t');
    if (!isSource(name)) return [];
    const [mode, kind, object] = metadata.split(' ');
    if (kind !== 'blob' || !['100644', '100755'].includes(mode)) throw new Error(`Nonregular source: ${name}`);
    return [{ name, mode, object }];
  });
}

function sourceHashes(root, entries) {
  const bytes = run(root, 'git', ['cat-file', '--batch'], { encoding: null, input: entries.map(entry => entry.object).join('\n') + '\n' });
  let offset = 0;
  return Object.fromEntries(entries.map(({ name }) => {
    const end = bytes.indexOf(10, offset);
    const size = Number(bytes.subarray(offset, end).toString().split(' ')[2]);
    if (!Number.isSafeInteger(size)) throw new Error('Invalid Git blob response');
    offset = end + 1;
    const result = [name, hash(bytes.subarray(offset, offset + size))];
    offset += size + 1;
    return result;
  }));
}

function walk(root, prefix = '') {
  return fs.readdirSync(root, { withFileTypes: true }).flatMap(entry => {
    const name = prefix + entry.name;
    if (entry.isSymbolicLink() || (!entry.isFile() && !entry.isDirectory())) throw new Error(`Nonregular asset: ${name}`);
    return entry.isDirectory() ? walk(path.join(root, entry.name), `${name}/`) : [name];
  }).sort();
}

export function assertOrigin(root) {
  for (const args of [['remote', 'get-url', '--all', 'origin'], ['remote', 'get-url', '--push', '--all', 'origin']]) {
    const urls = git(root, args).split('\n');
    if (urls.length !== 1 || ![ORIGIN, 'git@github.com:LambdaTheory/oh-my-cpa.git'].includes(urls[0])) {
      throw new Error(`origin must name exactly LambdaTheory/oh-my-cpa (see docs/install.md)`);
    }
  }
}

function remoteTip(root) {
  const lines = git(root, ['ls-remote', '--refs', 'origin', DEPLOY_REF]);
  if (!lines) return '';
  const [sha, ref] = lines.split(/\s+/);
  if (!SHA.test(sha) || ref !== DEPLOY_REF) throw new Error('Invalid deployment ref response');
  return sha;
}

const GIT_TRANSPORT = {
  tip: remoteTip,
  fetch(root) { git(root, ['fetch', '--no-tags', 'origin', DEPLOY_REF]); return git(root, ['rev-parse', 'FETCH_HEAD']); },
  push(root, commit, previous) { pushExpectedTip(root, 'origin', commit, previous); },
};

export function pushExpectedTip(root, remote, commit, previous) {
  const hooks = fs.mkdtempSync(path.join(os.tmpdir(), 'omc-prebuilt-hooks-'));
  try {
    // The hook inspects Git's advertised tip; the receive transaction then compares
    // that same tip atomically. This closes even a race to an ancestor of the source.
    fs.writeFileSync(path.join(hooks, 'pre-push'), '#!/bin/sh\nwhile read local_ref local_oid remote_ref remote_oid; do\n' +
      '  [ "$remote_ref" = "' + DEPLOY_REF + '" ] && [ "$remote_oid" = "' + (previous || '0'.repeat(40)) + '" ] || exit 1\ndone\n', { mode: 0o700 });
    git(root, ['-c', 'core.hooksPath=' + hooks, '-c', 'remote.origin.mirror=false', 'push', '--no-follow-tags', remote, commit + ':' + DEPLOY_REF]);
  } finally { fs.rmSync(hooks, { recursive: true, force: true }); }
}

function buildSPA(root) {
  run(root, 'pnpm', ['install', '--frozen-lockfile'], { stdio: 'inherit' });
  run(root, 'pnpm', ['build'], { stdio: 'inherit' });
}

export function verifySource(root, manifest) {
  if (!SHA.test(manifest.source_revision)) throw new Error('An immutable source SHA is required');
  const entries = sourceEntries(root, manifest.source_revision);
  const expected = sourceHashes(root, entries);
  if (digest(expected) !== digest(manifest.source_files) || JSON.stringify(Object.keys(expected).sort()) !== JSON.stringify(Object.keys(manifest.source_files).sort())) {
    throw new Error('Manifest source inventory does not match its source commit');
  }
  for (const [name, sha] of Object.entries(expected)) {
    if (!fs.lstatSync(path.join(root, name)).isFile() || hash(fs.readFileSync(path.join(root, name))) !== sha) throw new Error(`Modified source: ${name}`);
  }
  return entries;
}

function verify(root) {
  const manifest = JSON.parse(fs.readFileSync(path.join(root, MANIFEST), 'utf8'));
  verifySource(root, manifest);
  run(root, 'go', ['run', './scripts/prebuilt-verify', root], { stdio: ['ignore', 'pipe', 'inherit'] });
  return manifest;
}

export function validatePrevious(root, revision) {
  const manifest = JSON.parse(run(root, 'git', ['show', `${revision}:${MANIFEST}`]));
  if (!SHA.test(manifest.source_revision) || (manifest.previous_deploy && !SHA.test(manifest.previous_deploy))) throw new Error('Unknown deployment history');
  const parents = git(root, ['show', '-s', '--format=%P', revision]).split(' ');
  const expectedParents = [manifest.previous_deploy, manifest.source_revision].filter(Boolean);
  if (JSON.stringify(parents) !== JSON.stringify(expectedParents)) throw new Error('Unknown deployment commit parents');
  const source = sourceEntries(root, manifest.source_revision);
  const modes = Object.fromEntries(source.map(({ name, mode }) => [name, mode]));
  const hashes = sourceHashes(root, source);
  if (digest(hashes) !== digest(manifest.source_files)) throw new Error('Previous deployment source was modified');
  const expected = { ...hashes, ...Object.fromEntries(Object.entries(manifest.assets).map(([name, sha]) => [DIST + name, sha])) };
  const actual = git(root, ['ls-tree', '-rz', revision]).split('\0').filter(Boolean);
  const actualHashes = sourceHashes(root, actual.map(record => { const [metadata, name] = record.split('\t'); return { name, object: metadata.split(' ')[2] }; }));
  if (actual.length !== Object.keys(expected).length + 1) throw new Error('Unknown files in deployment history');
  for (const record of actual) {
    const [metadata, name] = record.split('\t');
    const [mode, kind, object] = metadata.split(' ');
    if (kind !== 'blob' || !['100644', '100755'].includes(mode)) throw new Error('Nonregular deployment payload');
    if (mode !== (modes[name] ?? '100644')) throw new Error(`Unknown deployment file mode: ${name}`);
    if (name === MANIFEST) continue;
    if (!expected[name] || actualHashes[name] !== expected[name]) throw new Error(`Unknown deployment modification: ${name}`);
  }
  return manifest;
}

export function prepare(root, revision, destination, tag = '', { build = buildSPA, transport = GIT_TRANSPORT } = {}) {
  if (!SHA.test(revision) || git(root, ['rev-parse', `${revision}^{commit}`]) !== revision) throw new Error('Pass a full immutable source commit SHA');
  assertOrigin(root);
  if (fs.existsSync(destination)) throw new Error('Preparation destination must not exist');
  const destinationParent = fs.realpathSync(path.dirname(destination));
  const relativeDestination = path.relative(fs.realpathSync(root), path.join(destinationParent, path.basename(destination)));
  if (!relativeDestination.startsWith('..' + path.sep) && !path.isAbsolute(relativeDestination)) throw new Error('Preparation destination must be outside the caller checkout');
  if (tag && !/^[A-Za-z0-9][A-Za-z0-9._/+@-]{0,199}(?![\s\S])/.test(tag)) throw new Error('Invalid build tag');
  const entries = sourceEntries(root, revision);
  if (entries.length === 0) throw new Error('Empty source payload');
  run(root, 'git', ['clone', '--no-local', '--no-checkout', '--', root, destination], { stdio: 'inherit' });
  git(destination, ['remote', 'set-url', 'origin', ORIGIN]);
  git(destination, ['config', 'core.autocrlf', 'false']);
  git(destination, ['checkout', '--detach', revision]);
  if (fs.existsSync(path.join(destination, MANIFEST))) throw new Error('Source must be a development commit, not a generated deployment');
  const previous = transport.tip(destination);
  if (previous) {
    if (transport.fetch(destination) !== previous) throw new Error('Deployment changed during preparation; prepare again');
    const old = validatePrevious(destination, previous);
    git(destination, ['merge-base', '--is-ancestor', old.source_revision, revision]);
  }
  // Read blobs, not the caller worktree, and later compare the checkout after build.
  const sourceFiles = sourceHashes(root, entries);
  build(destination);
  const assets = Object.fromEntries(walk(path.join(destination, DIST)).map(name => [name, hash(fs.readFileSync(path.join(destination, DIST, name)))]));
  const frontendFiles = Object.fromEntries(Object.entries(sourceFiles).filter(([name]) => /^(web\/|scripts\/|package\.json$|pnpm-)/.test(name)));
  const graph = JSON.parse(fs.readFileSync(path.join(destination, 'tmp/bundle/graph.json'), 'utf8'));
  if (graph.version !== 1 || !graph.chunks?.length) throw new Error('Missing bundle graph');
  const chunks = Object.fromEntries(graph.chunks.map(chunk => {
    if (assets[chunk.file] !== chunk.sha256) throw new Error('Stale bundle graph: ' + chunk.file);
    return [chunk.file, [...chunk.imports, ...chunk.dynamicImports, ...chunk.css].sort()];
  }).sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0));
  const manifest = { schema: 1, source_revision: revision, previous_deploy: previous, build_version: 'fork', build_tag: tag,
    source_files: sourceFiles, source_sha256: digest(sourceFiles), frontend_sha256: digest(frontendFiles), assets, assets_sha256: digest(assets), chunks };
  fs.writeFileSync(path.join(destination, MANIFEST), `${JSON.stringify(manifest, null, 2)}\n`);
  verify(destination);
  console.log(`Prepared ${destination} from ${revision}; publication is a separate command.`);
}

export function publish(root, transport = GIT_TRANSPORT) {
  assertOrigin(root);
  const manifest = verify(root);
  const current = transport.tip(root);
  if (current !== manifest.previous_deploy) throw new Error('Deployment changed since preparation; prepare again');
  if (current) {
    if (transport.fetch(root) !== current) throw new Error('Deployment changed during publication');
    const old = validatePrevious(root, current);
    git(root, ['merge-base', '--is-ancestor', old.source_revision, manifest.source_revision]);
  }
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'omc-prebuilt-index-'));
  try {
    const env = { ...process.env, GIT_INDEX_FILE: path.join(temp, 'index') };
    git(root, ['read-tree', '--empty'], { env });
    const entries = verifySource(root, manifest);
    const generated = [...Object.keys(manifest.assets).map(name => DIST + name), MANIFEST];
    if (generated.some(name => /[\r\n"\\]/.test(name))) throw new Error('Invalid generated path');
    const objects = git(root, ['hash-object', '-w', '--stdin-paths'], { input: generated.join('\n') + '\n' }).split('\n');
    const payload = [...entries, ...generated.map((name, index) => ({ name, mode: '100644', object: objects[index] }))];
    git(root, ['update-index', '-z', '--index-info'], { env, input: payload.map(({ name, mode, object }) => `${mode} ${object}\t${name}\0`).join('') });
    const tree = git(root, ['write-tree'], { env });
    const parents = [current, manifest.source_revision].filter(Boolean).flatMap(sha => ['-p', sha]);
    const commit = git(root, ['commit-tree', tree, ...parents, '-m', `build: prepare SPA from ${manifest.source_revision}`]);
    validatePrevious(root, commit);
    // A concurrent advance is rejected by Git's ordinary fast-forward rule.
    transport.push(root, commit, current);
    console.log(`Published ${commit} to origin ${DEPLOY_REF} (source ${manifest.source_revision})`);
    return commit;
  } finally {
    fs.rmSync(temp, { recursive: true, force: true });
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [command, ...args] = process.argv.slice(2);
    if (command === 'prepare' && (args.length === 2 || args.length === 3)) prepare(process.cwd(), args[0], path.resolve(args[1]), args[2]);
    else if (command === 'verify' && args.length === 1) { verify(path.resolve(args[0])); console.log('Prebuilt payload verified'); }
    else if (command === 'publish' && args.length === 1) publish(path.resolve(args[0]));
    else throw new Error('Usage: node scripts/prebuilt-web.mjs prepare <40-char-source-sha> <new-directory> [tag] | verify <directory> | publish <directory>');
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
