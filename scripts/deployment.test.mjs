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
  const service = readDeployment('dokploy/compose.yml').services.omc;
  assert.equal(service.build.context, '../..');
  assert.equal(service.build.dockerfile, 'Dockerfile');
  assert.equal(service.build.args.VERSION, 'fork');
  assert.equal(service.build.args.BUILD_TAG, '${OMCPA_BUILD_VERSION:-}');
  assert.equal(service.environment.OMCPA_VERSION, undefined);
});
