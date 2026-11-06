import React from 'react';
import { Tag, type TagTone } from '../../components/common/Tag';
import { useT } from '../../i18n';
import type { ModelGroupAvailability } from '../../types/modelGroups';

/**
 * One model's availability band, drawn the same way on the group card, the member table and
 * the candidate list.
 *
 * The labels are literal keys rather than an interpolated one, so the missing-translation
 * check can see them. A row without a reading draws a dash: the four bands all mean "the
 * server looked and found this", so none of them may stand in for "the page could not ask".
 */
const TONES: Record<ModelGroupAvailability, TagTone> = {
  available: 'success',
  tight: 'warn',
  unknown: 'neutral',
  unavailable: 'danger',
};

const LABEL_KEYS = {
  available: 'model_groups.availability.available',
  tight: 'model_groups.availability.tight',
  unknown: 'model_groups.availability.unknown',
  unavailable: 'model_groups.availability.unavailable',
} as const;

export const ModelGroupAvailabilityTag: React.FC<{ availability?: ModelGroupAvailability }> = ({ availability }) => {
  const t = useT();
  if (!availability) return <span aria-hidden="true">—</span>;
  return (
    <Tag size="small" tone={TONES[availability]} data-model-group-availability={availability}>
      {t(LABEL_KEYS[availability])}
    </Tag>
  );
};
