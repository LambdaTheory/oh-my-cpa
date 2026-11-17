import type { TFunc } from '../../i18n';
import type { DashboardPredictionItem, QuotaPredictionUnavailableReason } from '../../types/quotaPrediction';

const HOUR_MS = 60 * 60 * 1000;

export type PredictionLasts = 'yes' | 'no' | 'unknown';

/**
 * How the panel presents a row. A rolling window has no fixed cycle, so its
 * current share is a reading, not a risk conclusion. An idle window was read
 * successfully but recorded neither use nor traffic. A stale window is folded
 * out of the main list; the credential page still explains it.
 */
export type PredictionRowKind = 'risk' | 'rolling' | 'idle' | 'stale';

/** The three-state answer a row states, never derived from the deprecated verdict. */
export function predictionLasts(item: DashboardPredictionItem): PredictionLasts {
  if (item.lasts_until_reset === true) return 'yes';
  if (item.lasts_until_reset === false) return 'no';
  return 'unknown';
}

export function isRollingWindow(item: DashboardPredictionItem): boolean {
  return item.is_rolling_window === true;
}

/**
 * A zero reading only proves idleness together with the backend's cycle usage.
 * `usage_requests === undefined` means usage was not read, so the row must not
 * be treated as idle on the percentage alone.
 */
export function isIdleWindow(item: DashboardPredictionItem): boolean {
  return !isStaleReading(item)
    && item.used_percent !== null
    && item.used_percent === 0
    && item.usage_requests === 0;
}

export function predictionRowKind(item: DashboardPredictionItem): PredictionRowKind {
  if (isStaleReading(item)) return 'stale';
  if (isIdleWindow(item)) return 'idle';
  if (isRollingWindow(item)) return 'rolling';
  return 'risk';
}

export function partitionPredictions(items: DashboardPredictionItem[]): Record<PredictionRowKind, DashboardPredictionItem[]> {
  const result: Record<PredictionRowKind, DashboardPredictionItem[]> = { risk: [], rolling: [], idle: [], stale: [] };
  for (const item of items) result[predictionRowKind(item)].push(item);
  return result;
}

export function isExhausted(item: DashboardPredictionItem): boolean {
  return !isStaleReading(item) && item.used_percent !== null && Number.isFinite(item.used_percent) && item.used_percent >= 100;
}

/**
 * The reading's own age, shown as "expired" only when the server marked it so.
 * The client must not invent its own freshness rule: the server owns the
 * window-length-dependent threshold.
 */
export function isStaleReading(item: DashboardPredictionItem): boolean {
  return item.reading_stale === true;
}

/** Actual minus expected, in points, when both percentages are known. */
export function predictionDeviation(item: DashboardPredictionItem): number | null {
  if (item.used_percent === null || item.expected_used_percent === null) return null;
  if (!Number.isFinite(item.used_percent) || !Number.isFinite(item.expected_used_percent)) return null;
  return Math.round((item.used_percent - item.expected_used_percent) * 10) / 10;
}

/**
 * What the Feishu push leaves out. Stale rows have their own bucket instead of
 * also counting as unknown, so the footer counts stay disjoint. Rolling and
 * idle rows are not risk findings, so they belong to no bucket.
 */
export function summarizeUnpushed(items: DashboardPredictionItem[]) {
  let lasts = 0;
  let unknown = 0;
  let stale = 0;
  for (const item of items) {
    const kind = predictionRowKind(item);
    if (kind === 'stale') {
      stale += 1;
      continue;
    }
    if (kind !== 'risk') continue;
    const answer = predictionLasts(item);
    if (answer === 'yes') lasts += 1;
    else if (answer === 'unknown') unknown += 1;
  }
  return { lasts, unknown, stale };
}

/**
 * Sort by how soon the window runs out: already spent, then past its ETA, then
 * an ETA before the reset, then "will not exhaust", then rows that cannot be
 * estimated. Ties keep a stable identity order.
 */
export function predictionRank(item: DashboardPredictionItem, generatedAtMS: number): number {
  if (isExhausted(item)) return 0;
  if (item.exhaustion_eta_ms !== null && item.exhaustion_eta_ms <= generatedAtMS) return 1;
  if (item.lasts_until_reset === false) return 2;
  if (item.lasts_until_reset === true) return 3;
  return 4;
}

