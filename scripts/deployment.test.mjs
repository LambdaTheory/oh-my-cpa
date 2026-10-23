import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import YAML from 'yaml';

function readDeployment(name) {
  return YAML.parse(fs.readFileSync(new URL(`../deploy/${name}`, import.meta.url), 'utf8'));
}

function validateTopology(document, isFullStack) {
  assert.deepEqual(Object.keys(document.services).sort(), isFullStack ? ['cpa', 'oh-my-cpa'] : ['oh-my-cpa']);
  const console = document.services['oh-my-cpa'];
  assert.equal(console.build, undefined);
  assert.equal(console.image, '${OMCPA_IMAGE:-wiziscool/oh-my-cpa:latest}');
  assert.deepEqual(console.ports, ['${OMCPA_BIND:-127.0.0.1:8080}:8080']);
  assert.equal(console.read_only, true);
  assert.equal(console.user, '10001:10001');
  assert.equal(console.environment.OMCPA_TRUSTED_PROXY_CIDRS, '${OMCPA_TRUSTED_PROXY_CIDRS:-}');
  assert.equal(console.environment.OMCPA_USAGE_INGEST_MODE, '${OMCPA_USAGE_INGEST_MODE:-auto}');
  assert.equal(console.environment.OMCPA_UPDATER_ENABLED, '${OMCPA_UPDATER_ENABLED:-false}');
  assert.equal(console.environment.OMCPA_VERSION, undefined);
  assert.equal(console.healthcheck, undefined, 'The image owns its canonical health probe');
  assert.equal(console.volumes.length, 1, 'Installers must not need to mount helper scripts');
  assert.ok(!JSON.stringify(document).includes('docker.sock'), 'Release Compose files do not start a socket-holding sidecar by default');
  if (isFullStack) {
    assert.equal(document.services.cpa.image, '${CPA_IMAGE:-eceasy/cli-proxy-api:latest}');
    assert.deepEqual(document.services.cpa.ports, ['${CPA_BIND:-127.0.0.1:8317}:8317']);
    assert.equal(console.depends_on.cpa.condition, 'service_healthy');
  } else {
    assert.deepEqual(console.extra_hosts, ['host.docker.internal:host-gateway']);
    assert.equal(document.networks.default.external, '${OMCPA_NETWORK_EXTERNAL:-false}');
  }
}

test('Compose files install latest images through loopback and preserve collection choice', () => {
  validateTopology(readDeployment('compose.full.yml'), true);
  validateTopology(readDeployment('compose.omc.yml'), false);
  const broken = readDeployment('compose.full.yml');
  broken.services['oh-my-cpa'].ports = ['0.0.0.0:8080:8080'];
  assert.throws(() => validateTopology(broken, true));
  const socketInConsole = readDeployment('compose.full.yml');
  socketInConsole.services['oh-my-cpa'].volumes = ['/var/run/docker.sock:/var/run/docker.sock'];
  assert.throws(() => validateTopology(socketInConsole, true));
  for (const service of ['oh-my-cpa', 'cpa']) {
    const pinned = readDeployment('compose.full.yml');
    pinned.services[service].image = pinned.services[service].image.replace(':latest', ':v0.1.0');
    assert.throws(() => validateTopology(pinned, true));
  }
});
test('new gateway bootstrap uses v8, enables usage and has no reusable client secret', () => {
  const config = readDeployment('cpa.config.example.yaml');
  assert.equal(config['config-version'], 8);
  assert.equal(config.server.port, 8317);
  assert.equal(config.management['allow-remote'], true);
  assert.equal(config.management['secret-key'], '');
  assert.deepEqual(config.access['api-keys'], []);
  assert.equal(config.observability.usage['usage-statistics-enabled'], true);
});

