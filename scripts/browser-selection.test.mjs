import assert from 'node:assert/strict';
import test from 'node:test';
import { parseBrowserSelection } from './acceptance/browser-selection.mjs';

test('default retains the full browser catalog', () => {
  assert.deepEqual(parseBrowserSelection([]), { smokeOnly: false, p0Only: false, scenarioId: undefined });
  assert.deepEqual(parseBrowserSelection(['--smoke']), { smokeOnly: true, p0Only: false, scenarioId: undefined });
  assert.deepEqual(parseBrowserSelection(['--p0']), { smokeOnly: false, p0Only: true, scenarioId: undefined });
});

test('the named YAML case is the only selective built acceptance', () => {
  assert.deepEqual(parseBrowserSelection(['--scenario', 'config-yaml-format']), {
    smokeOnly: false, p0Only: false, scenarioId: 'config-yaml-format',
  });
  for (const args of [
    ['--scenario'], ['--scenario', ''], ['--scenario', 'unknown'],
    ['--scenario', 'config-yaml-format', '--scenario', 'config-yaml-format'],
    ['--scenario', 'config-yaml-format', '--smoke'],
    ['--p0', '--scenario', 'config-yaml-format'],
    ['--unknown'], ['--smoke', '--smoke'],
  ]) assert.throws(() => parseBrowserSelection(args));
});
