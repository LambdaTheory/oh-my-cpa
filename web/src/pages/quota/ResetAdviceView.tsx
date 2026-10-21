import { useT } from '../../i18n';
import type { ResetAdvice } from '../../types/resetAdvice';
import { Tag } from '../../components/common/Tag';
import { Panel } from '../../components/common/Panel';
import { formatTimeWithCountdown } from './quotaFormat';
import styles from './ResetAdviceView.module.css';

export function ResetAdviceTag({ advice }: { advice?: ResetAdvice }) {
  const t = useT();
  if (!advice || advice.verdict === 'unknown') return null;
  return <Tag tone={advice.verdict === 'use_before_expiry' ? 'accent' : 'neutral'} data-reset-advice={advice.verdict}>
    {t(`advice.verdict.${advice.verdict}`)}
  </Tag>;
}

export function ResetAdviceView({ advice, nowMS }: { advice: ResetAdvice; nowMS: number }) {
  const t = useT();
  const time = (value: number | null) => value === null || value <= 0 ? t('ann.unknown_time') : formatTimeWithCountdown(value, nowMS, t);
  return <Panel title={t('advice.title')} className={styles.panel} data-reset-advice-detail="true">
    <div className={styles.meta}>
      {advice.verdict === 'unknown' ? <span>{t('advice.verdict.unknown')}</span> : <ResetAdviceTag advice={advice} />}
      <span>{t(`advice.evidence.${advice.evidence}`)}</span>
    </div>
    <ul className={styles.reasons}>{advice.reasons.map(reason => <li key={reason}>{t(`advice.reason.${reason}`)}</li>)}</ul>
    <section className={styles.reading} aria-label={t('advice.local')}>
      <strong>{t('advice.local')}</strong>
      <div>{t(`advice.verdict.${advice.local_evidence.verdict}`)} · {t(`advice.evidence.${advice.local_evidence.evidence}`)}</div>
      <div>{t('advice.remaining', { n: advice.remaining_resets ?? t('advice.unknown') })}</div>
      <div>{t('advice.used', { n: advice.used_percent ?? t('advice.unknown') })}</div>
      <div>{t('advice.local_reset')}: {time(advice.local_evidence.quota_reset_at_ms)}</div>
      <div>{t('advice.expiry')}: {time(advice.earliest_expiry_ms)}</div>
      <div>{t('advice.observed')}: {time(advice.quota_observed_at_ms)}</div>
      <div>{t('advice.reset_observed')}: {time(advice.reset_observed_at_ms)}</div>
      <ul className={styles.reasons}>{advice.local_evidence.reasons.map(reason => <li key={reason}>{t(`advice.reason.${reason}`)}</li>)}</ul>
    </section>
    {advice.announcements.map(entry => <section className={styles.reading} key={entry.id} aria-label={t('advice.announcement')}>
      <div className={styles.meta}><strong>{t('advice.announcement')}</strong>
        <Tag>{t(`ann.confidence.${entry.confidence}`)}</Tag>
        {entry.has_conflict && <Tag>{t('advice.conflict')}</Tag>}
      </div>
      <div>{t('ann.source')}: {t(`ann.source.${entry.source}`)}</div>
      <div>{t('ann.scope')}: {t(`ann.scope.${entry.scope}`)} {entry.model}</div>
      <div>{t('ann.expected')}: {time(entry.expected_reset_at_ms)}</div>
      <div>{t('advice.registered')}: {time(entry.registered_at_ms)}</div>
      <p>{t(entry.influences_advice ? 'advice.notice_applied' : 'advice.notice_only')}</p>
      {entry.has_conflict && <p>{t('advice.conflict_hint')}</p>}
    </section>)}
    <p className={styles.hint}>{t('advice.hint')}</p>
  </Panel>;
}

