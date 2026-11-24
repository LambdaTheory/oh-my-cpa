import type { QuotaWindow } from '../../types/quota';
import { quotaRemainingPercent } from './quotaFormat';

/** How many windows a list row shows before the rest need the Drawer. */
export const COMPACT_QUOTA_WINDOW_COUNT = 2;

const HOUR_MS = 3_600_000;

/**
 * A window the lending rules compare: the credential's own plan, a day or longer.
 *
 * Shorter windows reset too quickly to say anything about stretching an allowance to the reset,
 * and rolling or credit windows have no fixed cycle to compare. The backend's `LongStandardWindow`
 * uses the same rule; the display layer must not drift from it.
 */
export function isLongStandardWindow(window: QuotaWindow): boolean {
  if (window.scope !== 'standard') return false;
  if (window.kind === 'rolling' || window.kind === 'credit_usage') return false;
  return window.period_hours != null && window.period_hours >= 24;
}

/** The reset sort's key: the soonest reset among a credential's long windows. */
export interface EarliestLongWindow {
  resetAtMS: number;
  remainingPercent?: number;
}

function compareNullablePercent(left?: number, right?: number): number {
  if (left == null && right == null) return 0;
  if (left == null) return 1;
  if (right == null) return -1;
  return left - right;
}

/**
 * The soonest natural reset among a credential's long windows, with that window's remaining share.
 *
 * A credential with no long window, or none that states a reset instant, has no key and sinks to
 * the end of the reset sort. A short window never decides the order, even when its boundary is the
 * next one on the credential: the row may carry it, but the comparison is about the long cycle.
 */
export function earliestLongWindow(windows: QuotaWindow[]): EarliestLongWindow | null {
  const candidates = windows.filter((window) => isLongStandardWindow(window) && window.reset_at_ms != null);
  if (candidates.length === 0) return null;
  candidates.sort((left, right) => (
    (left.reset_at_ms ?? 0) - (right.reset_at_ms ?? 0)
      || compareNullablePercent(quotaRemainingPercent(left), quotaRemainingPercent(right))
      || left.id.localeCompare(right.id)
  ));
  const chosen = candidates[0];
  return { resetAtMS: chosen.reset_at_ms ?? 0, remainingPercent: quotaRemainingPercent(chosen) };
}

/**
 * Whether a window is close enough to its own reset to be read as "fast reset".
 *
 * The period is what makes the distance meaningful, so a window without one is never marked. A
 * boundary already in the past is not fast either: that window has reset, and marking it would
 * describe a state it is no longer in.
 */
export function isFastResetWindow(
  window: Pick<QuotaWindow, 'period_hours' | 'reset_at_ms'>,
  nowMS: number,
): boolean {
  if (window.period_hours == null || window.period_hours <= 0 || window.reset_at_ms == null) return false;
  const remainingMS = window.reset_at_ms - nowMS;
  return remainingMS > 0 && remainingMS <= (window.period_hours * HOUR_MS) / 3;
}

/**
 * The remaining-share half of the fast-reset mark: `high` from 50%, `low` below it.
 *
 * `null` means no share is stated, so the caller prints the plain "fast reset" mark rather than
 * inventing a level; it also covers a window that is not fast at all.
 */
export function quotaFastResetState(
  window: Pick<QuotaWindow, 'period_hours' | 'reset_at_ms' | 'remaining_percent' | 'used_percent'>,
  nowMS: number,
): 'high' | 'low' | null {
  if (!isFastResetWindow(window, nowMS)) return null;
  // The 50% line is exact: `quotaRemainingPercent` rounds for the progress bar, which would turn
  // 49.9% into 50% and label a low window as high.
  const share = window.remaining_percent ?? (window.used_percent != null ? 100 - window.used_percent : undefined);
  const remaining = share == null || !Number.isFinite(share)
    ? undefined
    : Math.max(0, Math.min(100, share));
  if (remaining == null) return null;
  return remaining >= 50 ? 'high' : 'low';
}

/**
 * Which window a reading describes.
 *
 * Providers that publish named model groups (Antigravity) send no `kind`, only a label such as
 * "Gemini Models · Five Hour Limit Remaining" and the period it covers. The period is the
 * language-independent fact, so a row can name the window in the reader's own language instead
 * of repeating the provider's label, and ordering stops depending on the source sequence. A
 * period that matches no known bucket stays unnamed rather than being forced into one.
 */
