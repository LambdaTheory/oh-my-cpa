import { useT } from '../../i18n';
import type { QuotaItem } from '../../types/quota';
import { resolveQuotaStatus } from '../../utils/quotaStatus';
import { Meter } from './Meter';
import styles from './AccountBalance.module.css';

export function isStepFunProvider(provider: string | undefined): boolean {
  return ['step', 'stepfun'].includes(provider?.trim().toLowerCase() ?? '');
}

/** Each credential is kept separate: API keys can refer to the same billed account. */
export function AccountBalance({ item, nowMS = Date.now() }: { item?: QuotaItem; nowMS?: number }) {
  const t = useT();
  const balance = item?.plan?.account_balance;
  const status = item ? resolveQuotaStatus(item, nowMS) : undefined;
  const isMissing = !balance || Boolean(item?.error)
    || ['refresh_failed', 'rate_limited', 'error', 'stale', 'loading'].includes(status ?? '');
  const remaining = balance?.account_type === 'prepaid' ? balance.remaining_percent : undefined;
  return (
    <div className={styles['account-balance']} data-account-balance={isMissing ? 'missing' : balance.account_type}>
      <span>{t('quota.account_balance')}: {isMissing ? t('quota.balance_missing') : `CNY ${balance.balance.toFixed(2)}`}</span>
      {!isMissing && <>
        <span>{t(balance.account_type === 'postpaid' ? 'quota.balance_postpaid' : 'quota.balance_prepaid')}</span>
        {remaining != null && <>
          <span>{t('quota.balance_estimated_remaining', { percent: Math.round(remaining * 10) / 10 })}</span>
          <Meter className={styles['balance-meter']} value={remaining} label={t('quota.balance_estimated_share')} />
          <span>{t('quota.balance_estimate_hint')}</span>
        </>}
      </>}
    </div>
  );
}
