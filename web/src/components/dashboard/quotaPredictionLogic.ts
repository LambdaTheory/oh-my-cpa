import type { QuotaPredictionItem } from '../../types/quotaPrediction';

const DAY_MS = 24 * 60 * 60 * 1000;

export type PredictionSufficiency = 'enough' | 'insufficient' | 'unknown';

export function assessPrediction(item: QuotaPredictionItem, generatedAtMS: number) {
  const hasForecast = item.verdict !== 'unknown' && item.used_percent !== null
    && Number.isFinite(item.used_percent) && item.used_percent >= 0 && item.used_percent <= 100
    && item.reset_at_ms !== null && item.reset_at_ms > generatedAtMS
    && !item.reasons.some((reason) => ['stale_reading', 'reading_error', 'cycle_expired'].includes(reason));
  const untilReset: PredictionSufficiency = !hasForecast ? 'unknown'
    : item.verdict === 'will_exhaust' ? 'insufficient' : 'enough';
  const isUrgent = hasForecast && item.verdict === 'will_exhaust'
    && item.exhaustion_eta_ms !== null && item.exhaustion_eta_ms <= generatedAtMS + DAY_MS;
  const today: PredictionSufficiency = !hasForecast ? 'unknown'
    : item.verdict === 'will_exhaust' && item.exhaustion_eta_ms === null ? 'unknown'
      : isUrgent ? 'insufficient' : 'enough';
  return { untilReset, today, isUrgent };
}

export function summarizePredictions(items: QuotaPredictionItem[], generatedAtMS: number, hasMissingData = false) {
  const assessments = items.map((item) => assessPrediction(item, generatedAtMS));
  const summarize = (key: 'today' | 'untilReset'): PredictionSufficiency => {
    if (assessments.some((assessment) => assessment[key] === 'insufficient')) return 'insufficient';
    if (hasMissingData || assessments.length === 0 || assessments.some((assessment) => assessment[key] === 'unknown')) return 'unknown';
    return 'enough';
  };
  return { today: summarize('today'), untilReset: summarize('untilReset') };
}
