import clsx from 'clsx';
import { Link } from 'react-router-dom';
import { useT } from '../../i18n';
import type { LendingOverview } from '../../types/quota';
import { lendingEntryState } from './lendingAdvice';
import { QUOTA_LENDING_PATH } from './lendingMount';
import styles from './LendingAdviceEntryLink.module.css';

interface LendingAdviceEntryLinkProps {
  lending?: LendingOverview | null;
  nowMS: number;
}

/**
 * The OAuth page header's one-line entry to the quota lending page.
 *
 * It is a link inside the existing summary strip, not a button or a block, so the workspace does
 * not grow. The label states the state in words, so the accent dot is never the only signal.
 */
export function LendingAdviceEntryLink({ lending, nowMS }: LendingAdviceEntryLinkProps) {
  const t = useT();
  const state = lendingEntryState(lending, nowMS);
  if (!state) return null;
  const label = state.kind === 'advised'
    ? t(state.advisedCount === 1 ? 'quota.lending.entry_advised_one' : 'quota.lending.entry_advised_other', {
      n: state.advisedCount,
    })
    : state.kind === 'balanced'
      ? t('quota.lending.entry_balanced')
      : t('quota.lending.entry_plain');
  return (
    <Link
      to={QUOTA_LENDING_PATH}
      className={clsx(styles.entry, styles[state.kind])}
      data-lending-entry={state.kind}
      data-lending-entry-count={state.advisedCount}
    >
      {state.kind === 'advised' && <span className={styles.dot} aria-hidden="true" />}
      {label}
    </Link>
  );
}
