import React from 'react';
import { useT } from '../../i18n';
import type { NormalizedQuota } from '../../types/quota';
import { readOllamaCredits } from './ollamaCredits';
import styles from './OllamaCreditsView.module.css';

export const OllamaCreditsView: React.FC<{ quota?: NormalizedQuota | null; status?: string }> = ({ quota, status }) => {
  const t = useT();
  const credits = readOllamaCredits(quota, status);
  const formatAmount = (cents: number | null | undefined, zeroKey: string) => cents == null
    ? t('ollama.data_missing') : cents === 0 ? t(zeroKey) : `$${(cents / 100).toFixed(2)}`;
  const percent = credits?.usedPercent;
  return <div className={styles['credits']} data-testid="ollama-credits">
    {quota?.plan?.plan_label && <span>{quota.plan.plan_label}</span>}
    <span>{t('ollama.credits_remaining')}: <strong className="mono-num">{formatAmount(credits?.remaining, 'ollama.credits_empty')}</strong></span>
    <span>{t('ollama.credits_used')}: <strong className="mono-num">{formatAmount(credits?.used, 'ollama.credits_unused')}</strong></span>
    {credits?.total != null && credits.total > 0 && <span>{t('ollama.credits_total')}: {formatAmount(credits.total, 'ollama.credits_empty')}</span>}
    {percent != null && <span>{percent < 0.01 ? '<0.01' : Number(percent.toPrecision(3))}% · {t('ollama.credits_calculated')}</span>}
    {credits?.refillText && <span>{credits.refillText}</span>}
    <span>{t('ollama.models_month')}: {credits?.models?.length ? credits.models.map((entry) =>
      <span className={styles['model']} key={entry.model}>{entry.model} · {t('ollama.model_requests', { count: entry.requests })}</span>,
    ) : t('ollama.data_missing')}</span>
  </div>;
};
