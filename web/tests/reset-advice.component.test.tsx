
import { render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { I18nProvider } from '../src/i18n';
import { CompactQuotaView } from '../src/pages/quota/CompactQuotaView';
import { CredentialQuotaBody } from '../src/pages/quota/CredentialQuotaBody';
import type { QuotaItem } from '../src/types/quota';
import type { LendingDetail } from '../src/types/quota';
import type { ResetAdvice } from '../src/types/resetAdvice';

const NOW = Date.parse('2026-10-20T08:00:00Z');
const ADVICE: ResetAdvice = {
  verdict: 'use_before_expiry', evidence: 'projection', reasons: ['announcement_reset_soon', 'announcement_local_conflict'],
  remaining_resets: 2, earliest_expiry_ms: NOW + 120 * 3_600_000, used_percent: 95,
  reset_observed_at_ms: NOW, quota_observed_at_ms: NOW,
  local_evidence: { source: 'local_observation', verdict: 'keep_for_emergency', evidence: 'projection', reasons: ['quota_near_exhaustion'], quota_reset_at_ms: NOW + 3_600_000 },
  announcements: [{ id: 'synthetic-notice', source: 'manual', scope: 'all', confidence: 'confirmed', expected_reset_at_ms: NOW + 2 * 3_600_000, registered_at_ms: NOW, influences_advice: true, has_conflict: true }],
};
const QUOTA: QuotaItem = {
  auth_index: 'synthetic-credential', name: 'Synthetic credential', type: 'codex', provider: 'codex', disabled: false,
  status: 'healthy', observed_at_ms: NOW, windows: [], quota_exceeded: false,
  recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
  capabilities: { refresh_supported: true, clear_cooldown_supported: false, reset_credit_supported: false },
};

beforeEach(() => window.localStorage.setItem('omc-lang', 'en'));

describe('reset advice in credential quota', () => {
  it.each([
    ['use_before_expiry', 'Use before expiry'], ['keep_for_emergency', 'Keep for recovery'], ['no_action', 'No action needed'],
  ] as const)('renders the %s row label', (verdict, label) => {
    render(<I18nProvider><CompactQuotaView item={{ ...QUOTA, reset_advice: { ...ADVICE, verdict } }} nowMS={NOW} /></I18nProvider>);
    expect(screen.getByText(label).getAttribute('data-reset-advice')).toBe(verdict);
  });

  it('does not turn unknown into a certain row conclusion', () => {
    const { container } = render(<I18nProvider><CompactQuotaView item={{ ...QUOTA, reset_advice: { ...ADVICE, verdict: 'unknown' } }} nowMS={NOW} /></I18nProvider>);
    expect(container.querySelector('[data-reset-advice]')).toBeNull();
  });

  it('preserves local and announcement evidence with distinct conflicting reset times', () => {
    render(<I18nProvider><CredentialQuotaBody item={{ ...QUOTA, reset_advice: ADVICE }} nowMS={NOW} isDemo={false} /></I18nProvider>);
    expect(screen.getByText('Use before expiry')).toBeTruthy();
    const local = screen.getByRole('region', { name: 'Local observation' });
    expect(within(local).getByText(/Keep for recovery/)).toBeTruthy();
    expect(within(local).getByText('Local used percentage: 95')).toBeTruthy();
    expect(within(local).getByText(/Local window reset:/).textContent).toContain('1h');
    const notice = screen.getByRole('region', { name: 'Announcement evidence' });
    expect(within(notice).getByText('Source: Manual')).toBeTruthy();
    expect(within(notice).getByText('Confirmed')).toBeTruthy();
    expect(within(notice).getByText('Conflicting evidence')).toBeTruthy();
    expect(within(notice).getByText(/Expected reset time:/).textContent).toContain('2h');
  });

  it('explains evidence-only announcements beside an uncertain local reading', () => {
    render(<I18nProvider><CredentialQuotaBody item={{ ...QUOTA, reset_advice: { ...ADVICE, verdict: 'unknown', evidence: 'unknown', reasons: ['notice_without_local_capacity'], remaining_resets: null, announcements: [{ ...ADVICE.announcements[0], confidence: 'unverified', expected_reset_at_ms: null, influences_advice: false, has_conflict: false }] } }} nowMS={NOW} isDemo={false} /></I18nProvider>);
    expect(screen.getByText('Insufficient evidence for advice')).toBeTruthy();
    expect(screen.getByText('Remaining resets: Unknown')).toBeTruthy();
    expect(screen.getByText('Unverified')).toBeTruthy();
    expect(screen.getByText('This announcement is evidence only and does not change the advice.')).toBeTruthy();
  });
});




describe('Claude entitlement read outcomes', () => {
  it.each([
    ['unavailable', 'reset_not_provided', '上游未提供重置权益', '重置权益读取失败'],
    ['failed', 'reset_reading_failed', '重置权益读取失败', '上游未提供重置权益'],
  ] as const)('renders the %s outcome in the Chinese row and detail', (status, reason, copy, otherCopy) => {
    window.localStorage.setItem('omc-lang', 'zh');
    const advice: ResetAdvice = { ...ADVICE, verdict: 'unknown', evidence: 'unknown', reasons: [reason], remaining_resets: null, earliest_expiry_ms: null, announcements: [], local_evidence: { ...ADVICE.local_evidence, verdict: 'unknown', evidence: 'unknown', reasons: [reason] } };
    const item: QuotaItem = { ...QUOTA, type: 'claude', provider: 'claude', reset_grants_status: status, reset_advice: advice };
    const { unmount, container } = render(<I18nProvider><CompactQuotaView item={item} nowMS={NOW} /></I18nProvider>);
    expect(screen.getByText(copy)).toBeTruthy();
    expect(screen.queryByText(otherCopy)).toBeNull();
    expect(container.querySelector('[data-reset-advice]')).toBeNull();
    unmount();
    render(<I18nProvider><CredentialQuotaBody item={item} nowMS={NOW} isDemo={false} /></I18nProvider>);
    expect(screen.getAllByText(copy).length).toBeGreaterThan(0);
    expect(screen.queryByText(otherCopy)).toBeNull();
    expect(screen.getByText('剩余重置次数：未知')).toBeTruthy();
  });

  it.each([
    ['reset_reading_failed', 'Reset entitlement reading failed'],
    ['reset_not_provided', 'The upstream did not provide reset entitlements'],
  ])('explains %s without inventing a zero balance', (reason, copy) => {
    const advice: ResetAdvice = { ...ADVICE, verdict: 'unknown', evidence: 'unknown', reasons: [reason], remaining_resets: null, earliest_expiry_ms: null, announcements: [], local_evidence: { ...ADVICE.local_evidence, verdict: 'unknown', evidence: 'unknown', reasons: [reason] } };
    render(<I18nProvider><CredentialQuotaBody item={{ ...QUOTA, provider: 'claude', reset_advice: advice }} nowMS={NOW} isDemo={false} /></I18nProvider>);
    expect(within(screen.getByRole('region', { name: 'Local observation' })).getByText(copy)).toBeTruthy();
    expect(screen.getByText('Remaining resets: Unknown')).toBeTruthy();
    expect(screen.queryByText('The reset entitlement reading is missing')).toBeNull();
  });

  it('shows a Claude count and earliest expiry alongside its advice', () => {
    const advice: ResetAdvice = { ...ADVICE, remaining_resets: 2, earliest_expiry_ms: NOW + 48 * 3_600_000, announcements: [], reasons: ['reset_expiring_soon'], local_evidence: { ...ADVICE.local_evidence, verdict: 'use_before_expiry', reasons: ['reset_expiring_soon'] } };
    const item: QuotaItem = { ...QUOTA, provider: 'claude', type: 'claude', reset_grants_status: 'available', reset_grants: [{ id: 'synthetic-weekly', resets_total: 3, resets_left: 2, ends_at_ms: advice.earliest_expiry_ms!, clears: ['five_hour'] }], reset_advice: advice };
    const { unmount } = render(<I18nProvider><CompactQuotaView item={item} nowMS={NOW} /></I18nProvider>);
    const badge = document.querySelector('[data-reset-grants]');
    expect(badge?.textContent).toContain('Available 2');
    expect(badge?.textContent).toContain('Earliest: 2d left');
    expect(screen.getByText('Use before expiry')).toBeTruthy();
    unmount();
    render(<I18nProvider><CredentialQuotaBody item={item} nowMS={NOW} isDemo={false} /></I18nProvider>);
    expect(screen.getByText('Remaining resets: 2')).toBeTruthy();
    expect(screen.getByText(/Earliest local entitlement expiry:/).textContent).toContain('2d');
  });
});

describe('reset-entitlement advice beside lending advice', () => {
  const LENDING: LendingDetail = {
    generated_at_ms: NOW,
    pool: {
      id: 'codex:pro', provider: 'codex', plan_type: 'pro', plan_label: 'Pro 200', window_kind: 'weekly',
      state: 'advised', valid_until_ms: NOW + 3 * 3_600_000, observed_at_ms: NOW,
      fresh_until_ms: NOW + 24 * 3_600_000, is_reference: false, is_balanced: false,
      needs_change_count: 1, hold_count: 0, reason_codes: [], outside_cards: [],
      cards: [
        {
          auth_index: 'synthetic-credential', name: 'Synthetic credential', display_name: 'Synthetic credential',
          role: 'suggested', tier: 1, window_id: 'weekly',
          window_kind: 'weekly', remaining_percent: 88, reset_at_ms: NOW + 3 * 3_600_000, reset_accuracy: 'exact',
          observed_at_ms: NOW, fresh_until_ms: NOW + 24 * 3_600_000,
          priority: 10, priority_known: true, priority_is_default: false, can_edit_priority: true,
          suggested_priority: 10, needs_change: false, reason_codes: [], is_comparable: true,
        },
        {
          auth_index: 'synthetic-peer', name: 'Synthetic peer', display_name: 'Synthetic peer',
          role: 'suggested', tier: 2, window_id: 'weekly',
          window_kind: 'weekly', remaining_percent: 40, reset_at_ms: NOW + 2 * 3_600_000, reset_accuracy: 'exact',
          observed_at_ms: NOW, fresh_until_ms: NOW + 24 * 3_600_000,
          priority: 10, priority_known: true, priority_is_default: false, can_edit_priority: true,
          suggested_priority: -10, needs_change: true, reason_codes: [], is_comparable: true,
        },
      ],
    },
    unpooled_reason_codes: [],
  };

  it('keeps the two sections separate, ordered and unchanged in meaning', () => {
    render(
      <MemoryRouter>
        <I18nProvider>
          <CredentialQuotaBody item={{ ...QUOTA, reset_advice: ADVICE }} nowMS={NOW} isDemo={false} lending={LENDING} />
        </I18nProvider>
      </MemoryRouter>,
    );
    expect(screen.getByText('Reset advice')).toBeTruthy();
    expect(screen.getByText('Quota lending advice')).toBeTruthy();
    const reset = document.querySelector('[data-reset-advice-detail="true"]') as HTMLElement;
    const lending = document.querySelector('[data-lending-view="detail"]') as HTMLElement;
    expect(reset).toBeTruthy();
    expect(lending).toBeTruthy();
    expect(reset.contains(lending)).toBe(false);
    expect(lending.contains(reset)).toBe(false);
    // The reset verdict stays in its own panel, and the lending panel does not repeat it.
    expect(reset.textContent).toContain('Use before expiry');
    expect(reset.textContent).not.toContain('Advice only, routing unchanged');
    expect(lending.textContent).not.toContain('Use before expiry');
    expect(lending.textContent).toContain('Advice only, routing unchanged');
    // The reset-entitlement block keeps the position it had before lending advice existed.
    expect(reset.compareDocumentPosition(lending) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

