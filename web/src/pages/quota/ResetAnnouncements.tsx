import React from 'react';
import { Button, Form, Input, Modal, Select, Space } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import { useVisibleNow } from '../../hooks/useVisibleNow';
import { isDemoMode } from '../../types/demoMode';
import type { ResetAnnouncementInput } from '../../types/resetAnnouncements';
import { SectionHeading } from '../../components/common/SectionHeading';
import { Pager } from '../../components/common/Pager';
import { Panel } from '../../components/common/Panel';
import { PageLoading } from '../../components/common/PageLoading';
import { EmptyState } from '../../components/common/EmptyState';
import { LoadFailure, useToast } from '../../components/feedback';
import { useTimeZone } from '../../utils/TimeZoneProvider';
import dayjs from '../../utils/time';
import styles from './ResetAnnouncements.module.css';

interface AnnouncementForm extends Omit<ResetAnnouncementInput, 'source' | 'expected_reset_at_ms'> {
  expectedTime?: string;
}

export function ResetAnnouncements() {
  const t = useT();
  const toast = useToast();
  const client = useQueryClient();
  const nowMS = useVisibleNow();
  const timeZone = useTimeZone();
  const [page, setPage] = React.useState(1);
  const [isOpen, setIsOpen] = React.useState(false);
  const [form] = Form.useForm<AnnouncementForm>();
  const scope = Form.useWatch('scope', form);
  const query = useQuery({ queryKey: ['reset-announcements', page], queryFn: () => api.listResetAnnouncements(page), refetchInterval: 30_000 });
  const [isLoadingVisible, setIsLoadingVisible] = React.useState(false);
  React.useEffect(() => {
    if (!query.isPending) {
      setIsLoadingVisible(false);
      return;
    }
    // Fast reads stay quiet; background refreshes retain the existing content.
    const timer = window.setTimeout(() => setIsLoadingVisible(true), 200);
    return () => window.clearTimeout(timer);
  }, [query.isPending]);
  const mutation = useMutation({
    mutationFn: (values: AnnouncementForm) => api.createResetAnnouncement({
      provider: values.provider, source: 'manual', scope: values.scope,
      model: values.scope === 'model' ? values.model.trim() : '',
      auth_index: values.scope === 'credential' ? values.auth_index.trim() : '',
      confidence: values.confidence, original_text: values.original_text,
      expected_reset_at_ms: values.expectedTime ? dayjs(values.expectedTime).valueOf() : null,
    }),
    onSuccess: async () => {
      setIsOpen(false);
      form.resetFields();
      setPage(1);
      toast.success(t('ann.saved'));
      await client.invalidateQueries({ queryKey: ['reset-announcements'] });
    },
    onError: () => toast.error(t('ann.save_failed')),
  });
  const items = (query.data?.items ?? []).filter((item) => item.status === 'active' && (item.expected_reset_at_ms === null || item.expected_reset_at_ms > nowMS));
  const required = { required: true, whitespace: true, message: t('ann.required') };
  return (
    <Panel role="region" aria-label={t('ann.title')}>
      <SectionHeading className={styles.heading} title={t('ann.title')} description={t('ann.description')} extra={
        <Space wrap>
          <Button size="small" onClick={() => void query.refetch()}>{t('common.refresh')}</Button>
          <Button size="small" disabled={isDemoMode()} onClick={() => setIsOpen(true)}>{t('ann.register')}</Button>
        </Space>
      } />
      {query.isError && <LoadFailure title={t('ann.load_failed')} error={query.error} onRetry={() => void query.refetch()} />}
      {query.isPending && isLoadingVisible && <PageLoading variant="block" />}
      {!query.isPending && !query.isError && items.length === 0 && <EmptyState size="compact" title={t('ann.empty')} />}
      <ul className={styles.list}>
        {items.map((item) => <li key={item.id} className={styles.item}>
          <div className={styles.meta}>
            <strong>{item.provider}</strong>
            <span>{t('ann.source')}: {t(`ann.source.${item.source}`)}</span>
            <span>{t('ann.confidence')}: {t(`ann.confidence.${item.confidence}`)}</span>
            <span>{t('ann.scope')}: {t(`ann.scope.${item.scope}`)} {item.model || item.auth_index}</span>
            <span>{t('ann.expected')}: {item.expected_reset_at_ms === null ? t('ann.unknown_time') : dayjs(item.expected_reset_at_ms).format('YYYY-MM-DD HH:mm Z')}</span>
          </div>
          <p className={styles.original}>{item.original_text}</p>
        </li>)}
      </ul>
      <Pager page={page} pageSize={20} total={query.data?.total ?? 0} onChange={setPage} />
      <Modal open={isOpen} title={t('ann.register')} onCancel={() => { if (!mutation.isPending) { setIsOpen(false); form.resetFields(); } }}
        okText={t('ann.submit')} cancelText={t('common.cancel')} confirmLoading={mutation.isPending}
        cancelButtonProps={{ disabled: mutation.isPending }} onOk={() => form.submit()}>
        <Form form={form} layout="vertical" onFinish={(values) => mutation.mutate(values)} initialValues={{ scope: 'all' }} disabled={mutation.isPending}>
          <Form.Item name="provider" label={t('ann.provider')} rules={[required, { pattern: /^[a-z][a-z0-9_-]{0,63}$/, message: t('ann.provider_invalid') }]}>
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item name="scope" label={t('ann.scope')} rules={[required]}>
            <Select options={['all', 'model', 'credential'].map((value) => ({ value, label: t(`ann.scope.${value}`) }))} />
          </Form.Item>
          {scope === 'model' && <Form.Item name="model" label={t('ann.model')} rules={[required]}><Input maxLength={256} /></Form.Item>}
          {scope === 'credential' && <Form.Item name="auth_index" label={t('ann.credential')} rules={[required]}><Input maxLength={256} /></Form.Item>}
          <Form.Item name="confidence" label={t('ann.confidence')} rules={[required]}>
            <Select options={['confirmed', 'probable', 'unverified'].map((value) => ({ value, label: t(`ann.confidence.${value}`) }))} />
          </Form.Item>
          <Form.Item name="expectedTime" label={t('ann.expected_zone', { zone: timeZone })} rules={[{ validator: async (_, value: string | undefined) => {
            if (!value) return;
            try { if (dayjs(value).isValid() && dayjs(value).valueOf() > Date.now()) return; } catch { /* Invalid calendar input remains a form error. */ }
            throw new Error(t('ann.time_invalid'));
          } }]}>
            <Input type="datetime-local" />
          </Form.Item>
          <Form.Item name="original_text" label={t('ann.original')} extra={t('ann.original_hint')} rules={[required, { validator: async (_, value: string | undefined) => {
            if (value && (Array.from(value).length > 4000 || value.includes(String.fromCharCode(0)))) throw new Error(t('ann.text_invalid'));
          } }]}>
            <Input.TextArea rows={4} />
          </Form.Item>
        </Form>
      </Modal>
    </Panel>
  );
}
