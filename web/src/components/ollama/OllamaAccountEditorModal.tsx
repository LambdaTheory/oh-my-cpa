import React, { useState, useEffect } from 'react';
import { Button, Modal } from 'antd';
import { Tag } from '../common/Tag';
import { Switch } from '../common/Switch';
import { useT } from '../../i18n';
import { credentialLabel } from '../../utils/credentialLabel';
import type { OllamaAccountDTO, OllamaAccountWritePayload } from '../../types/ollama';

interface OllamaAccountEditorModalProps {
  open: boolean;
  account: OllamaAccountDTO | null;
  isSaving: boolean;
  onClose: () => void;
  onSave: (payload: OllamaAccountWritePayload) => void;
}

const FIELD_STYLE: React.CSSProperties = {
  width: '100%',
  padding: '6px 10px',
  background: 'var(--surface)',
  color: 'var(--fg)',
  border: '1px solid var(--border)',
  borderRadius: 'var(--radius)',
};

const LABEL_STYLE: React.CSSProperties = {
  display: 'block',
  marginBottom: 4,
  fontSize: 13,
  fontWeight: 500,
};

const NOTE_STYLE: React.CSSProperties = {
  display: 'block',
  fontSize: 12,
  color: 'var(--muted)',
  marginTop: 4,
};

export const OllamaAccountEditorModal: React.FC<OllamaAccountEditorModalProps> = ({
  open,
  account,
  isSaving,
  onClose,
  onSave,
}) => {
  const t = useT();
  const [formLabel, setFormLabel] = useState('');
  const [formCookie, setFormCookie] = useState('');
  const [formAPIKey, setFormAPIKey] = useState('');
  const [formEnabled, setFormEnabled] = useState(true);

  useEffect(() => {
    if (open) {
      if (account) {
        setFormLabel(credentialLabel(account.label, [`ollama:${account.id}`, account.id]));
        setFormCookie(''); // Never prefill stored cookie
        setFormAPIKey(''); // Never prefill stored API key
        setFormEnabled(account.enabled);
      } else {
        setFormLabel('');
        setFormCookie('');
        setFormAPIKey('');
        setFormEnabled(true);
      }
    }
  }, [open, account]);

  const handleSave = () => {
    onSave({
      label: formLabel.trim(),
      cookie: formCookie.trim() || undefined,
      api_key: formAPIKey.trim() || undefined,
      enabled: formEnabled,
    });
  };

  return (
    <Modal
      open={open}
      title={account ? t('ollama.drawer_edit_title') : t('ollama.drawer_add_title')}
      onCancel={onClose}
      footer={[
        <Button key="cancel" onClick={onClose}>
          {t('common.cancel')}
        </Button>,
        <Button
          key="submit"
          type="primary"
          disabled={(!account && !formCookie.trim()) || !formLabel.trim() || isSaving}
          loading={isSaving}
          onClick={handleSave}
        >
          {t('common.save')}
        </Button>,
      ]}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div>
          <label style={LABEL_STYLE}>{t('ollama.field_label')}</label>
          <input
            type="text"
            data-testid="ollama-label-input"
            style={FIELD_STYLE}
            placeholder={t('ollama.field_label_placeholder')}
            value={formLabel}
            onChange={(e) => setFormLabel(e.target.value)}
          />
        </div>

        <div>
          <label style={LABEL_STYLE}>{t('ollama.field_cookie')}</label>
          <input
            type="password"
            data-testid="ollama-cookie-input"
            autoComplete="new-password"
            style={FIELD_STYLE}
            placeholder={account ? t('ollama.field_cookie_edit_placeholder') : t('ollama.field_cookie_placeholder')}
            value={formCookie}
            onChange={(e) => setFormCookie(e.target.value)}
          />
          <span style={NOTE_STYLE}>{t('ollama.cookie_security_note')}</span>
        </div>

        <div>
          <label style={LABEL_STYLE}>{t('ollama.field_api_key')}</label>
          <input
            type="password"
            data-testid="ollama-api-key-input"
            autoComplete="new-password"
            style={FIELD_STYLE}
            placeholder={account ? t('ollama.field_api_key_edit_placeholder') : t('ollama.field_api_key_placeholder')}
            value={formAPIKey}
            onChange={(e) => setFormAPIKey(e.target.value)}
          />
          {account?.has_api_key && (
            <span style={{ ...NOTE_STYLE, display: 'flex', alignItems: 'center', gap: 6 }}>
              <Tag size="small">{t('ollama.api_key_saved')}</Tag>
              <span className="mono-num" aria-hidden="true">••••••••</span>
            </span>
          )}
          <span style={NOTE_STYLE}>{t('ollama.api_key_security_note')}</span>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Switch
            size="small"
            checked={formEnabled}
            onChange={(checked) => setFormEnabled(checked)}
            aria-label={t('ollama.field_enabled')}
          />
          <span style={{ fontSize: 13 }}>{t('ollama.field_enabled')}</span>
        </div>
      </div>
    </Modal>
  );
};
