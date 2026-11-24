import React from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { ActionMenu } from '../components/common/ActionMenu';
import { PageHeader } from '../components/common/PageHeader';
import { RefreshButton } from '../components/common/RefreshButton';
import { RepoKeysPanel } from '../components/keys/RepoKeysPanel';
import { useT } from '../i18n';

/**
 * Repository-owned keys as their own surface.
 *
 * The gateway's client keys stay on `/api-keys`; this page hosts the panel an agent's request
 * lands in, unchanged. Its header refresh re-reads the panel's active reads only, so a cached
 * search or a closed detail window is never fetched again.
 */
export const RepoKeysPage: React.FC = () => {
  const t = useT();
  const queryClient = useQueryClient();
  const [isRefreshing, setIsRefreshing] = React.useState(false);

  const refresh = React.useCallback(() => {
    setIsRefreshing(true);
    void queryClient
      .refetchQueries({ queryKey: ['management-repo-keys'], type: 'active' })
      .finally(() => setIsRefreshing(false));
  }, [queryClient]);

  const refreshControl = <RefreshButton isRefreshing={isRefreshing} onRefresh={refresh} />;

  return (
    <div className="terminal-page terminal-page-stack repo-keys-page">
      <PageHeader
        title={t('repo_keys.title')}
        actions={refreshControl}
        mobileActions={<ActionMenu>{refreshControl}</ActionMenu>}
      />
      <RepoKeysPanel isPageHosted />
    </div>
  );
};
