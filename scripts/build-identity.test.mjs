import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { buildIdentityFlags, resolveBuildIdentity } from './build-identity.mjs';

const REVISION = 'a1'.repeat(20);
function withCheckout(run) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'omc-identity-'));
  fs.mkdirSync(path.join(root, '.git/refs/heads'), { recursive: true });
  try { run(root); } finally { fs.rmSync(root, { recursive: true, force: true }); }
}
test('checkout HEAD resolves loose and packed refs without Git config or objects', () => withCheckout(root => {
  fs.writeFileSync(path.join(root, '.git/HEAD'), 'ref: refs/heads/dev\n');
  fs.writeFileSync(path.join(root, '.git/refs/heads/dev'), REVISION);
  const expected = { revision: REVISION, ref: 'refs/heads/dev', tag: '' };
  assert.deepEqual(resolveBuildIdentity(root), expected);
  fs.unlinkSync(path.join(root, '.git/refs/heads/dev'));
  fs.writeFileSync(path.join(root, '.git/packed-refs'), `${REVISION} refs/heads/dev\n`);
  assert.deepEqual(resolveBuildIdentity(root), expected);
  assert.throws(() => resolveBuildIdentity(root, { revision: 'b2'.repeat(20) }), /differs/);
  assert.throws(() => resolveBuildIdentity(root, { ref: 'refs/heads/master' }), /differs/);
}));
test('detached and absent source never manufacture a branch or version', () => withCheckout(root => {
  fs.writeFileSync(path.join(root, '.git/HEAD'), REVISION);
  assert.deepEqual(resolveBuildIdentity(root), { revision: REVISION, ref: '', tag: '' });
  fs.unlinkSync(path.join(root, '.git/HEAD'));
  assert.deepEqual(resolveBuildIdentity(root), { revision: '', ref: '', tag: '' });
  assert.deepEqual(resolveBuildIdentity(root, { revision: REVISION, tag: 'v2.1.0-local' }),
    { revision: REVISION, ref: '', tag: 'v2.1.0-local' });
  assert.equal(buildIdentityFlags(resolveBuildIdentity(root)), '');
  assert.deepEqual(resolveBuildIdentity(root, { ref: '../../private', tag: 'bad -X injected' }),
    { revision: '', ref: '', tag: '' });
  assert.deepEqual(resolveBuildIdentity(root, { revision: `${REVISION}\n`, ref: 'dev\n', tag: 'v1.2.3\n' }),
    { revision: '', ref: '', tag: '' });
}));
test('generated linker flags carry exactly the observed allowlisted values', () => {
  const identity = { revision: REVISION, ref: 'refs/heads/fixture', tag: 'v2.1.0-local' };
  const flags = buildIdentityFlags(identity);
  for (const [field, value] of [['BuildRevision', identity.revision], ['BuildRef', identity.ref], ['BuildTag', identity.tag]]) {
    assert.ok(flags.includes(`${field}=${value}`));
  }
  assert.equal(flags.split(' -X ').length, 3);
});
