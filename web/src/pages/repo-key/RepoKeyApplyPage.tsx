import React from 'react';
import { Link, useParams } from 'react-router-dom';
import { Button, Input } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError, describeError } from '../../api/client';
import { useDocumentTitle } from '../../hooks/useDocumentTitle';
import { useI18n } from '../../i18n';
import { Tag } from '../../components/common/Tag';
import { useToast } from '../../components/feedback';
import { isDemoMode } from '../../types/demoMode';
import dayjs from '../../utils/time';
import styles from './RepoKeyApplyPage.module.css';

/**
 * The screen a one-time approval link opens (AC-8).
 *
 * It is drawn on the sign-in column, outside the console's shell, for the same reason the
 * agent's consent screen is: the person arrives from another program to answer one question.
 * The link may be opened only while the application is still pending, and the answer goes
 * through the same approval action as the queue, so both routes record the same decision.
 */
export function RepoKeyApplyPage() {
  const { t } = useI18n();
  const { token = '' } = useParams();
  const queryClient = useQueryClient();
  const toast = useToast();
  const isDemo = isDemoMode();
  const [reason, setReason] = React.useState('');

  const canRead = token !== '' && !isDemo;
  const review = useQuery({
    queryKey: ['repo-key-apply-link', token],
    queryFn: () => api.getRepoKeyApprovalLink(token),
    enabled: canRead,
    retry: false,
    // Another tab may decide it while this one is open.
    refetchInterval: query => (query.state.data?.application.status === 'pending' ? 5000 : false),
  });

  const approve = useMutation({
    mutationFn: () => api.approveRepoKeyByLink(token, reason.trim()),
    onSuccess: async (result) => {
      toast.success(t('repo_key_apply.approved'));
      queryClient.setQueryData(['repo-key-apply-link', token], (current?: Awaited<ReturnType<typeof api.getRepoKeyApprovalLink>>) =>
        current ? { ...current, application: result.application } : current,
      );
    },
    onError: (err: unknown) => toast.error(err instanceof ApiError ? describeError(err) : t('repo_keys.failed')),
  });

  const reject = useMutation({
    mutationFn: () => api.rejectRepoKeyByLink(token, reason.trim()),
    onSuccess: async (result) => {
      toast.success(t('repo_key_apply.rejected'));
      queryClient.setQueryData(['repo-key-apply-link', token], (current?: Awaited<ReturnType<typeof api.getRepoKeyApprovalLink>>) =>
        current ? { ...current, application: result.application } : current,
      );
    },
    onError: (err: unknown) => toast.error(err instanceof ApiError ? describeError(err) : t('repo_keys.failed')),
  });

  const error = review.error;
  const isExpired = (error instanceof ApiError && error.status === 410) || review.data?.approval_expired === true;
  const isMissing = !isDemo && (!canRead || (error instanceof ApiError && error.status === 404));
  const isUnread = canRead && review.isError && !isExpired && !isMissing;
  const application = review.data?.application;
  const isPending = application?.status === 'pending' && !isExpired;
  const title = isMissing
    ? t('repo_key_apply.missing')
    : isExpired
      ? t('repo_key_apply.expired')
      : t('repo_key_apply.title');
  useDocumentTitle(title);

  const formatTime = (ms?: number) => (ms ? dayjs(ms).format('YYYY-MM-DD HH:mm:ss') : '—');

  return (
    <div className="auth-shell" data-testid="repo-key-apply-page">
      <main className={`auth-main ${styles['main']}`}>
        <section className={`auth-card ${styles['card']}`} aria-labelledby="repo-key-apply-title" aria-busy={review.isPending || undefined}>
          <h1 id="repo-key-apply-title" className="terminal-title auth-title">{title}</h1>

          {isDemo && <p className={styles['intro']}>{t('repo_keys.subtitle')}</p>}
          {isMissing && <p className={styles['intro']}>{t('repo_key_apply.missing_hint')}</p>}
          {isExpired && <p className={styles['intro']}>{t('repo_key_apply.expired_hint')}</p>}
          {isUnread && <p className={styles['intro']}>{t('repo_keys.failed')}</p>}
          {canRead && review.isPending && <p className={styles['intro']}>{t('repo_key_apply.loading')}</p>}

          {review.data && application && (
            <>
              <p className={styles['intro']}>{t('repo_key_apply.intro')}</p>

              <div className={styles['request']}>
                <dl className={styles['facts']}>
                  <div>
                    <dt>{t('repo_keys.col_repo')}</dt>
                    <dd className={styles['mono']}>{application.repo_display || application.repo}</dd>
                  </div>
                  <div>
                    <dt>{t('repo_keys.col_caller')}</dt>
                    <dd>{application.agent} @ {application.device}</dd>
                  </div>
                  <div>
                    <dt>{t('repo_keys.col_purpose')}</dt>
                    <dd>{application.purpose}</dd>
                  </div>
                  <div>
                    <dt>{t('repo_keys.col_kind')}</dt>
                    <dd>
                      {t(
                        application.request_kind === 'reenable'
                          ? 'repo_keys.kind_reenable'
                          : `repo_keys.kind_${application.kind}`,
                      )}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('repo_keys.col_fingerprint')}</dt>
                    <dd className={styles['fingerprint']}>{application.public_key_fingerprint || '—'}</dd>
                  </div>
                  <div>
                    <dt>{t('repo_keys.col_requested')}</dt>
                    <dd>{formatTime(application.created_at_ms)}</dd>
                  </div>
                </dl>
              </div>

              {review.data.public_key_changed && (
                <p className={styles['impact']}>
                  {t('repo_keys.review_fingerprint_changed', {
                    previous: review.data.previous_public_key_fingerprint || '—',
                  })}
                </p>
              )}

              <p className={styles['impact']}>{t(`repo_keys.impact_${review.data.impact}`)}</p>

              {isPending ? (
                <>
                  <label className={styles['label']} htmlFor="repo-key-apply-reason">
                    {t('repo_key_apply.reason')}
                  </label>
                  <Input.TextArea
                    id="repo-key-apply-reason"
                    value={reason}
                    onChange={(event) => setReason(event.target.value)}
                    rows={3}
                    maxLength={200}
                    showCount
                    disabled={isDemo}
                  />
                  <div className={styles['actions']}>
                    <Button
                      danger
                      disabled={isDemo || reason.trim() === ''}
                      loading={reject.isPending}
                      onClick={() => reject.mutate()}
                    >
                      {t('repo_key_apply.reject')}
                    </Button>
                    <Button
                      type="primary"
                      disabled={isDemo}
                      loading={approve.isPending}
                      onClick={() => approve.mutate()}
                    >
                      {t('repo_key_apply.approve')}
                    </Button>
                  </div>
                  {reason.trim() === '' && <p className={styles['hint']}>{t('repo_key_apply.reason_required')}</p>}
                </>
              ) : (
                <p className={styles['outcome']} data-testid="repo-key-apply-outcome">
                  <Tag tone={application.status === 'approved' ? 'success' : undefined}>
                    {t(`repo_keys.application_status_${application.status}`)}
                  </Tag>
                  {application.status === 'approved' && <span>{t('repo_key_apply.done_hint')}</span>}
                  {application.decision_reason && <span>{application.decision_reason}</span>}
                  {application.reject_reason && <span>{application.reject_reason}</span>}
                </p>
              )}
            </>
          )}

          {review.data && application && !isPending && (
            <p className={styles['hint']}>
              {t('repo_key_apply.settled', { status: t(`repo_keys.application_status_${application.status}`) })}
            </p>
          )}

          <Link className={styles['console']} to="/api-keys">{t('repo_keys.title')}</Link>
        </section>
      </main>
    </div>
  );
}
