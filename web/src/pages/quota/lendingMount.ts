/**
 * Where the quota lending page is mounted, and how the shell reaches it.
 *
 * The address, the sidebar entry it highlights and its menu label live here instead of at each
 * call site: a navigation redesign can move the page into a new group by changing this module,
 * without touching the route table, the entry link, the detail link or the page itself.
 */
export const QUOTA_LENDING_PATH = '/quota-lending';

/**
 * The sidebar entry the page highlights. It is not a menu item of its own yet; when navigation
 * adds the planned capabilities group, only this value and the label key below change.
 */
export const QUOTA_LENDING_NAV_KEY = '/oauth-management';

/** The copy key for the page's short label, ready for the day it joins a menu. */
export const QUOTA_LENDING_NAV_LABEL_KEY = 'nav.quota_lending';

/** The back control's label; the control itself moves through browser history. */
export const QUOTA_LENDING_BACK_LABEL_KEY = 'common.back';

/**
 * The nav entry a pathname belongs under. Only the lending page remaps today; a page whose mount
 * moves later changes `QUOTA_LENDING_NAV_KEY` rather than the shell's matching rules.
 */
export function lendingNavKeyForPath(pathname: string): string {
  // The router matches `/quota-lending/` as well as the bare path, so a trailing slash must not
  // drop the sidebar back to its fallback. Every other pathname is returned untouched, so the
  // shell's matching rules for existing routes do not change.
  const normalized = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname;
  return normalized === QUOTA_LENDING_PATH ? QUOTA_LENDING_NAV_KEY : pathname;
}

/**
 * The router stores its position in `history.state.idx`. Index 0 (or no state at all, as on a
 * direct open) has nothing behind it, so the back control must not be shown.
 */
export function hasPreviousHistoryEntry(state: { idx?: unknown } | null | undefined): boolean {
  return typeof state?.idx === 'number' && state.idx > 0;
}