test('Dokploy source build keeps fork tags out of upstream release comparisons', () => {
  const document = readDeployment('dokploy/compose.yml');
  assert.deepEqual(Object.keys(document.services), ['omc']);
  const service = document.services.omc;
  assert.equal(service.build.context, '../..');
  assert.equal(service.build.dockerfile, 'Dockerfile');
  assert.equal(service.build.args.VERSION, 'fork');
  assert.equal(service.build.args.BUILD_TAG, '${OMCPA_BUILD_VERSION:-}');
  assert.equal(service.read_only, true);
  assert.equal(service.user, '10001:10001');
  assert.deepEqual(service.tmpfs, ['/tmp:size=256m,mode=1777']);
  assert.deepEqual(service.volumes, ['omc-data:/data']);
  assert.equal(document.volumes['omc-data'], null);
  assert.equal(service.environment.OMCPA_DATA_DIR, '/data');
  assert.equal(service.environment.OMCPA_QUOTA_SCHEDULER_MODE, '${OMCPA_QUOTA_SCHEDULER_MODE:-interval}');
  assert.equal(service.environment.OMCPA_QUOTA_SCHEDULER_ENABLED, '${OMCPA_QUOTA_SCHEDULER_ENABLED:-true}');
  assert.equal(service.environment.OMCPA_UPDATER_ENABLED, '${OMCPA_UPDATER_ENABLED:-false}');
  assert.equal(service.environment.OMCPA_MCP_READONLY_KEY, '${OMCPA_MCP_READONLY_KEY:-}');
  assert.equal(service.environment.OMCPA_ACCOUNT_AUTOMATION_ENABLED, '${OMCPA_ACCOUNT_AUTOMATION_ENABLED:-}');
  assert.equal(service.environment.OMCPA_FEISHU_ENABLED, '${OMCPA_FEISHU_ENABLED:-}');
  assert.equal(service.environment.OMCPA_FEISHU_APP_SECRET, '${OMCPA_FEISHU_APP_SECRET:-}');
  assert.equal(service.environment.OMCPA_OLLAMA_COLLECTOR_ENABLED, '${OMCPA_OLLAMA_COLLECTOR_ENABLED:-true}');
  assert.equal(service.environment.OMCPA_VERSION, undefined);
  assert.ok(!JSON.stringify(document).includes('docker.sock'));
});

test('Dokploy prebuilt entry inherits runtime security and has a Go-only reachable graph', () => {
  const source = readDeployment('dokploy/compose.yml');
  const prebuilt = readDeployment('dokploy/compose.prebuilt.yml');
  assert.deepEqual(prebuilt.services.omc.extends, { file: 'compose.yml', service: 'omc' });
  assert.deepEqual(prebuilt.services.omc.build, { context: '../..', dockerfile: 'Dockerfile.prebuilt' });
  assert.deepEqual(prebuilt.volumes, source.volumes);
  assert.deepEqual(prebuilt.networks, source.networks);
  assert.deepEqual(Object.keys(prebuilt.services.omc).sort(), ['build', 'extends']);
  const normal = fs.readFileSync(new URL('../Dockerfile', import.meta.url), 'utf8');
  const dockerfile = fs.readFileSync(new URL('../Dockerfile.prebuilt', import.meta.url), 'utf8');
  const bases = [...dockerfile.matchAll(/^FROM (?:--platform=\S+ )?(\S+)/gm)].map(match => match[1]);
  assert.deepEqual(bases, ['golang:1.27.1-alpine', 'alpine:3.21', 'alpine:3.21']);
  const copiedStages = [...dockerfile.matchAll(/--from=(\S+)/g)].map(match => match[1]);
  assert.ok(copiedStages.every(stage => ['runtime-data', 'server'].includes(stage)));
  assert.ok(!/\b(node|pnpm|vite)\b/i.test(dockerfile));
  assert.ok(dockerfile.indexOf('go run ./scripts/prebuilt-verify') < dockerfile.indexOf('go mod download'));
  assert.match(dockerfile, /CGO_ENABLED=0 GOOS=\$TARGETOS GOARCH=\$TARGETARCH go build/);
  assert.equal(dockerfile.slice(dockerfile.indexOf('# Certificates,')).trim(), normal.slice(normal.indexOf('# Certificates,')).replace('ARG VERSION=v0.1.0-dev', 'ARG VERSION=fork').trim());
  assert.match(normal, /COPY --from=web \/src\/web\/dist\//);
  const ignore = fs.readFileSync(new URL('../Dockerfile.prebuilt.dockerignore', import.meta.url), 'utf8');
  assert.ok(ignore.includes('!internal/**'));
  assert.ok(!ignore.includes('**/dist'));
  assert.ok(!ignore.includes('!.git/'));
});
