/**
 * Keyboard shortcuts whose modifier differs by platform.
 *
 * Monaco binds editor commands to "CtrlCmd", which is Command on macOS and Control
 * elsewhere. A probe that presses `Control+f` on a Mac types an "f" into the editor
 * instead of opening the find widget, so the binding is chosen from the platform
 * rather than guessed in one place.
 */

/** The key spec Playwright should press for a Monaco CtrlCmd binding. */
export function editorShortcutKey(key, platform = process.platform) {
  return `${platform === 'darwin' ? 'Meta' : 'Control'}+${key}`;
}

/**
 * Document start is the one Monaco command whose key itself differs, not just the
 * modifier: macOS binds it to Command+Up rather than Control+Home. The project's
 * built-SPA config scenario already presses it that way.
 */
export function editorDocumentStartKey(platform = process.platform) {
  return platform === 'darwin' ? 'Meta+ArrowUp' : 'Control+Home';
}
