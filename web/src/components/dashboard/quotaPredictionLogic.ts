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
  const untilReset: PredictionSufficiency = isLow || willExhaust ? 'insufficient' : hasForecast ? 'enough' : 'unknown';
  const isUrgent = isLow || (willExhaust && item.exhaustion_eta_ms !== null
    && item.exhaustion_eta_ms <= generatedAtMS + DAY_MS);
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

export function summarizePredictions(items: DashboardPredictionItem[], generatedAtMS: number, hasMissingData = false) {
  const assessments = items.map((item) => assessPrediction(item, generatedAtMS));
  const summarize = (key: 'today' | 'untilReset'): PredictionSufficiency => {
    if (assessments.some((assessment) => assessment[key] === 'insufficient')) return 'insufficient';
    if (hasMissingData || assessments.length === 0 || assessments.some((assessment) => assessment[key] === 'unknown')) return 'unknown';
    return 'enough';
  };
  return { today: summarize('today'), untilReset: summarize('untilReset') };
}
