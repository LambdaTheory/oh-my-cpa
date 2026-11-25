#!/usr/bin/env node
// Runs the built binary in demonstration mode in the foreground, for a launcher that owns the process tree (experience.py starts
// this under its own hidden console and stops it by killing the tree). It listens on 127.0.0.1 only and serves the committed
// demonstration data; there is no sign-in, no gateway and nothing is written outside the given data directory.
//   node scripts/acceptance/capabilities-nav/demo-serve.mjs <binary> <data dir> <port>
import { spawn } from 'node:child_process';
import fs from 'node:fs';

const [binary, data, port] = process.argv.slice(2);
if (!binary || !data || !port) {
  console.error('usage: demo-serve.mjs <binary> <data dir> <port>');
  process.exit(2);
}
fs.mkdirSync(data, { recursive: true });
const environment = Object.fromEntries(Object.entries(process.env).filter(([name]) => !/^OMCPA_/i.test(name)));
Object.assign(environment, {
  OMCPA_ENV_FILE: '\\\\.\\nul', OMCPA_DEMO_MODE: 'true', OMCPA_BASE_PATH: '/', OMCPA_LISTEN_ADDR: `127.0.0.1:${port}`, OMCPA_DATA_DIR: data,
  OMCPA_MASTER_KEY: 'demo-nav-acceptance-master-key-000000', OMCPA_CPA_BASE_URL: 'http://127.0.0.1:1', OMCPA_CPA_MANAGEMENT_KEY: 'unused-in-demo', TZ: 'Asia/Shanghai',
});
const child = spawn(binary, [], { env: environment, stdio: 'inherit', windowsHide: true });
child.on('exit', (code) => process.exit(code ?? 0));
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill());
