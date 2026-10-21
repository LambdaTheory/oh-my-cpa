
import { render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { CompactQuotaView } from '../src/pages/quota/CompactQuotaView';
import { CredentialQuotaBody } from '../src/pages/quota/CredentialQuotaBody';
import type { QuotaItem } from '../src/types/quota';
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



