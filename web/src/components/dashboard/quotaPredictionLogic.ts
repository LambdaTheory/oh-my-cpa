import type { DashboardPredictionItem } from '../../types/quotaPrediction';

const DAY_MS = 24 * 60 * 60 * 1000;
const INVALID_READING_REASONS = new Set(['stale_reading', 'reading_error', 'cycle_expired', 'invalid_used_percent']);

export type PredictionSufficiency = 'enough' | 'insufficient' | 'unknown';
export type PredictionFailure = 'reading_failed' | 'history_insufficient' | 'cycle_unknown' | 'usage_abnormal';

export function assessPrediction(item: DashboardPredictionItem, generatedAtMS: number) {
  const hasReading = item.used_percent !== null && Number.isFinite(item.used_percent)
    && item.used_percent >= 0 && item.used_percent <= 100
    && (item.reset_at_ms === null || item.reset_at_ms > generatedAtMS)
    && !item.reasons.some((reason) => INVALID_READING_REASONS.has(reason));
  const remainingPercent = hasReading ? 100 - item.used_percent! : null;
  const isLow = remainingPercent !== null && remainingPercent <= 10;
  const hasForecast = hasReading && item.verdict !== 'unknown'
    && item.reset_at_ms !== null && item.reset_at_ms > generatedAtMS;
  const willExhaust = hasForecast && item.verdict === 'will_exhaust';
  // The risk threshold is solely "can this last until the reset" (浩文 2026-11-05).
  // When the backend states it, that answer wins; the older local heuristic stays
  // as the fallback for responses that predate the field.
  const stated = item.lasts_until_reset;
  const untilReset: PredictionSufficiency = stated === false ? 'insufficient'
    : stated === true ? (hasReading ? 'enough' : 'unknown')
      : stated === null ? 'unknown'
        : isLow || willExhaust ? 'insufficient' : hasForecast ? 'enough' : 'unknown';
  // Urgency must agree with the risk threshold above: a window the backend says
  // lasts until its reset is never "will exhaust soon", even when headroom is low.
  const isUrgent = untilReset === 'insufficient' && (isLow || (willExhaust && item.exhaustion_eta_ms !== null
    && item.exhaustion_eta_ms <= generatedAtMS + DAY_MS));
  const today: PredictionSufficiency = isUrgent ? 'insufficient' : !hasForecast
    || (willExhaust && item.exhaustion_eta_ms === null) ? 'unknown' : 'enough';
  const enduranceMS = hasForecast && item.exhaustion_eta_ms !== null && item.exhaustion_eta_ms < item.reset_at_ms!
    ? Math.max(0, item.exhaustion_eta_ms - generatedAtMS) : null;
  return { untilReset, today, isUrgent, isLow, hasForecast, remainingPercent, enduranceMS };
}

export function classifyPredictionFailure(item: DashboardPredictionItem, generatedAtMS: number): PredictionFailure | null {
  const reasons = item.reasons;
  if (reasons.some((reason) => ['reading_error', 'stale_reading', 'no_reading'].includes(reason))) return 'reading_failed';
  if (reasons.some((reason) => ['mid_cycle_reset', 'invalid_used_percent', 'invalid_observation', 'arithmetic_unsafe', 'scope_unknown'].includes(reason))
    || (item.used_percent != null && (!Number.isFinite(item.used_percent) || item.used_percent < 0 || item.used_percent > 100))) return 'usage_abnormal';
  if (item.used_percent == null) return 'reading_failed';
  if (reasons.some((reason) => ['boundary_unknown', 'cycle_expired'].includes(reason))
    || item.reset_at_ms === null || item.reset_at_ms <= generatedAtMS) return 'cycle_unknown';
  return item.verdict === 'unknown' ? 'history_insufficient' : null;
}

export type PredictionDeviationDirection = 'ahead' | 'behind' | 'even';

/**
 * The gap between actual and expected usage in percentage points. Ahead means
 * the credential is burning faster than the time-linear pace; behind means it is
 * slower. Null when either side is unknown, so the row never invents a gap.
 */
export function predictionDeviation(item: DashboardPredictionItem): { direction: PredictionDeviationDirection; points: number } | null {
  const actual = item.used_percent;
  const expected = item.expected_used_percent;
  if (actual == null || expected == null || !Number.isFinite(actual) || !Number.isFinite(expected)) return null;
  const delta = actual - expected;
  if (Math.abs(delta) < 0.05) return { direction: 'even', points: 0 };
  return { direction: delta > 0 ? 'ahead' : 'behind', points: Math.abs(delta) };
}

/**
 * What the Feishu push leaves out. The three buckets are disjoint: a stale
 * reading is reported as stale rather than also counting as unknown, so the
 * footer numbers add up to the rows the console shows.
 */
export function summarizeUnpushed(items: DashboardPredictionItem[], generatedAtMS: number) {
  let lasts = 0;
  let unknown = 0;
  let stale = 0;
  for (const item of items) {
    if (item.reasons.includes('stale_reading')) {
      stale += 1;
      continue;
    }
    const assessment = assessPrediction(item, generatedAtMS);
    if (assessment.untilReset === 'enough') lasts += 1;
    else if (assessment.untilReset === 'unknown') unknown += 1;
  }
  return { lasts, unknown, stale };
}

export function summarizePredictions(items: DashboardPredictionItem[], generatedAtMS: number, hasMissingData = false) {
  const assessments = items.map((item) => assessPrediction(item, generatedAtMS));
  const summarize = (key: 'today' | 'untilReset'): PredictionSufficiency => {
    if (assessments.some((assessment) => assessment[key] === 'insufficient')) return 'insufficient';
    if (hasMissingData || assessments.length === 0 || assessments.some((assessment) => assessment[key] === 'unknown')) return 'unknown';
    return 'enough';
  };
  return { today: summarize('today'), untilReset: summarize('untilReset') };
}
