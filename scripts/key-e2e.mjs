// Local entry point for the repository-key delivery harness
// (internal/keyclient/e2e). It is not part of any gate: the project decision of
// 2026-11-17 keeps `test:key-e2e` a developer command, and CI gets an entry of
// its own when that is decided.
//
// The harness needs a Go toolchain, which this machine does not have on PATH.
// When `go` is missing and Docker is present, the same test runs in the
// golang container with the checkout mounted read-only and the repository's
// module and build caches, so nothing is downloaded and nothing in the checkout
// is written.
//
// Exit code: 0 when the loop passed, non-zero otherwise. The run prints one
// line and leaves its evidence in the directory named by OMC_E2E_EVIDENCE_DIR.

import { spawn } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const evidenceDir = process.env.OMC_E2E_EVIDENCE_DIR || path.join(os.tmpdir(), 'omc-key-e2e-evidence');
// The evidence directory is the one writable mount, so it has to exist before
// Docker creates the mount point itself.
mkdirSync(evidenceDir, { recursive: true });
const env = { ...process.env, ASSISTANT_NO_SEND: '1', OMC_E2E_EVIDENCE_DIR: evidenceDir };

function run(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { stdio: 'inherit', ...options });
    child.on('error', reject);
    child.on('exit', (code) => resolve(code ?? 1));
  });
}

function hasGo() {
  return (process.env.PATH || '').split(path.delimiter).some((entry) => {
    const candidate = path.join(entry, process.platform === 'win32' ? 'go.exe' : 'go');
    return entry && existsSync(candidate);
  });
}

// The Docker recipe this repository already uses for its Go gates: the network
// is off, the module cache is the prepared volume, and the checkout is mounted
// read-only. The evidence directory is the one writable mount.
function dockerArgs() {
  const args = ['run', '--rm', '--network', 'none'];
  args.push('-v', `${root}:/src:ro`);
  args.push('-v', `${evidenceDir}:/evidence`);
  args.push('-v', 'omc-go-mod:/go/pkg/mod');
  args.push('-v', 'omc-gocache:/root/.cache/go-build');
  args.push('-w', '/src');
  for (const [name, value] of Object.entries({ GOPROXY: 'off', GOFLAGS: '-mod=readonly', TZ: 'Asia/Shanghai', ASSISTANT_NO_SEND: '1', OMC_E2E_EVIDENCE_DIR: '/evidence' })) {
    args.push('-e', `${name}=${value}`);
  }
  args.push('golang:1.27.1', 'go', 'test', '-tags', 'e2e', '-count=1', '-v', './internal/keyclient/e2e');
  return args;
}

const testArgs = ['test', '-tags', 'e2e', '-count=1', '-v', './internal/keyclient/e2e'];
let command;
let args;
let options = { cwd: root, env };
if (hasGo()) {
  command = 'go';
  args = testArgs;
} else {
  command = 'docker';
  args = dockerArgs();
  options = { env };
  console.log('go is not on PATH; running the harness in golang:1.27.1 (offline, repository caches)');
}

const code = await run(command, args, options);
if (code !== 0) {
  console.error(`key e2e failed with exit code ${code}`);
  process.exit(code);
}
console.log(`key e2e passed; evidence in ${evidenceDir}`);