export function sortPredictions(items: DashboardPredictionItem[], generatedAtMS: number): DashboardPredictionItem[] {
  return [...items].sort((left, right) => {
    const rank = predictionRank(left, generatedAtMS) - predictionRank(right, generatedAtMS);
    if (rank !== 0) return rank;
    const leftETA = left.exhaustion_eta_ms ?? Number.POSITIVE_INFINITY;
    const rightETA = right.exhaustion_eta_ms ?? Number.POSITIVE_INFINITY;
    if (leftETA !== rightETA) return leftETA - rightETA;
    return left.credential_label.localeCompare(right.credential_label)
      || left.window_id.localeCompare(right.window_id);
  });
}

/**
 * Window counts for the panel summary. Collapsed rows (stale, idle) and rows
 * that are only a current reading (rolling) are not risk conclusions.
 */
export function summarizePredictions(items: DashboardPredictionItem[]): { cannot: number; unknown: number } {
  let cannot = 0;
  let unknown = 0;
  for (const item of items) {
    if (predictionRowKind(item) !== 'risk') continue;
    const lasts = predictionLasts(item);
    if (lasts === 'no') cannot += 1;
    else if (lasts === 'unknown') unknown += 1;
  }
  return { cannot, unknown };
}

/**
 * How many distinct credentials the group covers. A credential can report more
 * than one window, so counting rows would overstate it; the group header counts
 * credentials and the rows underneath count windows.
 */
export function countCredentials(items: DashboardPredictionItem[]): number {
  return new Set(items.map((item) => item.auth_index || item.credential_label)).size;
}

/**
 * The copy key for the one-line conclusion. It carries only what the row can
 * state: exhaustion and whether the window lasts; percentages live in the
 * numbers above the sentence.
 */
export function predictionConclusion(item: DashboardPredictionItem, nowMS: number): { key: string; vars?: Record<string, string | number> } {
  if (isExhausted(item)) return { key: 'dash.prediction.line_exhausted' };
  if (isStaleReading(item)) return { key: 'dash.prediction.line_expired' };
  if (isRollingWindow(item)) return { key: 'dash.prediction.line_rolling' };
  const lasts = predictionLasts(item);
  if (lasts === 'no') {
    const eta = item.exhaustion_eta_ms;
    return eta !== null && eta > nowMS
      ? { key: 'dash.prediction.line_cannot', vars: { time: formatInstant(eta) } }
      : { key: 'dash.prediction.line_cannot_past' };
  }
  if (lasts === 'yes') return { key: 'dash.prediction.line_can' };
  return { key: `dash.prediction.unknown.${item.unavailable_reason || 'usage_abnormal'}` };
}

export function predictionUnavailableKey(reason: QuotaPredictionUnavailableReason | undefined): string {
  return `dash.prediction.unknown.${reason || 'usage_abnormal'}`;
}

/** ETA copy: a concrete instant, "will not exhaust", or "cannot be estimated". */
export function formatPredictionETA(item: DashboardPredictionItem, t: TFunc): string {
  if (item.exhaustion_eta_ms !== null && item.exhaustion_eta_ms > 0) {
    return t('dash.prediction.eta_at', { time: formatInstant(item.exhaustion_eta_ms) });
  }
  if (predictionLasts(item) === 'yes') return t('dash.prediction.eta_none');
  return t('dash.prediction.eta_unknown');
}

export function formatPredictionPercent(value: number | null | undefined, t: TFunc): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return t('dash.prediction.cannot_calculate');
  if (value > 0 && value < 0.1) return '<0.1%';
  return `${value.toFixed(1)}%`;
}

export function formatPredictionDeviation(item: DashboardPredictionItem, t: TFunc): string {
  const deviation = predictionDeviation(item);
  if (deviation === null) return t('dash.prediction.deviation_unknown');
  if (deviation > 0.05) return t('dash.prediction.deviation_over', { n: Math.abs(deviation).toFixed(1) });
  if (deviation < -0.05) return t('dash.prediction.deviation_under', { n: Math.abs(deviation).toFixed(1) });
  return t('dash.prediction.deviation_even');
}

function formatInstant(value: number): string {
  const date = new Date(value);
  const pad = (number: number) => String(number).padStart(2, '0');
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** Hours a window still has, used only for the diagnostics sample line. */
export function predictionCoverageHours(item: DashboardPredictionItem): number | null {
  return item.coverage_seconds == null ? null : Math.round((item.coverage_seconds / (HOUR_MS / 1000)) * 10) / 10;
}
