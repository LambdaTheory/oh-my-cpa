import React, { useState, useEffect } from 'react';
import { Button, Input, Modal, Space } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Switch } from '../common/Switch';
import { Tag } from '../common/Tag';
import { Notice, useToast } from '../feedback';
import { StatusLabel } from '../common/StatusLabel';
import { SendOutlined, SaveOutlined, ReloadOutlined } from '../icons';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import { getTimeZone } from '../../utils/time';
import { getDateTimeFormatter } from '../../utils/dateTimeFormat';
import type { FeishuSettingsWriteDTO, FeishuStatusDTO } from '../../types/feishu';

export const FeishuSettingsPanel: React.FC = () => {
  const t = useT();
  const toast = useToast();
  const queryClient = useQueryClient();
  const isDemo = isDemoMode();

  // Local form state
  const [enabled, setEnabled] = useState(false);
  const [appId, setAppId] = useState('');
  const [appSecret, setAppSecret] = useState('');
  const [chatId, setChatId] = useState('');
  const [summaryTimesStr, setSummaryTimesStr] = useState('10:00, 17:00');
  const [quietStart, setQuietStart] = useState('23:00');
  const [quietEnd, setQuietEnd] = useState('08:00');
  const [channelTimeoutMinutes, setChannelTimeoutMinutes] = useState<number>(30);
  const [monitorTimeoutMinutes, setMonitorTimeoutMinutes] = useState<number>(30);

  // Test confirm modal
  const [isTestConfirmOpen, setIsTestConfirmOpen] = useState(false);

  // Queries
  const settingsQuery = useQuery({
    queryKey: ['feishu-settings'],
    queryFn: () => api.getFeishuSettings(),
  });

  const statusQuery = useQuery({
    queryKey: ['feishu-status'],
    queryFn: () => api.getFeishuStatus(),
    refetchInterval: 15_000,
  });

  // Sync state from server settings
  useEffect(() => {
    if (settingsQuery.data) {
      const s = settingsQuery.data;
      setEnabled(s.enabled ?? false);
      setAppId(s.app_id ?? '');
      setAppSecret(''); // App secret write-only, not echoed
      setChatId(s.chat_id ?? '');
      if (s.summary_times && s.summary_times.length > 0) {
        setSummaryTimesStr(s.summary_times.join(', '));
      }
      if (s.quiet_start) setQuietStart(s.quiet_start);
      if (s.quiet_end) setQuietEnd(s.quiet_end);
      if (s.channel_unavailable_after_ms !== undefined) {
        setChannelTimeoutMinutes(Math.round(s.channel_unavailable_after_ms / 60_000));
      }
      if (s.monitor_down_after_ms !== undefined) {
        setMonitorTimeoutMinutes(Math.round(s.monitor_down_after_ms / 60_000));
      }
    }
  }, [settingsQuery.data]);

  // Mutations
  const saveMutation = useMutation({
    mutationFn: async () => {
      const summary_times = summaryTimesStr
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);

      const payload: FeishuSettingsWriteDTO = {
        enabled,
        app_id: appId.trim(),
        chat_id: chatId.trim(),
        summary_times,
        quiet_start: quietStart.trim(),
        quiet_end: quietEnd.trim(),
        channel_unavailable_after_ms: Number(channelTimeoutMinutes) * 60_000,
        monitor_down_after_ms: Number(monitorTimeoutMinutes) * 60_000,
      };

      if (appSecret.trim()) {
        payload.app_secret = appSecret.trim();
      }

      return api.putFeishuSettings(payload);
    },
    onSuccess: () => {
      toast.success(t('feishu.save_success'));
      setAppSecret('');
      void queryClient.invalidateQueries({ queryKey: ['feishu-settings'] });
      void queryClient.invalidateQueries({ queryKey: ['feishu-status'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  const testMutation = useMutation({
    mutationFn: () => api.postFeishuTest(),
    onSuccess: (res) => {
      if (res.sent) {
        toast.success(t('feishu.test_success'));
      } else if (res.reason === 'queued_for_retry') {
        toast.info(t('feishu.test_queued', { attempts: res.attempts ?? 0, next: formatTime(res.next_attempt_at) }));
      } else {
        toast.error(t('feishu.test_failed', { error: res.error || 'Unknown error' }));
      }
      setIsTestConfirmOpen(false);
      void queryClient.invalidateQueries({ queryKey: ['feishu-status'] });
    },
    onError: (err: Error) => {
      toast.error(t('feishu.test_failed', { error: err.message }));
      setIsTestConfirmOpen(false);
    },
  });

  const status: FeishuStatusDTO | undefined = statusQuery.data;

  const formatTime = (isoString?: string) => {
    if (!isoString) return '—';
    try {
      const d = new Date(isoString);
      return getDateTimeFormatter('zh-CN', {
        timeZone: getTimeZone(),
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(d);
    } catch {
      return isoString;
    }
  };

  const isEnvOverridden = status?.source?.toLowerCase().includes('env') || status?.forced_off;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Notice info */}
      <Notice
        tone="info"
        title={t('feishu.section_title')}
        description={t('feishu.section_desc')}
      />

      {/* Connection & Runtime Status Card */}
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 12,
          padding: '14px 18px',
          background: 'var(--surface)',
          border: '1px solid var(--border)',
          borderRadius: 'var(--radius)',
        }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ fontWeight: 600 }}>{t('feishu.section_title')}</span>
            {status?.connected ? (
              <StatusLabel tone="success">{t('feishu.bot_connected')}</StatusLabel>
            ) : (
              <StatusLabel tone="neutral">{t('feishu.bot_disconnected')}</StatusLabel>
            )}
            {isEnvOverridden && (
              <Tag size="small" tone="accent">{t('feishu.source_env_override')}</Tag>
            )}
            {status?.forced_off && (
              <Tag size="small" tone="danger">{t('feishu.forced_off_badge')}</Tag>
            )}
            {status?.degraded && (
              <Tag size="small" tone="warn">{t('feishu.degraded_badge', { reason: status.reason || 'degraded' })}</Tag>
            )}
          </div>

          <Space>
            <Button
              icon={<SendOutlined />}
              disabled={isDemo || !status?.ready || testMutation.isPending}
              onClick={() => setIsTestConfirmOpen(true)}
            >
              {t('feishu.btn_test')}
            </Button>
            <Button
              icon={<ReloadOutlined />}
              aria-label={t('common.refresh')}
              onClick={() => {
                void settingsQuery.refetch();
                void statusQuery.refetch();
              }}
            />
          </Space>
        </div>

        {/* Runtime metric badges */}
        <div
          style={{
            display: 'flex',
            gap: 16,
            flexWrap: 'wrap',
            fontSize: 12,
            color: 'var(--muted)',
            background: 'var(--bg)',
            padding: '8px 12px',
            borderRadius: 4,
          }}
        >
          <span>{t('feishu.pending_sends', { n: status?.pending_sends ?? 0 })}</span>
          <span>·</span>
          <span>{t('feishu.queued_sends', { n: status?.queued_sends ?? 0 })}</span>
          <span>·</span>
          <span>{t('feishu.open_items', { n: status?.open_items ?? 0 })}</span>
          <span>·</span>
          <span>{t('feishu.last_summary', { time: formatTime(status?.last_summary_at) })}</span>
        </div>
      </div>

      {/* Settings Form */}
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 16,
          padding: '16px 18px',
          background: 'var(--surface)',
          border: '1px solid var(--border)',
          borderRadius: 'var(--radius)',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <div>
            <div style={{ fontWeight: 600 }}>{t('feishu.form_enabled')}</div>
          </div>
          <Switch
            checked={enabled}
            disabled={isDemo || isEnvOverridden}
            onChange={setEnabled}
            aria-label={t('feishu.form_enabled')}
          />
        </div>

        <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14 }}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 16 }}>
            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 4 }}>
                {t('feishu.form_app_id')}
              </label>
              <Input
                value={appId}
                disabled={isDemo || isEnvOverridden}
                placeholder="cli_a1b2c3d4..."
                onChange={(e) => setAppId(e.target.value)}
              />
              <span style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, display: 'block' }}>
                {t('feishu.form_app_id_desc')}
              </span>
            </div>

            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 4 }}>
                {t('feishu.form_app_secret')}
              </label>
              <Input.Password
                value={appSecret}
                disabled={isDemo || isEnvOverridden}
                placeholder={settingsQuery.data?.has_secret ? '•••••••• (保持不变)' : 'App Secret'}
                onChange={(e) => setAppSecret(e.target.value)}
              />
              <span style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, display: 'block' }}>
                {t('feishu.form_app_secret_desc')}
              </span>
            </div>

            <div style={{ gridColumn: '1 / -1' }}>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 4 }}>
                {t('feishu.form_chat_id')}
              </label>
              <Input
                value={chatId}
                disabled={isDemo || isEnvOverridden}
                placeholder="ou_xxxxxxxxxxxxxxxxxxxx"
                onChange={(e) => setChatId(e.target.value)}
              />
              <span style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, display: 'block' }}>
                {t('feishu.form_chat_id_desc')}
              </span>
            </div>
          </div>
        </div>

        {/* Schedules & Thresholds */}
        <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14 }}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 16 }}>
            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 4 }}>
                {t('feishu.form_summary_times')}
              </label>
              <Input
                value={summaryTimesStr}
                disabled={isDemo || isEnvOverridden}
                placeholder="10:00, 17:00"
                onChange={(e) => setSummaryTimesStr(e.target.value)}
              />
              <span style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, display: 'block' }}>
                {t('feishu.form_summary_times_desc')}
              </span>
            </div>

            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 4 }}>
                {t('feishu.form_quiet_hours')}
              </label>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Input
                  style={{ width: 100 }}
                  value={quietStart}
                  disabled={isDemo || isEnvOverridden}
                  placeholder="23:00"
                  onChange={(e) => setQuietStart(e.target.value)}
                />
                <span>-</span>
                <Input
                  style={{ width: 100 }}
                  value={quietEnd}
                  disabled={isDemo || isEnvOverridden}
                  placeholder="08:00"
                  onChange={(e) => setQuietEnd(e.target.value)}
                />
              </div>
              <span style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, display: 'block' }}>
                {t('feishu.form_quiet_hours_desc')}
              </span>
            </div>

            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 4 }}>
                {t('feishu.form_tight_percent')}
              </label>
              <Input
                type="number"
                min={1}
                max={99}
                value={10}
                disabled
              />
              <span style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, display: 'block' }}>
                {t('feishu.form_tight_percent_desc')}
              </span>
            </div>

            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 4 }}>
                {t('feishu.form_channel_timeout')}
              </label>
              <Input
                type="number"
                min={1}
                value={channelTimeoutMinutes}
                disabled={isDemo || isEnvOverridden}
                onChange={(e) => setChannelTimeoutMinutes(Number(e.target.value))}
              />
              <span style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, display: 'block' }}>
                {t('feishu.form_channel_timeout_desc')}
              </span>
            </div>

            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, marginBottom: 4 }}>
                {t('feishu.form_monitor_timeout')}
              </label>
              <Input
                type="number"
                min={1}
                value={monitorTimeoutMinutes}
                disabled={isDemo || isEnvOverridden}
                onChange={(e) => setMonitorTimeoutMinutes(Number(e.target.value))}
              />
              <span style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, display: 'block' }}>
                {t('feishu.form_monitor_timeout_desc')}
              </span>
            </div>
          </div>
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 8 }}>
          <Button
            type="primary"
            icon={<SaveOutlined />}
            disabled={isDemo || isEnvOverridden || saveMutation.isPending}
            loading={saveMutation.isPending}
            onClick={() => saveMutation.mutate()}
          >
            {t('feishu.btn_save')}
          </Button>
        </div>
      </div>

      {/* Test Card Confirm Modal */}
      <Modal
        open={isTestConfirmOpen}
        title={t('feishu.test_confirm_title')}
        onCancel={() => setIsTestConfirmOpen(false)}
        footer={[
          <Button key="cancel" onClick={() => setIsTestConfirmOpen(false)}>
            {t('common.cancel')}
          </Button>,
          <Button
            key="submit"
            type="primary"
            loading={testMutation.isPending}
            onClick={() => testMutation.mutate()}
          >
            {t('common.confirm')}
          </Button>,
        ]}
      >
        <p style={{ color: 'var(--fg2)' }}>{t('feishu.test_confirm_desc')}</p>
      </Modal>
    </div>
  );
};
