import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const PREFIX = 'github.com/oh-my-cpa/oh-my-cpa/internal/config.';
// A bare $ also accepts a trailing newline, which is not a linker-safe value.
const REVISION = /^[a-f0-9]{40}(?:[a-f0-9]{24})?(?![\s\S])/;
const LABEL = /^[A-Za-z0-9][A-Za-z0-9._/+@-]{0,199}(?![\s\S])/;

function readMetadata(root, name) {
  try {
    const filename = path.join(root, '.git', name);
    if (!fs.lstatSync(filename).isFile()) return '';
    return fs.readFileSync(filename, 'utf8').trim();
  } catch {
    return '';
  }
}

export function resolveBuildIdentity(root, supplied = {}) {
  const head = readMetadata(root, 'HEAD');
  const candidateRef = head.startsWith('ref: ') ? head.slice(5) : '';
  const ref = LABEL.test(candidateRef) && candidateRef.startsWith('refs/') && !candidateRef.includes('..')
    ? candidateRef : '';
  let revision = REVISION.test(head) ? head : '';
  if (ref) {
    revision = readMetadata(root, ref);
    if (!REVISION.test(revision)) {
      const packed = readMetadata(root, 'packed-refs').split('\n').map(line => line.split(' '));
      revision = packed.find(fields => fields.length === 2 && fields[1] === ref)?.[0] ?? '';
    }
  }
  if (!REVISION.test(revision)) revision = '';
  const suppliedRevision = REVISION.test(supplied.revision ?? '') ? supplied.revision : '';
  if (revision && suppliedRevision && revision !== suppliedRevision) {
    throw new Error('Supplied build revision differs from the checked-out source');
  }
  const suppliedRef = LABEL.test(supplied.ref ?? '') && !supplied.ref.includes('..') ? supplied.ref : '';
  if (ref && suppliedRef && ref !== suppliedRef) {
    throw new Error('Supplied build ref differs from the checked-out source');
  }
  return {
    revision: revision || suppliedRevision,
    ref: ref || suppliedRef,
    tag: LABEL.test(supplied.tag ?? '') ? supplied.tag : '',
  };
}

export function buildIdentityFlags(identity) {
  return Object.entries({ BuildRevision: identity.revision, BuildRef: identity.ref, BuildTag: identity.tag })
    .filter(([, value]) => value)
    .map(([name, value]) => `-X ${PREFIX}${name}=${value}`).join(' ');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const identity = resolveBuildIdentity(process.argv[2], {
    revision: process.env.REVISION, ref: process.env.BUILD_REF, tag: process.env.BUILD_TAG,
  });
  process.stdout.write(buildIdentityFlags(identity));
}
