import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { LendingAdviceView } from '../src/pages/quota/LendingAdviceView';
import type { LendingCard, LendingDetail, LendingOverview, LendingPool } from '../src/types/quota';

const NOW = Date.parse('2026-11-23T12:00:00Z');
const DAY = 86_400_000;

function card(overrides: Partial<LendingCard> & Pick<LendingCard, 'auth_index' | 'name'>): LendingCard {
  return {
    role: 'normal',
    reason_codes: [],
    is_comparable: true,
    remaining_percent: 50,
    reset_at_ms: NOW + 4 * DAY,
    reset_accuracy: 'exact',
    window_id: 'weekly',
    window_kind: 'weekly',
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    days_to_reset: 4,
    daily_available_percent: 12.5,
    ...overrides,
  };
}

function pool(cards: LendingCard[], overrides: Partial<LendingPool> = {}): LendingPool {
  const resets = cards.map((entry) => entry.reset_at_ms).filter((value): value is number => value != null);
  return {
    id: 'codex:pro',
    provider: 'codex',
    plan_type: 'pro',
    plan_label: 'Pro 200',
    window_kind: 'weekly',
    state: 'advised',
    valid_until_ms: resets.length > 0 ? Math.min(...resets) : undefined,
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    is_reference: false,
    reason_codes: [],
    cards,
    ...overrides,
  };
}

function overview(pools: LendingPool[], unpooled: LendingOverview['unpooled'] = []): LendingOverview {
  return { generated_at_ms: NOW, pools, unpooled_count: unpooled.length, unpooled };
}

