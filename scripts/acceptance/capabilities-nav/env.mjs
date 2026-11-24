#!/usr/bin/env node
// Starts and stops the self-hosted environments this acceptance runs against: the fake CPA plus a built binary,
// through the delivery's existing lending-experience-env.mjs (cases.md 0.3). One environment is one detached node
// process; its application is a child of it, so stopping an environment kills the tree.
//
//   node env.mjs start <role> --root <worktree> --binary <exe> --data <dir> --app-port N --cpa-port N --control-port N
//   node env.mjs stop <role>
//   node env.mjs status
//   node env.mjs demo-start <role> --binary <exe> --data <dir> --port N      (demonstration mode, no fake CPA)
//   node env.mjs copy-data <from> <to>                                      (both directories must belong to stopped environments)
//
// Registry: <output>/net/s8/env-<role>.json (pids, ports, log path). Only 127.0.0.1 is ever listened on.
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { NET, until } from './lib.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const registryFile = (role) => path.join(NET, `env-${role}.json`);

function option(args, name, fallback) {
  const index = args.indexOf(`--${name}`);
  return index >= 0 ? args[index + 1] : fallback;
}

export function readRegistry(role) {
  try { return JSON.parse(fs.readFileSync(registryFile(role), 'utf8')); } catch { return null; }
}

async function answers(url) {
  try {
    const response = await fetch(url, { redirect: 'manual', signal: AbortSignal.timeout(1500) });
    return response.status > 0;
  } catch { return false; }
}

/** Starts one self-hosted environment and returns once its application answers. */
export async function startEnvironment({ role, root, binary, data, appPort, cpaPort, controlPort }) {
  fs.mkdirSync(data, { recursive: true });
  const log = path.join(NET, `env-${role}.log`);
  const fd = fs.openSync(log, 'w');
  // Always this checkout's copy of the environment script, for the baseline as well: it starts the application with
  // windowsHide, so no console window opens on the desktop. The script needs only the binary and the data directory,
  // which are absolute paths, so `root` (the checkout the binary was built from) is informational.
  const script = path.resolve(here, '../lending-experience-env.mjs');
  if (!fs.existsSync(script)) throw new Error(`missing ${script}`);
  const child = spawn(process.execPath, [script], {
    cwd: path.resolve(here, '../../..'), detached: true, windowsHide: true, stdio: ['ignore', fd, fd],
    env: {
      ...process.env,
      OMCPA_BROWSER_BINARY: binary,
      OMC_LENDING_APP_PORT: String(appPort), OMC_LENDING_CPA_PORT: String(cpaPort), OMC_LENDING_CONTROL_PORT: String(controlPort),
      OMC_LENDING_DATA_DIR: data,
    },
  });
  child.unref();
  fs.writeFileSync(registryFile(role), JSON.stringify({ role, nodePid: child.pid, appPort, cpaPort, controlPort, data, binary, root, log, app: `http://127.0.0.1:${appPort}/omc`, startedAt: new Date().toISOString() }, null, 2));
  await until(() => fs.readFileSync(log, 'utf8').includes('lending experience environment ready') || /failed|EADDRINUSE/.test(fs.readFileSync(log, 'utf8')), { label: `${role} environment ready`, timeoutMs: 90_000 });
  const text = fs.readFileSync(log, 'utf8');
  if (!text.includes('lending experience environment ready')) throw new Error(`${role} did not start:\n${text.slice(-1500)}`);
  return readRegistry(role);
}

/** Starts the binary in demonstration mode on its own port (cases.md 0.5). */
export async function startDemo({ role, binary, data, port }) {
  fs.mkdirSync(data, { recursive: true });
  const log = path.join(NET, `env-${role}.log`);
  const fd = fs.openSync(log, 'w');
  const environment = Object.fromEntries(Object.entries(process.env).filter(([name]) => !/^OMCPA_/i.test(name)));
  Object.assign(environment, {
    OMCPA_ENV_FILE: '\\\\.\\nul', OMCPA_DEMO_MODE: 'true', OMCPA_BASE_PATH: '/', OMCPA_LISTEN_ADDR: `127.0.0.1:${port}`, OMCPA_DATA_DIR: data,
    OMCPA_MASTER_KEY: 'demo-nav-acceptance-master-key-000000', OMCPA_CPA_BASE_URL: 'http://127.0.0.1:1', OMCPA_CPA_MANAGEMENT_KEY: 'unused-in-demo', TZ: 'Asia/Shanghai',
  });
  const child = spawn(binary, [], { detached: true, windowsHide: true, stdio: ['ignore', fd, fd], env: environment });
  child.unref();
  fs.writeFileSync(registryFile(role), JSON.stringify({ role, nodePid: child.pid, appPort: port, data, binary, log, app: `http://127.0.0.1:${port}`, startedAt: new Date().toISOString() }, null, 2));
  await until(() => answers(`http://127.0.0.1:${port}/api/healthz`), { label: `${role} demo answers`, timeoutMs: 60_000 });
  return readRegistry(role);
}

export function stopEnvironment(role) {
  const entry = readRegistry(role);
  if (!entry) return false;
  // taskkill /T takes the application child with the node process; the application is the reason to kill the tree.
  spawnSync('taskkill', ['/PID', String(entry.nodePid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
  fs.rmSync(registryFile(role), { force: true });
  return true;
}

export function copyData(from, to) {
  fs.rmSync(to, { recursive: true, force: true });
  fs.cpSync(from, to, { recursive: true });
}

async function main() {
  const [command, role, ...rest] = process.argv.slice(2);
  if (command === 'start') {
    const entry = await startEnvironment({
      role, root: path.resolve(option(rest, 'root', path.resolve(here, '../../..'))), binary: path.resolve(option(rest, 'binary')), data: path.resolve(option(rest, 'data')),
      appPort: Number(option(rest, 'app-port')), cpaPort: Number(option(rest, 'cpa-port')), controlPort: Number(option(rest, 'control-port')),
    });
    console.log(JSON.stringify(entry));
  } else if (command === 'demo-start') {
    console.log(JSON.stringify(await startDemo({ role, binary: path.resolve(option(rest, 'binary')), data: path.resolve(option(rest, 'data')), port: Number(option(rest, 'port')) })));
  } else if (command === 'stop') {
    console.log(stopEnvironment(role) ? `stopped ${role}` : `${role} is not registered`);
  } else if (command === 'copy-data') {
    copyData(path.resolve(role), path.resolve(rest[0]));
    console.log(`copied ${role} -> ${rest[0]}`);
  } else if (command === 'status') {
    for (const file of fs.readdirSync(NET).filter((name) => /^env-.*\.json$/.test(name))) console.log(fs.readFileSync(path.join(NET, file), 'utf8'));
  } else {
    console.error('usage: env.mjs start|demo-start|stop|copy-data|status');
    process.exit(2);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main();
