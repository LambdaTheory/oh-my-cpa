import assert from 'node:assert/strict';
import test from 'node:test';

import { editorDocumentStartKey, editorShortcutKey } from './acceptance/shortcuts.mjs';

test('the editor find shortcut follows the platform binding', () => {
  // Monaco's "CtrlCmd" means Command on macOS and Control everywhere else. Pressing
  // Control+f on a Mac types a character instead of opening the find widget, which is
  // exactly how the config-source probe failed.
  assert.equal(editorShortcutKey('f', 'darwin'), 'Meta+f');
  assert.equal(editorShortcutKey('f', 'linux'), 'Control+f');
  assert.equal(editorShortcutKey('f', 'win32'), 'Control+f');
  assert.equal(editorShortcutKey('f', 'freebsd'), 'Control+f');
  assert.equal(editorShortcutKey('f'), process.platform === 'darwin' ? 'Meta+f' : 'Control+f');
});

test('undo and redo follow the same platform modifier', () => {
  assert.equal(editorShortcutKey('z', 'darwin'), 'Meta+z');
  assert.equal(editorShortcutKey('Shift+z', 'darwin'), 'Meta+Shift+z');
  assert.equal(editorShortcutKey('z', 'linux'), 'Control+z');
  assert.equal(editorShortcutKey('Shift+z', 'linux'), 'Control+Shift+z');
});

test('document start uses the platform key rather than a fixed Control+Home', () => {
  assert.equal(editorDocumentStartKey('darwin'), 'Meta+ArrowUp');
  assert.equal(editorDocumentStartKey('linux'), 'Control+Home');
  assert.equal(editorDocumentStartKey('win32'), 'Control+Home');
  assert.equal(editorDocumentStartKey(), process.platform === 'darwin' ? 'Meta+ArrowUp' : 'Control+Home');
});