const advisedPool = () => pool([
  card({ auth_index: 'a', name: 'codex-primary', remaining_percent: 88, reset_at_ms: NOW + 4 * DAY }),
  card({ auth_index: 'b', name: 'codex-secondary', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
]);

const renderView = (props: React.ComponentProps<typeof LendingAdviceView>) => render(
  <I18nProvider><LendingAdviceView {...props} /></I18nProvider>,
);

beforeEach(() => window.localStorage.setItem('omc-lang', 'en'));

describe('lending advice page and detail', () => {
  it('shows prefer, avoid and the three figures for an advised pool', () => {
    const { container } = renderView({ lending: overview([advisedPool()]), nowMS: NOW });
    expect(container.querySelector('[data-lending-view="page"]')).toBeTruthy();
    expect(container.querySelector('[data-lending-read-only="true"]')?.textContent).toContain('Advice only, routing unchanged');
    expect(screen.getByText('codex · Pro 200 · Weekly limit')).toBeTruthy();
    expect(screen.getByText('2 lendable')).toBeTruthy();
    expect(screen.getByText('Prefer')).toBeTruthy();
    expect(screen.getByText('Avoid')).toBeTruthy();
    expect(screen.getByText('88%')).toBeTruthy();
    expect(screen.getByText('4.0d')).toBeTruthy();
    expect(screen.getByText('22.0%/day')).toBeTruthy();
    expect(screen.getByText('40%')).toBeTruthy();
    expect(screen.getByText('20.0%/day')).toBeTruthy();
    expect(screen.getByText(/codex-primary resets in 4\.0d/)).toBeTruthy();
    expect(screen.getByText(/This advice is read-only/)).toBeTruthy();
    expect(container.querySelector('[data-lending-pool]')?.getAttribute('data-lending-state')).toBe('advised');
    expect([...container.querySelectorAll('[data-lending-card]')].map((entry) => entry.getAttribute('data-lending-role')))
      .toEqual(['prefer', 'avoid']);
  });

  it('shows balanced when every serviceable card is within one point per day', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'codex-primary', remaining_percent: 21.8, reset_at_ms: NOW + 2 * DAY }),
        card({ auth_index: 'b', name: 'codex-secondary', remaining_percent: 40, reset_at_ms: NOW + 4 * DAY }),
      ], { state: 'balanced' })]),
      nowMS: NOW,
    });
    expect(screen.getAllByText('Use evenly').length).toBeGreaterThanOrEqual(3);
    expect(container.querySelector('[data-lending-pool]')?.getAttribute('data-lending-state')).toBe('balanced');
    expect([...container.querySelectorAll('[data-lending-card]')].map((entry) => entry.getAttribute('data-lending-role')))
      .toEqual(['balanced', 'balanced']);
    expect(screen.queryByText('Prefer')).toBeNull();
  });

  it('gives no preference when fewer than two cards can serve', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'codex-primary' }),
        card({
          auth_index: 'b', name: 'codex-secondary', role: 'limited', is_comparable: false,
          reason_codes: ['window_exhausted'],
        }),
      ], { state: 'limited' })]),
      nowMS: NOW,
    });
    expect(screen.getByText('No preference available')).toBeTruthy();
    expect(screen.getByText('Restricted')).toBeTruthy();
    expect(screen.getByText('No allowance left in the window')).toBeTruthy();
    expect(screen.queryByText('Prefer')).toBeNull();
    expect(container.querySelector('[data-lending-pool]')?.getAttribute('data-lending-state')).toBe('limited');
  });

  it('degrades an unreadable pool to unavailable and lists each reason', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({
          auth_index: 'a', name: 'codex-primary', role: 'unavailable', is_comparable: false,
          reason_codes: ['reset_time_unknown'],
        }),
      ], {
        state: 'unavailable',
        reason_codes: ['reset_time_unknown'],
        valid_until_ms: undefined,
      })]),
      nowMS: NOW,
    });
    expect(screen.getByText('Advice unavailable')).toBeTruthy();
    expect(screen.getByText('Not comparable')).toBeTruthy();
    expect(screen.getByText('Upstream stated text, not a reset instant')).toBeTruthy();
    expect(screen.getByText('Compare once the readings are complete')).toBeTruthy();
    expect(screen.queryByText('Prefer')).toBeNull();
    expect(screen.queryByText('Avoid')).toBeNull();
    expect(container.querySelector('[data-lending-pool]')?.getAttribute('data-lending-state')).toBe('unavailable');
  });

  it('states the pool conditions and the collapsed unpooled summary when there is no pool', () => {
    const { container } = renderView({
      lending: overview([], [
        { auth_index: 'x', name: 'xai-primary', reason_codes: ['plan_type_unknown'] },
        { auth_index: 'y', name: 'claude-secondary', reason_codes: ['no_long_window'] },
      ]),
      nowMS: NOW,
    });
    expect(screen.getByText('No lendable pool yet')).toBeTruthy();
    expect(screen.getByText(/same provider and plan/)).toBeTruthy();
    expect(container.querySelector('[data-lending-empty="true"]')).toBeTruthy();
    const summary = container.querySelector('[data-lending-unpooled-summary]') as HTMLElement;
    expect(summary).toBeTruthy();
    expect(summary.getAttribute('aria-expanded')).toBe('false');
    expect(summary.textContent).toContain('Not pooled: 2');
    expect(summary.textContent).toContain('Plan type unknown, so it joins no pool 1');
    expect(summary.textContent).toContain('No window of 24 hours or longer 1');
    expect(container.querySelector('[data-lending-unpooled-names]')).toBeNull();
  });

  it('folds the unpooled list twice: reason groups first, credential names second', () => {
    const { container } = renderView({
      lending: overview([], [
        { auth_index: 'x', name: 'xai-primary', reason_codes: ['plan_type_unknown', 'no_long_window'] },
        { auth_index: 'y', name: 'claude-secondary', reason_codes: ['plan_type_unknown'] },
      ]),
      nowMS: NOW,
    });
    const summary = container.querySelector('[data-lending-unpooled-summary]') as HTMLElement;
    fireEvent.click(summary);
    expect(summary.getAttribute('aria-expanded')).toBe('true');
    const groups = [...container.querySelectorAll('[data-lending-unpooled-group]')];
    expect(groups.map((group) => group.getAttribute('data-lending-unpooled-group')))
      .toEqual(['no_long_window', 'plan_type_unknown']);
    expect(groups[1].textContent).toContain('Plan type unknown, so it joins no pool');
    expect(groups[1].textContent?.endsWith('2')).toBe(true);
    expect(container.querySelector('[data-lending-unpooled-names]')).toBeNull();

    const planGroup = groups[1].querySelector('[data-lending-unpooled-group-toggle]') as HTMLElement;
    fireEvent.click(planGroup);
    const names = container.querySelector('[data-lending-unpooled-names]') as HTMLElement;
    expect(names).toBeTruthy();
    expect([...names.querySelectorAll('li')].map((item) => item.textContent)).toEqual(['xai-primary', 'claude-secondary']);

    const windowGroup = groups[0].querySelector('[data-lending-unpooled-group-toggle]') as HTMLElement;
    fireEvent.click(windowGroup);
    const allNames = [...container.querySelectorAll('[data-lending-unpooled-names] li')].map((item) => item.textContent);
    expect(allNames).toEqual(['xai-primary', 'xai-primary', 'claude-secondary']);
  });

  it('hides the unpooled section when the backend reports none', () => {
    const { container } = renderView({ lending: overview([advisedPool()]), nowMS: NOW });
    expect(container.querySelector('[data-lending-unpooled-section]')).toBeNull();
  });

  it('marks this credential and the pool membership in the detail view', () => {
    const lending: LendingDetail = {
      generated_at_ms: NOW,
      pool: advisedPool(),
      unpooled_reason_codes: [],
    };
    const { container } = renderView({ lending, mode: 'detail', authIndex: 'b', nowMS: NOW });
    expect(screen.getByText('Quota lending advice')).toBeTruthy();
    expect(screen.getByText('In the codex · Pro 200 · Weekly limit pool (2 credentials)')).toBeTruthy();
    expect(screen.getByText('This credential')).toBeTruthy();
    expect(container.querySelector('[data-lending-current="true"]')?.getAttribute('data-lending-card')).toBe('b');
    expect(screen.getByText(/Lending advice covers usage before a natural reset/)).toBeTruthy();
    expect(screen.getByText(/This advice is read-only/)).toBeTruthy();
    expect(container.querySelector('[data-lending-view="detail"]')).toBeTruthy();
  });

  it('names the reason a credential is in no pool in the detail view', () => {
    renderView({
      lending: { generated_at_ms: NOW, pool: null, unpooled_reason_codes: ['pool_too_small'] },
      mode: 'detail',
      authIndex: 'solo',
      nowMS: NOW,
    });
    expect(screen.getByText('No lendable pool yet')).toBeTruthy();
    expect(screen.getByText('This credential is in no lendable pool: Fewer than two credentials of this plan')).toBeTruthy();
  });

  it('marks an estimated reset as a reference and prefixes its days with a tilde', () => {
    renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'codex-primary', reset_accuracy: 'derived', remaining_percent: 88 }),
        card({ auth_index: 'b', name: 'codex-secondary', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
      ], { is_reference: true })]),
      nowMS: NOW,
    });
    expect(screen.getByText('Reference (estimated reset)')).toBeTruthy();
    expect(screen.getByText('~4.0d')).toBeTruthy();
  });

  it('prints a missing figure as an em dash instead of zero', () => {
    renderView({
      lending: overview([pool([
        card({
          auth_index: 'a', name: 'codex-primary', role: 'unavailable', is_comparable: false,
          remaining_percent: undefined, reason_codes: ['remaining_unknown'],
        }),
      ], { state: 'unavailable' })]),
      nowMS: NOW,
    });
    expect(screen.getAllByText('—')).toHaveLength(3);
  });

  it('shows the page empty state and hides the drawer block when lending is missing or null', () => {
    for (const lending of [undefined, null]) {
      const page = renderView({ mode: 'page', lending, nowMS: NOW });
      expect(page.container.querySelector('[data-lending-absent="true"]')).toBeTruthy();
      expect(page.container.querySelector('[data-lending-view="page"]')).toBeTruthy();
      expect(screen.getByText('Lending advice could not be read')).toBeTruthy();
      expect(screen.queryByText('No lendable pool yet')).toBeNull();
      expect(screen.getByText(/This advice is read-only/)).toBeTruthy();
      expect(page.container.querySelector('[data-lending-read-only="true"]')?.textContent).toContain('Advice only, routing unchanged');
      expect(page.container.querySelectorAll('button')).toHaveLength(0);
      page.unmount();

      const detail = renderView({ mode: 'detail', lending, authIndex: 'x', nowMS: NOW });
      expect(detail.container.textContent).toBe('');
      expect(detail.container.querySelector('[data-lending-view]')).toBeNull();
      detail.unmount();
    }
  });

  it('offers no control in any pool state and always states the read-only boundary', () => {
    const states: Array<React.ComponentProps<typeof LendingAdviceView>> = [
      { lending: overview([advisedPool()]), nowMS: NOW },
      { lending: overview([pool([
        card({ auth_index: 'a', name: 'codex-primary', remaining_percent: 21.8, reset_at_ms: NOW + 2 * DAY }),
        card({ auth_index: 'b', name: 'codex-secondary', remaining_percent: 40, reset_at_ms: NOW + 4 * DAY }),
      ], { state: 'balanced' })]), nowMS: NOW },
      { lending: overview([pool([
        card({ auth_index: 'a', name: 'codex-primary' }),
        card({ auth_index: 'b', name: 'codex-secondary', role: 'limited', is_comparable: false }),
      ], { state: 'limited' })]), nowMS: NOW },
      { lending: overview([pool([
        card({ auth_index: 'a', name: 'codex-primary', role: 'unavailable', is_comparable: false }),
      ], { state: 'unavailable' })]), nowMS: NOW },
      { lending: overview([]), nowMS: NOW },
      { lending: { generated_at_ms: NOW, pool: advisedPool(), unpooled_reason_codes: [] }, mode: 'detail', authIndex: 'a', nowMS: NOW },
      { lending: { generated_at_ms: NOW, pool: null, unpooled_reason_codes: ['plan_type_unknown'] }, mode: 'detail', authIndex: 'x', nowMS: NOW },
    ];
    for (const props of states) {
      const { container, unmount } = renderView(props);
      expect(container.querySelectorAll('button')).toHaveLength(0);
      expect(container.textContent).toContain('This advice is read-only');
      const tag = container.querySelector('[data-lending-read-only="true"]');
      expect(tag?.textContent).toContain('Advice only, routing unchanged');
      unmount();
    }
  });

  it('only exposes the two disclosure controls when the unpooled list is present', () => {
    const { container } = renderView({
      lending: overview([], [{ auth_index: 'x', name: 'solo', reason_codes: ['plan_type_unknown'] }]),
      nowMS: NOW,
    });
    const buttons = [...container.querySelectorAll('button')];
    expect(buttons).toHaveLength(1);
    expect(buttons[0].getAttribute('data-lending-unpooled-summary')).toBe('true');
    fireEvent.click(buttons[0]);
    const groupButtons = [...container.querySelectorAll('button')];
    expect(groupButtons).toHaveLength(2);
    expect(groupButtons[1].getAttribute('data-lending-unpooled-group-toggle')).toBe('true');
  });
});

