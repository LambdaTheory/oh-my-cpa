import React from 'react';
import { Tooltip } from 'antd';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  StopOutlined,
  WarningOutlined,
} from '../../components/icons';
import type { TFunc } from '../../i18n';
import type { QuotaItem } from '../../types/quota';
import { resolveQuotaCooldownTooltip } from './quotaCooldown';
import { Tag } from '../../components/common/Tag';
import { quotaRefreshReasonLabel, resolveQuotaStatus } from '../../utils/quotaStatus';
import { formatShortDateTime } from './quotaFormat';

/**
 * The one status vocabulary for credential quota.
 *
 * The list row and the Drawer's quota tab both read it, so a credential cannot be
 * "Normal" in one place and something else in the other.
 */
export function quotaStatusTag(item: QuotaItem, t: TFunc, nowMS = Date.now()): React.ReactNode {
  const status = resolveQuotaStatus(item, nowMS);
  if (status === 'disabled') return <Tag>{t('af.disabled')}</Tag>;
  if (status === 'refresh_failed') {
    const reason = quotaRefreshReasonLabel(item.last_refresh, t);
    const detail = [reason, item.last_refresh?.reason_summary].filter(Boolean).join(' · ');
    return <Tooltip title={detail}><Tag tone="danger">{t('quota.status_refresh_failed')} · {reason}</Tag></Tooltip>;
  }
  if (status === 'rate_limited') {
    const retry = item.last_refresh?.retry_at_ms;
    return <Tooltip title={retry ? t('quota.refresh_retry_at', { time: formatShortDateTime(retry) }) : undefined}><Tag tone="warn">{t('quota.status_rate_limited')}</Tag></Tooltip>;
  }
  if (status === 'cooldown') {
    const reason = item.active_cooldown?.reason;
    return (
      <Tooltip title={resolveQuotaCooldownTooltip(reason, t('quota.cooldown_active_desc'))}>
        <Tag tone="danger" icon={<StopOutlined />}>{t('quota.status_cooldown')}</Tag>
      </Tooltip>
    );
  }
  switch (status) {
    case 'healthy':
      return <Tag tone="success" icon={<CheckCircleOutlined />}>{t('quota.status_normal')}</Tag>;
    case 'warning':
      return <Tag tone="warn" icon={<WarningOutlined />}>{t('quota.status_warning')}</Tag>;
    case 'exhausted':
      return <Tag tone="danger" icon={<CloseCircleOutlined />}>{t('quota.status_exceeded')}</Tag>;
    case 'error':
      return <Tag tone="danger">{t('quota.status_error')}</Tag>;
    case 'stale':
      return <Tag>{t('quota.status_data_stale')}</Tag>;
    case 'unpublished':
      // Distinct from idle: the read happened, and the provider published no window. The two must
      // not share a label, because only one of them is worth repeating.
      return <Tag>{t('quota.status_unpublished')}</Tag>;
    case 'loading':
      return <Tag tone="accent">{t('common.loading')}</Tag>;
    default:
      return <Tag>{t('quota.status_idle')}</Tag>;
  }
}
