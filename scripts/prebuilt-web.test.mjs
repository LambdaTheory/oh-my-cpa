import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import test from 'node:test';
import { ORIGIN, DEPLOY_REF, MANIFEST, prepare, publish, assertOrigin, isSource, hash, digest, verifySource, validatePrevious, pushExpectedTip } from './prebuilt-web.mjs';

const git = (root, args, options = {}) => execFileSync('git', args, { cwd: root, encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'], ...options }).trim();
const write = (root, name, content) => { fs.mkdirSync(path.dirname(path.join(root, name)), { recursive: true }); fs.writeFileSync(path.join(root, name), content); };

function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'omc-prebuilt-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const repo = path.join(root, 'source'); fs.mkdirSync(repo);
  git(repo, ['init', '-b', 'dev']);
  git(repo, ['config', 'user.name', 'Fixture']); git(repo, ['config', 'user.email', 'fixture@example.invalid']);
  git(repo, ['config', 'core.autocrlf', 'false']);
  git(repo, ['remote', 'add', 'origin', ORIGIN]);
  const files = {
    'web/vite.config.ts': 'relative base', 'web/package.json': '{}', 'pnpm-lock.yaml': 'lock',
    'scripts/sync-web-dist.mjs': 'sync', 'go.mod': 'module fixture\n\ngo 1.25.0\n',
    'Dockerfile.prebuilt': 'FROM golang', 'web/src/generated/lobeIconCatalog.json': '[{"id":"Example","hasColor":true}]',
    'web/public/favicon.svg': 'icon', 'cmd/main.go': 'package main', 'migrations/001.sql': 'select 1',
    'internal/web/dist/.gitkeep': '', '.gitignore': 'node_modules/\ninternal/web/dist/*\n!internal/web/dist/.gitkeep\n',
    'scripts/prebuilt-verify/main.go': fs.readFileSync(new URL('./prebuilt-verify/main.go', import.meta.url)),
    'README.md': 'not part of deployment payload', 'deploy/healthcheck.sh': 'healthcheck',
  };
  for (const [name, content] of Object.entries(files)) write(repo, name, content);
  git(repo, ['add', '.']); git(repo, ['commit', '-m', 'fixture']);
  const revision = git(repo, ['rev-parse', 'HEAD']);
  const remote = path.join(root, 'remote.git'); git(root, ['init', '--bare', remote]);
  const transport = {
    tip(cwd) { const result = git(cwd, ['ls-remote', '--refs', remote, DEPLOY_REF]); return result ? result.split(/\s+/)[0] : ''; },
    fetch(cwd) { git(cwd, ['fetch', '--no-tags', remote, DEPLOY_REF]); return git(cwd, ['rev-parse', 'FETCH_HEAD']); },
    push(cwd, commit, previous = '') { pushExpectedTip(cwd, remote, commit, previous); },
  };
  const build = cwd => {
    write(cwd, 'tmp/bundle/graph.json', JSON.stringify({ version: 1, chunks: [{ file: 'assets/main.js', sha256: hash('export default 1'), imports: [], dynamicImports: [], css: [] }] }));
    for (const [name, content] of Object.entries({
      'index.html': '<script type="module" src="./assets/main.js"></script>', 'assets/main.js': 'export default 1',
      'favicon.svg': 'icon', 'lobe-icons/example.svg': 'icon', 'lobe-icons/example-color.svg': 'color',
    })) write(cwd, 'internal/web/dist/' + name, content);
    write(cwd, 'node_modules/local-native.node', 'host binary');
    write(cwd, '.env', 'synthetic environment'); write(cwd, 'local.db', 'synthetic database');
    write(cwd, '.orchestration/state.json', '{}'); write(cwd, 'arbitrary.txt', 'not published');
    git(cwd, ['config', 'user.name', 'Fixture']); git(cwd, ['config', 'user.email', 'fixture@example.invalid']);
  };
  return { root, repo, revision, transport, build };
}

test('prepare is isolated; publish allowlists bytes and advances deployment history normally', t => {
  const f = fixture(t); const first = path.join(f.root, 'prepared-first');
  write(f.repo, 'arbitrary.txt', 'caller artwork');
  write(f.repo, 'web/vite.config.ts', 'caller work in progress');
  const status = git(f.repo, ['status', '--porcelain=v1']);
  const index = fs.readFileSync(path.join(f.repo, '.git/index'));
  prepare(f.repo, f.revision, first, 'test-build', f);
  assert.equal(git(f.repo, ['status', '--porcelain=v1']), status);
  assert.deepEqual(fs.readFileSync(path.join(f.repo, '.git/index')), index);
  assert.equal(git(f.repo, ['branch', '--show-current']), 'dev');
  const firstIndex = fs.readFileSync(path.join(first, '.git/index'));
  const deployed = publish(first, f.transport);
  assert.deepEqual(fs.readFileSync(path.join(first, '.git/index')), firstIndex);
  assert.equal(git(first, ['rev-parse', 'HEAD']), f.revision);
  const manifest = validatePrevious(first, deployed);
  assert.equal(manifest.source_revision, f.revision); assert.notEqual(deployed, f.revision);
  const names = git(first, ['ls-tree', '-r', '--name-only', deployed]).split('\n');
  for (const name of ['.env', 'local.db', '.orchestration/state.json', 'arbitrary.txt', 'node_modules/local-native.node', 'README.md']) assert.ok(!names.includes(name), name);
  assert.ok(names.includes('internal/web/dist/lobe-icons/example-color.svg'));
  assert.throws(() => publish(first, f.transport), /changed since preparation/);
  write(f.repo, 'web/vite.config.ts', 'next source'); git(f.repo, ['add', 'web/vite.config.ts']); git(f.repo, ['commit', '-m', 'next']);
  const next = git(f.repo, ['rev-parse', 'HEAD']); const second = path.join(f.root, 'prepared-second');
  prepare(f.repo, next, second, '', f);
  const secondDeploy = publish(second, f.transport);
  assert.equal(git(second, ['show', '-s', '--format=%P', secondDeploy]), deployed + ' ' + next);
  assert.throws(() => prepare(f.repo, f.revision, path.join(f.root, 'regressed'), '', f));
});

test('strict origin identity and immutable revisions refuse before any build', t => {
  const f = fixture(t);
  assert.throws(() => prepare(f.repo, 'dev', path.join(f.root, 'bad'), '', f), /immutable/);
  assert.throws(() => prepare(f.repo, f.revision, path.join(f.repo, 'nested'), '', f), /outside/);
  assert.throws(() => prepare(f.repo, f.revision, path.join(f.root, 'bad-tag'), 'tag\n', f), /Invalid build tag/);
  for (const url of ['https://github.com/WizisCool/oh-my-cpa.git', 'https://github.com/LambdaTheory/other.git', 'https://github.com/Newoahil/oh-my-cpa.git']) {
    git(f.repo, ['remote', 'set-url', 'origin', url]); assert.throws(() => assertOrigin(f.repo), /exactly/);
  }
  git(f.repo, ['remote', 'set-url', 'origin', ORIGIN]);
  git(f.repo, ['config', 'remote.origin.pushurl', 'https://github.com/WizisCool/oh-my-cpa.git']);
  assert.throws(() => assertOrigin(f.repo), /exactly/);
  assert.equal(fs.existsSync(path.join(f.root, 'bad')), false);
});

test('source whitelist and inventories reject tracked sensitive paths and source tampering', t => {
  for (const name of ['.env', '.orchestration/task.json', 'untracked.png', 'node_modules/native.node']) assert.equal(isSource(name), false);
  for (const name of ['web/.env.local', 'internal/private.key', 'scripts/state.db', 'web/node_modules/file.js']) assert.throws(() => isSource(name), /Forbidden/);
  const f = fixture(t); const prepared = path.join(f.root, 'prepared'); prepare(f.repo, f.revision, prepared, '', f);
  const manifest = JSON.parse(fs.readFileSync(path.join(prepared, MANIFEST)));
  const reversed = Object.fromEntries(Object.entries(manifest.source_files).reverse());
  assert.equal(digest(reversed), manifest.source_sha256);
  write(prepared, 'web/vite.config.ts', 'changed');
  assert.throws(() => verifySource(prepared, manifest), /Modified source/);
  manifest.source_files['web/vite.config.ts'] = hash('changed');
  assert.throws(() => verifySource(prepared, manifest), /source commit/);
});

test('publication refuses remote races and unknown modifications without overwriting them', t => {
  const f = fixture(t); const prepared = path.join(f.root, 'prepared'); prepare(f.repo, f.revision, prepared, '', f);
  const deployed = publish(prepared, f.transport);
  const second = path.join(f.root, 'second'); prepare(f.repo, f.revision, second, '', f);
  const tree = git(second, ['rev-parse', deployed + '^{tree}']);
  const unknown = git(second, ['commit-tree', tree, '-p', deployed, '-m', 'operator change']);
  assert.throws(() => publish(second, { ...f.transport, push(cwd, commit, previous) { f.transport.push(cwd, unknown, deployed); f.transport.push(cwd, commit, previous); } }), /failed|rejected/);
  assert.equal(f.transport.tip(second), unknown);
  assert.throws(() => prepare(f.repo, f.revision, path.join(f.root, 'unknown'), '', f), /Unknown deployment commit parents/);
});

test('deployment history accepts a pull-request merge that wraps a published commit', t => {
  const f = fixture(t); const first = path.join(f.root, 'prepared-first'); prepare(f.repo, f.revision, first, '', f);
  const deployed = publish(first, f.transport);
  write(f.repo, 'web/vite.config.ts', 'next source'); git(f.repo, ['add', '.']); git(f.repo, ['commit', '-m', 'next']);
  const next = git(f.repo, ['rev-parse', 'HEAD']); const second = path.join(f.root, 'prepared-second');
  prepare(f.repo, next, second, '', f);
  const advanced = publish(second, f.transport);
  const tree = git(second, ['rev-parse', advanced + '^{tree}']);
  const merged = git(second, ['commit-tree', tree, '-p', deployed, '-p', advanced, '-m', 'Merge pull request']);
  f.transport.push(second, merged, advanced);
  assert.equal(validatePrevious(second, merged).source_revision, next);
  const unrelated = git(second, ['commit-tree', git(second, ['rev-parse', deployed + '^{tree}']), '-p', deployed, '-p', advanced, '-m', 'Unrelated tree']);
  assert.throws(() => validatePrevious(second, unrelated), /Unknown deployment commit parents/);
  write(f.repo, 'web/vite.config.ts', 'third source'); git(f.repo, ['add', '.']); git(f.repo, ['commit', '-m', 'third']);
  const third = git(f.repo, ['rev-parse', 'HEAD']);
  assert.doesNotThrow(() => prepare(f.repo, third, path.join(f.root, 'prepared-third'), '', f));
});

test('publication refuses a concurrent first branch even when its tip is a source ancestor', t => {
  const f=fixture(t); const prepared=path.join(f.root,'prepared'); prepare(f.repo,f.revision,prepared,'',f);
  assert.throws(()=>publish(prepared,{...f.transport,push(cwd,commit,previous){f.transport.push(cwd,f.revision);f.transport.push(cwd,commit,previous);}}),/failed|rejected/);
  assert.equal(f.transport.tip(prepared),f.revision);
});
