import assert from 'node:assert/strict';
import test from 'node:test';
import { credentialLabel } from '../web/src/utils/credentialLabel.ts';

test('credential labels remove known opaque prefixes but preserve ordinary names', () => {
  for (const value of ['index-7: synthetic@example.test', 'index-7 | synthetic@example.test', 'index-7 - synthetic@example.test']) {
    assert.equal(credentialLabel(value, ['index-7']), 'synthetic@example.test');
  }
  assert.equal(credentialLabel('ollama:opaque-7: Legacy account', ['ollama:opaque-7', 'opaque-7']), 'Legacy account');
  assert.equal(credentialLabel('12345678-1234-1234-1234-123456789abc - Account', []), 'Account');
  assert.equal(credentialLabel('opaque-7', ['opaque-7'], 'Ollama account'), 'Ollama account');
  assert.equal(credentialLabel('index-70: friendly', ['index-7']), 'index-70: friendly');
  assert.equal(credentialLabel('Friendly index-7', ['index-7']), 'Friendly index-7');
});
