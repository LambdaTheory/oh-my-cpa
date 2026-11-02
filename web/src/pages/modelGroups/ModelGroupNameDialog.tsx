import React from 'react';
import { Button, Input, Modal } from 'antd';
import styles from './ModelGroups.module.css';
import { useT } from '../../i18n';
import {
  MODEL_GROUP_NAME_LIMIT,
  modelGroupNameError,
  type ModelGroup,
  type ModelGroupNameError,
} from '../../types/modelGroups';

export interface ModelGroupNameDialogProps {
  open: boolean;
  mode: 'create' | 'rename';
  /** The name a rename starts from; ignored when creating. */
  initialName?: string;
  /** Every group the name must differ from, case-insensitively. */
  groups: readonly ModelGroup[];
  /** The group being renamed, which may keep its own name. */
  exceptId?: string;
  isSaving: boolean;
  /** A refusal the server answered that the local checks could not predict. */
  serverError?: string | null;
  /** Called on every keystroke, so a refusal from the previous submission does not outlive its input. */
  onDraftChange?: () => void;
  onCancel: () => void;
  onSubmit: (name: string) => void;
}

const NAME_ERROR_KEYS: Record<ModelGroupNameError, string> = {
  empty: 'model_groups.name_required',
  too_long: 'model_groups.name_too_long',
  conflict: 'model_groups.name_conflict',
};

/**
 * The one place a group gets its name.
 *
 * Create and rename ask the same question with the same rules, so they are one dialog: a rename
 * that allowed a name a create would refuse (or the reverse) is a defect the operator would meet
 * as an inconsistent form. The server owns the rules; this checks them first so a conflict is an
 * inline sentence rather than a failed round trip that closes the dialog.
 */
export const ModelGroupNameDialog: React.FC<ModelGroupNameDialogProps> = ({
  open,
  mode,
  initialName,
  groups,
  exceptId,
  isSaving,
  serverError,
  onDraftChange,
  onCancel,
  onSubmit,
}) => {
  const t = useT();
  const [name, setName] = React.useState(initialName ?? '');
  const [localError, setLocalError] = React.useState<ModelGroupNameError | null>(null);

  // Reopening for another group must not carry the previous draft or its refusal.
  React.useEffect(() => {
    if (!open) return;
    setName(initialName ?? '');
    setLocalError(null);
  }, [open, initialName, mode]);

  const error = localError ? t(NAME_ERROR_KEYS[localError], { limit: MODEL_GROUP_NAME_LIMIT }) : serverError || undefined;

  const submit = () => {
    const refusal = modelGroupNameError(name, groups, exceptId);
    if (refusal) {
      setLocalError(refusal);
      return;
    }
    onSubmit(name.trim());
  };

  return (
    <Modal
      open={open}
      title={t(mode === 'create' ? 'model_groups.create_title' : 'model_groups.rename_title')}
      destroyOnHidden
      onCancel={onCancel}
      onOk={submit}
      footer={[
        <Button key="cancel" onClick={onCancel} disabled={isSaving}>{t('common.cancel')}</Button>,
        <Button key="save" type="primary" loading={isSaving} onClick={submit}>
          {mode === 'create' ? t('model_groups.create') : t('common.save')}
        </Button>,
      ]}
    >
      <label className={styles['name-label']} htmlFor="model-group-name">{t('model_groups.name_label')}</label>
      <Input
        id="model-group-name"
        autoFocus
        value={name}
        status={error ? 'error' : undefined}
        placeholder={t('model_groups.name_placeholder')}
        aria-describedby={error ? 'model-group-name-error' : 'model-group-name-help'}
        onChange={(event) => {
          setName(event.target.value);
          setLocalError(null);
          onDraftChange?.();
        }}
        onPressEnter={submit}
      />
      {error
        ? <p id="model-group-name-error" className={styles['form-error']} role="alert">{error}</p>
        : <p id="model-group-name-help" className={styles['form-help']}>{t('model_groups.name_help', { limit: MODEL_GROUP_NAME_LIMIT })}</p>}
    </Modal>
  );
};
