import React from 'react';
import { useT } from '../../i18n';
import { formatShortDateTime, quotaRemainingPercent, quotaResetCountdownLabel, quotaWindowLabel } from './quotaFormat';
import { quotaRemainingStroke } from './quotaThresholds';
import { describeQuotaCapacity } from './quotaCapacity';
import { isFastResetWindow, quotaFastResetState } from './quotaWindowSelection';
import { useTokenDisplayStyle } from '../../types/tokenDisplayContext';
import type { QuotaWindow } from '../../types/quota';
import styles from './QuotaPresentation.module.css';
import { Meter } from '../../components/common/Meter';
import { Tag } from '../../components/common/Tag';

interface QuotaProgressBarProps {
  nowMS: number;
  kind?: string;
  label?: string;
  subLabel?: string;
  usedPercent?: number | null;
  remainingPercent?: number | null;
  resetAtMS?: number | null;
  resetLabel?: string;
  resetAccuracy?: 'exact' | 'derived' | 'approximate';
  /** The window's cycle, so the fast-reset mark can compare the distance to it. */
  periodHours?: number | null;
  showPercent?: boolean;
  /** The plugin returned its last successful reading for this window, not a fresh one. */
  stale?: boolean;
  /** Why the reading is cached or unusual, as the plugin stated it; absent when it stated none. */
  note?: string;
  /** The window's recorded usage and estimated capacity, when the estimate applies to it. */
  capacity?: Pick<QuotaWindow, 'usage' | 'capacity' | 'capacity_unavailable'>;
}

export const QuotaProgressBar: React.FC<QuotaProgressBarProps> = ({
  nowMS,
  kind,
  label,
  subLabel,
  usedPercent,
  remainingPercent,
  resetAtMS,
  resetLabel,
  resetAccuracy = 'exact',
  periodHours,
  showPercent = true,
  stale,
  note,
  capacity,
}) => {
  const t = useT();
  const { style: tokenStyle } = useTokenDisplayStyle();
  const capacityReading = capacity ? describeQuotaCapacity(capacity, tokenStyle, t) : null;

  // A credential may report only usage; derive the remaining share from it
  // rather than showing no bar at all. The colour rule is shared with the list row's bars.
  const clampedRemaining = quotaRemainingPercent({ remaining_percent: remainingPercent ?? undefined, used_percent: usedPercent ?? undefined });
  const hasData = clampedRemaining != null;
  const strokeColor = quotaRemainingStroke(clampedRemaining);

  const resolvedLabel = quotaWindowLabel(kind, label, t);

  // Reset cell: the same labelled line the list row prints, so the two surfaces cannot disagree
  // about the instant, its provenance, or whether a fast-reset mark applies.
  const resetWindow = {
    reset_at_ms: resetAtMS ?? undefined,
    reset_label: resetLabel,
    reset_accuracy: resetAccuracy,
    period_hours: periodHours ?? undefined,
    remaining_percent: remainingPercent ?? undefined,
    used_percent: usedPercent ?? undefined,
  };
  const computedResetText = quotaResetCountdownLabel(resetWindow, nowMS, t) || t('quota.window_boundary_unknown');
  const fastState = quotaFastResetState(resetWindow, nowMS);
  const isFast = isFastResetWindow(resetWindow, nowMS);

  const displayPercent = hasData ? `${clampedRemaining}%` : '--';

  return (
    <div className={styles['progress-wrap']}>
      <div className={styles['progress-label-row']}>
        <span className={styles['progress-label']} title={resolvedLabel}>
          {resolvedLabel}
          {subLabel && <span className={styles['progress-sub-label']}>{subLabel}</span>}
          {stale && (
            <span
              className={styles['progress-sub-label']}
              data-quota-window-stale="true"
              title={t('quota.plugin_stale_hint')}
            >
              {t('quota.plugin_stale')}
            </span>
          )}
        </span>
        {showPercent && (
          <span className={styles['progress-value']} style={{ color: hasData ? undefined : 'var(--meta)' }}>
            {displayPercent}
          </span>
        )}
        <span
          className={styles['progress-reset']}
          title={resetAccuracy === 'exact' ? computedResetText || undefined : t('quota.reset_estimated_hint')}
        >
          {computedResetText}
          {isFast && (
            <span className={styles['progress-fast']}>
              <Tag className={styles['fast-tag']} tone={fastState === 'low' ? 'warn' : 'accent'} size="small" data-fast-reset={fastState ?? 'unknown'}>
                {fastState === 'high'
                  ? t('quota.fast_reset.high')
                  : fastState === 'low' ? t('quota.fast_reset.low') : t('quota.fast_reset.plain')}
              </Tag>
            </span>
          )}
        </span>
      </div>

      {note && (
        <div className={styles['capacity-note']} data-quota-window-note="true">{note}</div>
      )}

      <div title={hasData ? `${resolvedLabel}: ${displayPercent}` : undefined}>
        <Meter
          size="thick"
          value={clampedRemaining ?? 0}
          color={strokeColor}
          // An unread window has no reading to announce; a labelled meter would state zero.
          label={hasData ? resolvedLabel || t('quota.col_windows') : undefined}
        />
      </div>

      {capacityReading && (
        <div
          className={styles['capacity-row']}
          data-quota-capacity-basis={capacity?.capacity?.basis ?? 'current_cycle'}
          data-quota-capacity={capacityReading.estimate ? 'estimated' : 'withheld'}
        >
          {capacityReading.recorded && (
            <span className={styles['capacity-part']}>
              <span className={styles['capacity-label']}>{t('quota.capacity_recorded')}</span>
              <span className={styles['capacity-value']}>{capacityReading.recorded}</span>
            </span>
          )}
          {capacityReading.estimate ? (
            <span className={`${styles['capacity-part']} ${styles['capacity-part-end']}`}>
              <span className={styles['capacity-label']}>{capacityReading.basis ?? t('quota.capacity_estimate')}</span>
              {capacityReading.basis && capacity?.capacity?.observed_at_ms != null && (
                <span className={styles['capacity-label']}>{formatShortDateTime(capacity.capacity.observed_at_ms)}</span>
              )}
              <span className={`${styles['capacity-value']} ${styles['capacity-value-strong']}`}>{capacityReading.estimate}</span>
              <span className={styles['capacity-error']}>{capacityReading.error}</span>
            </span>
          ) : null}
          {capacityReading.note && (
            <span className={`${styles['capacity-note']} ${styles['capacity-part-end']}`}>{capacityReading.note}</span>
          )}
        </div>
      )}
    </div>
  );
};