export function quotaWindowKindOf(window: QuotaWindow): QuotaWindow['kind'] {
  if (window.kind) return window.kind;
  const hours = window.period_hours;
  if (hours == null) return undefined;
  if (Math.abs(hours - 5) < 1) return 'five_hour';
  if (Math.abs(hours - 24) < 1) return 'daily';
  if (Math.abs(hours - 168) < 1) return 'weekly';
  if (hours >= 24 * 28 && hours <= 24 * 31) return 'monthly';
  return undefined;
}

/**
 * Shortest window first, so a reading leads with the limit that binds soonest. A provider
 * that publishes a daily limit beside its weekly one (Devin) therefore reads "daily, then
 * weekly" in both the row and the Drawer.
 */
function windowRank(kind?: string): number {
  if (kind === 'five_hour') return 0;
  if (kind === 'daily') return 1;
  if (kind === 'weekly') return 2;
  if (kind === 'monthly') return 3;
  return 4;
}

/**
 * The unit a window belongs to: one model group, one model's own limit, or the credential's
 * plan.
 *
 * A model-scoped window is keyed by the model it meters rather than by its label: the label
 * carries the period, so the same model limited over five hours and over a week would
 * otherwise be two families, and the row and the Drawer would be picking from different sets.
 * Every scope that is not the credential's own plan is its own family, including a limit
 * reported outside the plan's windows (a code-review allowance is not part of the five-hour
 * and weekly limits it sits beside). Keying a scope by its window id as well would make the
 * code-review allowance's two windows two families of one window each, so a credential whose
 * only limits were those would show one of them and hide the other.
 */
export function quotaWindowGroupKey(window: QuotaWindow): string {
  if (window.scope === 'group') return `group:${window.label.split(' · ')[0]}`;
  if (window.scope === 'model') return `model:${window.model || window.label}`;
  if (window.scope === 'standard') return 'standard';
  return window.scope;
}

/** Source order, one group at a time, and within a group the shorter window first. */
export function orderQuotaWindows(windows: QuotaWindow[]): QuotaWindow[] {
  const groups = new Map<string, number>();
  for (const window of windows) {
    const key = quotaWindowGroupKey(window);
    if (!groups.has(key)) groups.set(key, groups.size);
  }
  return [...windows].sort((left, right) => (
    groups.get(quotaWindowGroupKey(left))! - groups.get(quotaWindowGroupKey(right))!
      || windowRank(quotaWindowKindOf(left)) - windowRank(quotaWindowKindOf(right))
  ));
}

/**
 * The pair a list row shows.
 *
 * A row answers "can this credential serve the next request", so it carries the
 * credential's own model family - the first group the provider reports, which is the one
 * named after it - and takes that family's two shortest limits, in the same
 * shortest-first order the Drawer uses. Every other family the same endpoint publishes
 * (Antigravity answers with Claude and GPT groups beside Gemini) stays in the Drawer's
 * quota tab rather than crowding the row. A family that publishes fewer than two windows
 * is filled from what it did return; a family whose windows carry no kind keeps the order
 * its provider sent.
 */
export function pickCompactQuotaWindows(windows: QuotaWindow[]): QuotaWindow[] {
  const primary = windows[0];
  if (!primary) return [];
  const familyKey = quotaWindowGroupKey(primary);
  const family: QuotaWindow[] = [];
  const seenIds = new Set<string>();
  for (const window of windows) {
    if (quotaWindowGroupKey(window) !== familyKey) continue;
    // A provider can repeat a bucket, and a row must not spend one of its two lines on a
    // duplicate of the other. Two readings of the same window id are one window - deduped by
    // id, not by object - while distinct windows that share a kind or a group keep both lines.
    if (window.id) {
      if (seenIds.has(window.id)) continue;
      seenIds.add(window.id);
    } else if (family.includes(window)) {
      continue;
    }
    family.push(window);
  }
  return [...family]
    .sort((left, right) => windowRank(quotaWindowKindOf(left)) - windowRank(quotaWindowKindOf(right)))
    .slice(0, COMPACT_QUOTA_WINDOW_COUNT);
}
