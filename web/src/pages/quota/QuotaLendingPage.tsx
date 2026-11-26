import React from 'react';
import { Button } from 'antd';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useLocation, useNavigate } from 'react-router-dom';
import { api } from '../../api/client';
import { PageHeader } from '../../components/common/PageHeader';
import { PageLoading } from '../../components/common/PageLoading';
import { Tag } from '../../components/common/Tag';
import { LoadFailure } from '../../components/feedback';
import { ArrowLeftOutlined } from '../../components/icons';
import { useVisibleNow } from '../../hooks/useVisibleNow';
import { useT } from '../../i18n';
import { LendingAdviceView } from './LendingAdviceView';
import { lendingBasisMS } from './lendingAdvice';
import { QUOTA_LENDING_BACK_LABEL_KEY, hasPreviousHistoryEntry } from '../../types/quotaLendingMount';
import { formatShortDateTime } from './quotaFormat';
import styles from './QuotaLendingPage.module.css';

/**
 * The quota lending page: every pool's complete, read-only advice.
 *
 * It shares the OAuth workspace's `['management-quota']` query and its `useVisibleNow` clock, so
 * opening it costs no extra request and its figures agree with the header entry and the drawer.
 * The address and the menu label come from `quotaLendingMount.ts`, so a navigation redesign
 * groups the page without touching this file.
 */
export const QuotaLendingPage: React.FC = () => {
  const t = useT();
  const navigate = useNavigate();
  const { key } = useLocation();
  const visibleNow = useVisibleNow();
  const quotaQuery = useQuery({
    queryKey: ['management-quota'],
    queryFn: api.getQuotaOverview,
    refetchInterval: 60_000,
    staleTime: 15_000,
    placeholderData: keepPreviousData,
  });
  const canGoBack = React.useMemo(
    () => hasPreviousHistoryEntry(window.history.state as { idx?: unknown } | null),
    [key],
  );
  const lending = quotaQuery.data?.lending;
  const subtitle = lending
    ? t('quota.lending.basis', { time: formatShortDateTime(lendingBasisMS(lending)) })
    : undefined;

  return (
    <div className="terminal-page terminal-page-stack">
      <PageHeader
        className={styles.header}
        title={t('quota.lending.title')}
        subtitle={subtitle ? <span className={styles['header-subtitle']}>{subtitle}</span> : undefined}
        actions={canGoBack ? (
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate(-1)}>
            {t(QUOTA_LENDING_BACK_LABEL_KEY)}
          </Button>
        ) : undefined}
      >
        <Tag className={styles['header-tag']} data-lending-read-only="true">{t('quota.lending.read_only')}</Tag>
      </PageHeader>
      {quotaQuery.isLoading ? (
        <PageLoading variant="block" />
      ) : quotaQuery.isError && !quotaQuery.data ? (
        <LoadFailure
          title={t('omc.quota_read_failed')}
          error={quotaQuery.error}
          onRetry={() => { void quotaQuery.refetch(); }}
        />
      ) : (
        <LendingAdviceView mode="page" lending={lending} nowMS={visibleNow} />
      )}
    </div>
  );
};
