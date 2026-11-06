import React from 'react';
import { useT } from '../../i18n';
import type { NormalizedQuota } from '../../types/quota';
import { readOllamaCredits } from './ollamaCredits';
import styles from './OllamaCreditsView.module.css';

/**
 * Ollama's credits reading. A missing amount is written as `—` and an unpublished total as
 * "not reported upstream", never as 0: a zero the upstream stated and a zero the console filled
 * in read the same in a table, and only one of them is true.
 */
export const OllamaCreditsView: React.FC<{ quota?: NormalizedQuota | null; status?: string }> = ({ quota, status }) => {
  const t = useT();
  const credits = readOllamaCredits(quota, status);
  const formatAmount = (cents: number | null | undefined, zeroKey: string) => cents == null
    ? '—' : cents === 0 ? t(zeroKey) : `$${(cents / 100).toFixed(2)}`;
  const percent = credits?.usedPercent;
  const percentText = percent == null
    ? t('ollama.credits_share_unavailable')
    : `${percent > 0 && percent < 0.01 ? '<0.01' : Number(percent.toPrecision(3))}% · ${t('ollama.credits_calculated')}`;
  if (!credits) {
    return <div className={styles['credits']} data-testid="ollama-credits">
      <span>{t('ollama.credits_no_reading')}</span>
    </div>;
  }
  return <div className={styles['credits']} data-testid="ollama-credits">
    {quota?.plan?.plan_label && <span>{quota.plan.plan_label}</span>}
    <span>{t('ollama.credits_remaining')}: <strong className="mono-num">{formatAmount(credits.remaining, 'ollama.credits_empty')}</strong></span>
    <span>{t('ollama.credits_used')}: <strong className="mono-num">{formatAmount(credits.used, 'ollama.credits_unused')}</strong></span>
    <span>
      {t('ollama.credits_total')}: <strong className="mono-num">{credits.total == null
        ? t('ollama.credits_total_unreported')
        : formatAmount(credits.total, 'ollama.credits_empty')}</strong>
    </span>
    <span>{t('ollama.credits_share')}: {percentText}</span>
    {credits.refillText && <span>{credits.refillText}</span>}
    <span>{t('ollama.models_month')}: {credits.models?.length ? credits.models.map((entry) =>
      <span className={styles['model']} key={entry.model}>{entry.model} · {t('ollama.model_requests', { count: entry.requests })}</span>,
    ) : t('ollama.unreported')}</span>
  </div>;
};
