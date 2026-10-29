import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { Writable } from 'node:stream';
import { test } from 'node:test';
import { build, createServer } from '../web/node_modules/vite/dist/node/index.js';
import { lobeIconsPlugin, syncLobeIcons } from './sync-lobe-icons.mjs';

const PROVIDER_ICONS = [
  'claude-color.svg',
  'codex-color.svg',
  'antigravity-color.svg',
  'stepfun-color.svg',
  'ollama.svg',
];

const FALLBACK_HTML = '<!doctype html><html><body data-omc-fixture="fallback"></body></html>';

class Capture extends Writable {
  constructor() {
    super();
    this.chunks = [];
    this.headers = new Map();
    this.statusCode = 200;
  }

  _write(chunk, _encoding, callback) {
    this.chunks.push(Buffer.from(chunk));
    callback();
  }

  setHeader(name, value) {
    this.headers.set(name.toLowerCase(), value);
  }

  getHeader(name) {
    return this.headers.get(name.toLowerCase());
  }

  writeHead(status, headers = {}) {
    this.statusCode = status;
    for (const [name, value] of Object.entries(headers)) this.setHeader(name, value);
    return this;
  }

  body() {
    return Buffer.concat(this.chunks);
  }
}

/**
 * Drives Vite's real middleware stack without opening a TCP listener. The public
 * middleware is the behavior under test, so using its own stack keeps the claim
 * at the lifecycle layer that actually failed rather than duplicating Vite.
 */
function requestMiddleware(server, url) {
  const response = new Capture();
  const request = { method: 'GET', url, headers: { host: '127.0.0.1' } };
  return new Promise((resolve, reject) => {
    let settled = false;
    const settle = (error) => {
      if (settled) return;
      settled = true;
      if (error) reject(error);
      else resolve({ status: response.statusCode, headers: response.headers, body: response.body() });
    };
    response.once('finish', () => settle());
    response.once('error', settle);
    try {
      server.middlewares.handle(request, response, (error) => settle(error));
    } catch (error) {
      settle(error);
    }
  });
}

async function startFixtureServer(t, makePlugin) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'omc-lobe-icons-'));
  const publicDir = path.join(root, 'public');
  const target = path.join(publicDir, 'lobe-icons');
  fs.mkdirSync(publicDir, { recursive: true });
  fs.writeFileSync(path.join(root, 'index.html'), FALLBACK_HTML);
  const wasAbsent = !fs.existsSync(target);
  let server;
  try {
    server = await createServer({
      root,
      configFile: false,
      appType: 'spa',
      base: '/omc/',
      publicDir,
      plugins: [makePlugin(target)],
      server: { middlewareMode: true },
      logLevel: 'silent',
    });
  } catch (error) {
    fs.rmSync(root, { recursive: true, force: true });
    throw error;
  }
  t.after(async () => {
    await server.close();
    fs.rmSync(root, { recursive: true, force: true });
  });
  return { publicDir, target, server, wasAbsent };
}

test('cold dev startup generates the five provider SVGs before Vite snapshots public files', async (t) => {
  let syncCalls = 0;
  const fixture = await startFixtureServer(t, (target) => lobeIconsPlugin({
    quiet: true,
    sync: ({ quiet }) => {
      syncCalls += 1;
      syncLobeIcons({ quiet, target });
    },
  }));
  assert.equal(fixture.wasAbsent, true, 'the fixture starts with no generated icon directory');
  assert.equal(syncCalls, 1, 'serve generates once during config resolution, before Vite scans publicDir');

  for (const icon of PROVIDER_ICONS) {
    const response = await requestMiddleware(fixture.server, `/omc/lobe-icons/${icon}`);
    const body = response.body;
    assert.equal(response.status, 200, `${icon} status`);
    assert.equal(response.headers.get('content-type'), 'image/svg+xml', `${icon} content type`);
    assert.ok(body.length > 100, `${icon} carries SVG bytes`);
    assert.equal(body.subarray(0, 4).toString(), '<svg', `${icon} starts with the SVG element`);
    assert.ok(!body.includes('<html'), `${icon} is not the SPA fallback`);
  }
});

test('the old buildStart-only lifecycle serves the exact SPA fallback for a cold icon request', async (t) => {
  const fixture = await startFixtureServer(t, (target) => ({
    name: 'legacy-sync-lobe-icons',
    enforce: 'pre',
    buildStart() {
      syncLobeIcons({ quiet: true, target });
    },
  }));
  assert.ok(
    fs.existsSync(path.join(fixture.target, 'claude-color.svg')),
    'buildStart did generate the file after Vite had already taken its public inventory',
  );

  const response = await requestMiddleware(fixture.server, '/omc/lobe-icons/claude-color.svg');
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('content-type'), 'text/html');
  const html = response.body.toString();
  assert.match(html, /^<!doctype html>/i, 'the request fell through to the SPA fallback document');
  assert.match(html, /data-omc-fixture="fallback"/, 'the response is the fixture shell, not an SVG');
  assert.match(html, /<\/html>$/, 'the fallback document is complete');
});

test('each build regenerates icons and copies them into the build output', async (t) => {
  const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'omc-lobe-icons-build-')));
  const publicDir = path.join(root, 'public');
  const target = path.join(publicDir, 'lobe-icons');
  const outDir = path.join(root, 'dist');
  fs.mkdirSync(publicDir, { recursive: true });
  fs.writeFileSync(path.join(root, 'index.html'), '<!doctype html><body>build fixture</body>');
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  const plugin = lobeIconsPlugin({
    quiet: true,
    sync: ({ quiet }) => syncLobeIcons({ quiet, target }),
  });
  const config = {
    root,
    configFile: false,
    logLevel: 'silent',
    publicDir,
    plugins: [plugin],
    build: { outDir, emptyOutDir: true },
  };

  for (const pass of ['first build', 'second build']) {
    fs.rmSync(target, { recursive: true, force: true });
    fs.rmSync(path.join(outDir, 'lobe-icons'), { recursive: true, force: true });
    await build(config);
    const sourceIcon = path.join(target, 'claude-color.svg');
    const builtIcon = path.join(outDir, 'lobe-icons', 'claude-color.svg');
    assert.ok(fs.existsSync(sourceIcon), `${pass} regenerates the generated public inventory`);
    assert.ok(fs.existsSync(builtIcon), `${pass} copies the regenerated inventory into the build output`);
    assert.ok(fs.lstatSync(sourceIcon).isFile(), `${pass} generates regular files rather than symlinks`);
    assert.ok(fs.lstatSync(builtIcon).isFile(), `${pass} copies regular files into the output allowlist`);
    const bytes = fs.readFileSync(builtIcon);
    assert.equal(bytes.subarray(0, 4).toString(), '<svg', `${pass} keeps SVG bytes`);
    assert.deepEqual(bytes, fs.readFileSync(sourceIcon), `${pass} copies the generated bytes unchanged`);
  }
});

test('buildStart regenerates on every invocation so a watch rebuild cannot reuse a stale inventory', () => {
  let syncCalls = 0;
  const plugin = lobeIconsPlugin({ quiet: true, sync: () => { syncCalls += 1; } });
  plugin.configResolved({ command: 'build' });
  assert.equal(syncCalls, 0, 'configuration alone does not generate for a build');
  plugin.buildStart();
  plugin.buildStart();
  assert.equal(syncCalls, 2, 'every Rollup buildStart, including a watch rebuild, regenerates the icons');
});
