import type { SystemBuildIdentity } from '../../types/system';
import { useT } from '../../i18n';
import { FactList } from '../common/FactList';

export function BuildIdentity({ identity }: { identity?: SystemBuildIdentity }) {
  const t = useT();
  const unknown = t('sys.version_unknown');
  return <section aria-label={t('sys.fork_build')} data-testid="sys-build-identity">
    <FactList facts={[
      { key: 'version', label: t('sys.fork_build'), value: identity?.version || unknown },
      { key: 'revision', label: t('sys.build_commit'), value: identity?.revision
        ? <span title={identity.revision}>{identity.revision.slice(0, 12)}</span> : unknown },
      { key: 'ref', label: t('sys.build_ref'), value: identity?.ref || unknown },
    ]} />
  </section>;
}
