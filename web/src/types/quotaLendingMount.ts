/**
 * Where the quota lending page is mounted.
 *
 * The address, the menu label and the back label live here instead of at each call site, so the
 * route table, the entry links, the page and the navigation entry all share one definition: a
 * navigation redesign changes which group the entry belongs to without touching the page itself.
 */
export const QUOTA_LENDING_PATH = '/quota-lending';

/** The copy key for the page's short menu label. */
export const QUOTA_LENDING_NAV_LABEL_KEY = 'nav.quota_lending';

/** The back control's label; the control itself moves through browser history. */
export const QUOTA_LENDING_BACK_LABEL_KEY = 'common.back';

/**
 * The router stores its position in `history.state.idx`. Index 0 (or no state at all, as on a
 * direct open) has nothing behind it, so the back control must not be shown.
 */
export function hasPreviousHistoryEntry(state: { idx?: unknown } | null | undefined): boolean {
  return typeof state?.idx === 'number' && state.idx > 0;
}
