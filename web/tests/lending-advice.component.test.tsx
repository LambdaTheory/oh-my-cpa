import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { LendingAdviceView } from '../src/pages/quota/LendingAdviceView';
import { oauthPrioritySettingsPath } from '../src/types/quotaLendingMount';
import type {
  LendingCard,
  LendingDetail,
  LendingOutsideCard,
  LendingOverview,
  LendingPool,
} from '../src/types/quota';

const NOW = Date.parse('2026-11-23T12:00:00Z');
const DAY = 86_400_000;

function card(overrides: Partial<LendingCard> & Pick<LendingCard, 'auth_index' | 'name'>): LendingCard {
  return {
    display_name: overrides.name,
    role: 'suggested',
    reason_codes: [],
    is_comparable: true,
    remaining_percent: 50,
    reset_at_ms: NOW + 4 * DAY,
    reset_accuracy: 'exact',
    window_id: 'weekly',
    window_kind: 'weekly',
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    priority: 0,
    priority_known: true,
    priority_is_default: true,
    can_edit_priority: true,
    suggested_priority: 0,
    needs_change: false,
    ...overrides,
  };
}

function holdCard(overrides: Partial<LendingCard> & Pick<LendingCard, 'auth_index' | 'name'>): LendingCard {
  return card({
    role: 'hold',
    priority: undefined,
    priority_known: false,
    priority_is_default: false,
    suggested_priority: undefined,
    needs_change: undefined,
    tier: undefined,
    ...overrides,
  });
}

function pool(cards: LendingCard[], overrides: Partial<LendingPool> = {}): LendingPool {
  const suggested = cards.filter((entry) => entry.role === 'suggested');
  return {
    id: 'codex:pro',
    provider: 'codex',
    plan_type: 'pro',
    plan_label: 'Pro 200',
    window_kind: 'weekly',
    state: 'advised',
    valid_until_ms: NOW + 2 * DAY,
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    is_reference: false,
    is_balanced: false,
    needs_change_count: suggested.filter((entry) => entry.needs_change === true).length,
    hold_count: cards.filter((entry) => entry.role === 'hold').length,
    reason_codes: [],
    cards,
    outside_cards: [],
    ...overrides,
  };
}

function overview(pools: LendingPool[], unpooled: LendingOverview['unpooled'] = []): LendingOverview {
  return { generated_at_ms: NOW, pools, unpooled_count: unpooled.length, unpooled };
}

const outsideCard = (overrides: Partial<LendingOutsideCard> & Pick<LendingOutsideCard, 'auth_index' | 'name'>): LendingOutsideCard => ({
  display_name: overrides.name,
  provider: 'codex',
  priority: 0,
  priority_known: true,
  priority_is_default: true,
  ...overrides,
});

/** One card to change and one already right: the everyday advised pool. */
const changeOnePool = () => pool([
  card({
    auth_index: 'primary', name: 'synthetic-primary', tier: 1,
    remaining_percent: 88, priority: 0, suggested_priority: 0, needs_change: false,
  }),
  card({
    auth_index: 'secondary', name: 'synthetic-secondary', tier: 2,
    remaining_percent: 40, reset_at_ms: NOW + 2 * DAY,
    priority: 0, suggested_priority: -10, needs_change: true,
  }),
]);

const renderView = (props: React.ComponentProps<typeof LendingAdviceView>) => render(
  <MemoryRouter>
    <I18nProvider><LendingAdviceView {...props} /></I18nProvider>
  </MemoryRouter>,
);

const cardRow = (container: HTMLElement, authIndex: string) => container.querySelector(`[data-lending-card="${authIndex}"]`) as HTMLElement;

beforeEach(() => window.localStorage.setItem('omc-lang', 'en'));

describe('lending advice page', () => {
  it('shows one row per card with the reading, the current number, the suggested number and the verdict', () => {
    const { container } = renderView({ lending: overview([changeOnePool()]), nowMS: NOW });
    expect(container.querySelector('[data-lending-view="page"]')).toBeTruthy();
    expect(screen.getByText('codex · Pro 200')).toBeTruthy();
    expect(screen.getByText('Weekly limit')).toBeTruthy();
    expect(screen.getByText('Change 1')).toBeTruthy();
    expect(screen.getByText('Set synthetic-secondary to -10; leave synthetic-primary alone')).toBeTruthy();

    const rows = [...container.querySelectorAll('[data-lending-card]')];
    expect(rows.map((entry) => entry.getAttribute('data-lending-card'))).toEqual(['primary', 'secondary']);
    expect(rows.map((entry) => entry.getAttribute('data-lending-action'))).toEqual(['keep', 'change']);
    expect(rows.map((entry) => entry.getAttribute('data-lending-card-role'))).toEqual(['suggested', 'suggested']);

    const primary = cardRow(container, 'primary');
    expect(primary.querySelector('[data-lending-now]')?.getAttribute('data-lending-now')).toBe('0');
    expect(primary.querySelector('[data-lending-suggested]')?.getAttribute('data-lending-suggested')).toBe('0');
    expect(within(primary).getByText('default')).toBeTruthy();
    expect(within(primary).getByText('No change')).toBeTruthy();
    expect(within(primary).getByText('88%')).toBeTruthy();

    const secondary = cardRow(container, 'secondary');
    expect(secondary.querySelector('[data-lending-suggested]')?.textContent).toContain('-10');
    expect(within(secondary).getByText('Change')).toBeTruthy();
    expect(within(secondary).getByText('40%')).toBeTruthy();

    const go = secondary.querySelector('[data-lending-go="page"]') as HTMLAnchorElement;
    expect(go.getAttribute('href')).toBe(oauthPrioritySettingsPath('secondary'));
    expect(go.getAttribute('href')).toBe('/oauth-management?open=secondary');
    expect(primary.querySelector('[data-lending-go]')).toBeNull();
  });

  it('keeps the full read-only sentence inside the basis instead of the main screen', () => {
    const { container } = renderView({ lending: overview([changeOnePool()]), nowMS: NOW });
    const sentence = 'Advice only, routing unchanged: there is no apply action, and nothing in CPA is changed.';
    expect(container.textContent).not.toContain(sentence);
    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="evidence"]') as HTMLElement);
    expect(container.textContent).toContain(sentence);
  });

  it('names every card it leaves alone when more than one stays put', () => {
    renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'synthetic-a', tier: 1, suggested_priority: 0, needs_change: false }),
        card({ auth_index: 'b', name: 'synthetic-b', tier: 1, suggested_priority: 0, needs_change: false }),
        card({ auth_index: 'c', name: 'synthetic-c', tier: 2, suggested_priority: -10, needs_change: true }),
      ])]),
      nowMS: NOW,
    });
    expect(screen.getByText('Set synthetic-c to -10; leave synthetic-a, synthetic-b alone')).toBeTruthy();
  });

  it('writes the no-change verdict when every suggested number already matches', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'synthetic-a', tier: 1, priority: 10, priority_is_default: false, suggested_priority: 10 }),
        card({ auth_index: 'b', name: 'synthetic-b', tier: 2, priority: 0, suggested_priority: 0 }),
      ])]),
      nowMS: NOW,
    });
    expect(container.querySelector('[data-lending-pool-verdict]')?.textContent).toBe('No change');
    expect(screen.getByText('The current settings are already right; nothing to change')).toBeTruthy();
    expect([...container.querySelectorAll('[data-lending-action-tag]')].map((entry) => entry.getAttribute('data-lending-action-tag')))
      .toEqual(['keep', 'keep']);
    expect(container.querySelector('[data-lending-go]')).toBeNull();
  });

  it('calls a close ranking about the same and still offers no change', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'synthetic-a', tier: 1, priority: 5, suggested_priority: 5 }),
        card({ auth_index: 'b', name: 'synthetic-b', tier: 1, priority: 0, suggested_priority: 0 }),
      ], { is_balanced: true })]),
      nowMS: NOW,
    });
    expect(screen.getByText('The two are about the same; nothing to change')).toBeTruthy();
    expect(screen.getByText('Their reset times are less than a day apart, so the difference is not worth a change')).toBeTruthy();
    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="evidence"]') as HTMLElement);
    expect(within(container.querySelector('[data-lending-evidence-table="true"]') as HTMLElement).getAllByText('#1, tied'))
      .toHaveLength(2);
  });

  it('shows an unreadable current number as an em dash, keeps the suggestion, and judges nothing', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({
          auth_index: 'a', name: 'synthetic-a', tier: 1,
          priority: undefined, priority_known: false, priority_is_default: false,
          suggested_priority: 5, needs_change: undefined,
        }),
      ])]),
      nowMS: NOW,
    });
    const row = cardRow(container, 'a');
    expect(row.textContent).toContain('—');
    expect(row.querySelector('[data-lending-suggested]')?.textContent).toContain('5');
    expect(within(row).getByText('Current value unavailable')).toBeTruthy();
    expect(row.querySelector('[data-lending-action-tag]')).toBeNull();
    expect(row.querySelector('[data-lending-go]')).toBeNull();
  });

  it('names a priority this console cannot write instead of linking to the field', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({
          auth_index: 'a', name: 'synthetic-a', tier: 1,
          suggested_priority: -10, needs_change: true, can_edit_priority: false,
        }),
      ])]),
      nowMS: NOW,
    });
    const row = cardRow(container, 'a');
    expect(within(row).getByText('Change')).toBeTruthy();
    expect(within(row).getByText('This priority cannot be changed here')).toBeTruthy();
    expect(row.querySelector('[data-lending-go]')).toBeNull();
  });

  it('folds the cards it leaves alone into one counted line without a suggested number', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'synthetic-a', tier: 1, suggested_priority: 0 }),
        card({ auth_index: 'b', name: 'synthetic-b', tier: 2, suggested_priority: -10, needs_change: true }),
        holdCard({ auth_index: 'held', name: 'synthetic-held', primary_reason: 'window_exhausted', reason_codes: ['window_exhausted'] }),
      ])]),
      nowMS: NOW,
    });
    expect(screen.getByText('1 not moved')).toBeTruthy();
    expect(screen.getByText('1 more not moved')).toBeTruthy();
    expect(container.querySelector('[data-lending-hold-card="held"]')).toBeNull();

    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="hold"]') as HTMLElement);
    const held = container.querySelector('[data-lending-hold-card="held"]') as HTMLElement;
    expect(held).toBeTruthy();
    expect(held.textContent).toContain('synthetic-held');
    expect(held.textContent).toContain('Exhausted');
    expect(held.textContent).toContain('Not moved');
    expect(held.textContent).toContain('No allowance is available now');
    expect(held.querySelector('[data-lending-suggested]')).toBeNull();
  });

  it('collapses a pool with no ranking into one line that opens the held cards', () => {
    const { container } = renderView({
      lending: overview([pool([
        holdCard({ auth_index: 'held-exhausted', name: 'synthetic-exhausted', primary_reason: 'window_exhausted', reason_codes: ['window_exhausted'] }),
        holdCard({ auth_index: 'held-failed', name: 'synthetic-failed', primary_reason: 'reading_failed', reason_codes: ['reading_failed'] }),
      ], { state: 'no_advice', needs_change_count: 0 })]),
      nowMS: NOW,
    });
    const section = container.querySelector('[data-lending-state="no_advice"]') as HTMLElement;
    expect(section).toBeTruthy();
    expect(section.textContent).toContain('No advice');
    expect(section.textContent).toContain('All 2 not moved');
    expect(section.textContent).toContain('Exhausted 1');
    expect(section.textContent).toContain('Read failed 1');
    expect(container.querySelector('[data-lending-hold-rows]')).toBeNull();

    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="no-advice"]') as HTMLElement);
    expect(container.querySelector('[data-lending-hold-card="held-exhausted"]')).toBeTruthy();
    expect(container.querySelector('[data-lending-hold-card="held-failed"]')).toBeTruthy();
  });

  it('names a passed refill before the age and keeps a short age in minutes', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'synthetic-a', tier: 1 }),
        holdCard({
          auth_index: 'passed', name: 'synthetic-passed', primary_reason: 'reading_stale',
          reason_codes: ['reading_stale'], reset_at_ms: NOW - 1, observed_at_ms: NOW - 26 * 3_600_000,
        }),
        holdCard({
          auth_index: 'recent', name: 'synthetic-recent', primary_reason: 'reading_stale',
          reason_codes: ['reading_stale'], reset_at_ms: NOW + DAY, observed_at_ms: NOW - 60_000,
        }),
      ])]),
      nowMS: NOW,
    });

    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="hold"]') as HTMLElement);
    const passed = container.querySelector('[data-lending-hold-card="passed"]') as HTMLElement;
    const recent = container.querySelector('[data-lending-hold-card="recent"]') as HTMLElement;
    expect(passed.textContent).toContain('The refill time has passed (no later than now); the reading is no longer accurate; refresh the quota');
    expect(passed.textContent).not.toContain('hours ago');
    expect(recent.textContent).toContain('The last reading was 1 min ago; refresh the quota');
    expect(recent.textContent).not.toContain('1 hours ago');
  });

  it('names the window and share below the ten-percent floor, falling back when either is missing', () => {
    const { container } = renderView({
      lending: overview([pool([
        card({ auth_index: 'a', name: 'synthetic-a', tier: 1 }),
        holdCard({
          auth_index: 'low', name: 'synthetic-low', primary_reason: 'remaining_below_ten',
          reason_codes: ['remaining_below_ten'], remaining_percent: 6, window_kind: 'weekly',
        }),
        holdCard({
          auth_index: 'low-no-window', name: 'synthetic-low-no-window', primary_reason: 'remaining_below_ten',
          reason_codes: ['remaining_below_ten'], remaining_percent: 6, window_kind: undefined,
        }),
        holdCard({
          auth_index: 'low-no-share', name: 'synthetic-low-no-share', primary_reason: 'remaining_below_ten',
          reason_codes: ['remaining_below_ten'], remaining_percent: undefined, window_kind: 'weekly',
        }),
      ])]),
      nowMS: NOW,
    });

    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="hold"]') as HTMLElement);
    const low = container.querySelector('[data-lending-hold-card="low"]') as HTMLElement;
    const lowNoWindow = container.querySelector('[data-lending-hold-card="low-no-window"]') as HTMLElement;
    const lowNoShare = container.querySelector('[data-lending-hold-card="low-no-share"]') as HTMLElement;
    expect(low.textContent).toContain('Weekly limit: 6% left; kept in reserve and not ranked');
    expect(lowNoWindow.textContent).toContain('Kept in reserve and not ranked');
    expect(lowNoShare.textContent).toContain('Kept in reserve and not ranked');
  });

  it('reminds the reader of same-model cards outside the pool and lists where they live', () => {
    const { container } = renderView({
      lending: overview([pool([card({ auth_index: 'a', name: 'synthetic-a', tier: 1 })], {
        outside_cards: [
          outsideCard({ auth_index: 'o1', name: 'synthetic-outside', pool_label: 'codex · Team' }),
          outsideCard({
            auth_index: 'o2', name: 'synthetic-other', provider: 'claude',
            priority: undefined, priority_known: false, priority_is_default: false,
          }),
        ],
      })]),
      nowMS: NOW,
    });
    expect(screen.getByText('2 more credentials outside this pool serve this model')).toBeTruthy();
    expect(container.querySelector('[data-lending-outside-card]')).toBeNull();

    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="outside"]') as HTMLElement);
    const rows = [...container.querySelectorAll('[data-lending-outside-card]')];
    expect(rows.map((entry) => entry.getAttribute('data-lending-outside-card'))).toEqual(['o1', 'o2']);
    expect(rows[0].textContent).toContain('codex · Team');
    expect(rows[0].textContent).toContain('now 0 (default)');
    expect(rows[1].textContent).toContain('claude');
    expect(rows[1].textContent).toContain('now unavailable');
  });

  it('keeps the basis folded, and opening it reveals the table without changing a number', () => {
    const { container } = renderView({ lending: overview([changeOnePool()]), nowMS: NOW });
    expect(container.querySelector('[data-lending-disclosure="evidence"]')).toBeTruthy();
    expect(container.querySelector('[data-lending-evidence-table]')).toBeNull();
    expect(screen.getByText('See the basis')).toBeTruthy();

    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="evidence"]') as HTMLElement);
    const table = container.querySelector('[data-lending-evidence-table="true"]') as HTMLElement;
    expect(table).toBeTruthy();
    expect(within(table).getByText('#1')).toBeTruthy();
    expect(within(table).getByText('#2')).toBeTruthy();
    expect(within(table).getByText('synthetic-primary')).toBeTruthy();
    expect(container.querySelector('[data-lending-suggested]')?.getAttribute('data-lending-suggested')).toBe('0');
    expect(screen.getAllByText(/there is no apply action/).length).toBeGreaterThan(0);
  });

  it('keeps a long name readable at the ends and prints it in full inside the basis', () => {
    const longName = 'synthetic-credential-with-a-very-long-name';
    const { container } = renderView({
      lending: overview([pool([card({ auth_index: 'a', name: longName, tier: 1 })])]),
      nowMS: NOW,
    });
    const name = container.querySelector(`[data-lending-name="${longName}"]`) as HTMLElement;
    expect(name.getAttribute('title')).toBe(longName);
    expect(name.querySelectorAll('span')).toHaveLength(2);

    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="evidence"]') as HTMLElement);
    expect(container.querySelector('[data-lending-full-names="true"]')?.textContent).toContain(longName);
  });

  it('offers no write control in any page state', () => {
    const pageStates: Array<React.ComponentProps<typeof LendingAdviceView>> = [
      { lending: overview([changeOnePool()]), nowMS: NOW },
      { lending: overview([pool([holdCard({ auth_index: 'h', name: 'synthetic-h', primary_reason: 'reading_failed', reason_codes: ['reading_failed'] })], { state: 'no_advice' })]), nowMS: NOW },
      { lending: overview([]), nowMS: NOW },
      { lending: undefined, nowMS: NOW },
    ];
    for (const props of pageStates) {
      const { container, unmount } = renderView(props);
      expect(container.querySelector('button[type="submit"]')).toBeNull();
      expect(container.querySelector('[data-lending-apply]')).toBeNull();
      unmount();
    }
  });

  it('degrades to the absent reading state and draws no drawer block without data', () => {
    for (const lending of [undefined, null]) {
      const page = renderView({ mode: 'page', lending, nowMS: NOW });
      expect(page.container.querySelector('[data-lending-view="page"]')).toBeTruthy();
      expect(page.container.querySelector('[data-lending-absent="true"]')).toBeTruthy();
      expect(screen.getByText('Lending advice could not be read')).toBeTruthy();
      expect(screen.getByText(/Advice only, routing unchanged: there is no apply action/)).toBeTruthy();
      page.unmount();

      const detail = renderView({ mode: 'detail', lending, authIndex: 'x', nowMS: NOW });
      expect(detail.container.querySelector('[data-lending-view]')).toBeNull();
      detail.unmount();
    }
  });
});

describe('lending advice in the credential drawer', () => {
  const detail = (poolValue: LendingPool | null, reasons: string[] = []): LendingDetail => ({
    generated_at_ms: NOW,
    pool: poolValue,
    unpooled_reason_codes: reasons,
  });

  it('answers one suggested card with its number, its verdict and the group link', () => {
    const onEditPriority = vi.fn();
    const { container } = renderView({
      lending: detail(changeOnePool()),
      mode: 'detail',
      authIndex: 'secondary',
      nowMS: NOW,
      onEditPriority,
    });
    expect(container.querySelector('[data-lending-view="detail"]')?.getAttribute('data-lending-detail-state')).toBe('change');
    expect(screen.getByText('synthetic-secondary: set to -10')).toBeTruthy();
    expect(container.querySelector('[data-lending-detail-sentence="true"]')).toBeTruthy();
    fireEvent.click(container.querySelector('[data-lending-go="detail"]') as HTMLElement);
    expect(onEditPriority).toHaveBeenCalledOnce();
    const link = container.querySelector('[data-lending-detail-link="true"]') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/quota-lending');
    expect(link.textContent).toBe('View the whole group on the lending page');
  });

  it('answers a card that needs no change without a link to the field', () => {
    const { container } = renderView({
      lending: detail(changeOnePool()),
      mode: 'detail',
      authIndex: 'primary',
      nowMS: NOW,
    });
    expect(container.querySelector('[data-lending-view="detail"]')?.getAttribute('data-lending-detail-state')).toBe('keep');
    expect(screen.getByText('synthetic-primary: set to 0, no change')).toBeTruthy();
    expect(container.querySelector('[data-lending-go]')).toBeNull();
  });

  it('answers a held card with the short reason and no suggested number', () => {
    const heldPool = pool([
      card({ auth_index: 'a', name: 'synthetic-a', tier: 1 }),
      holdCard({ auth_index: 'held', name: 'synthetic-held', primary_reason: 'reading_stale', reason_codes: ['reading_stale'] }),
    ]);
    const { container } = renderView({
      lending: detail(heldPool),
      mode: 'detail',
      authIndex: 'held',
      nowMS: NOW,
    });
    expect(container.querySelector('[data-lending-view="detail"]')?.getAttribute('data-lending-detail-state')).toBe('hold');
    expect(screen.getByText('This credential is not moved: Stale reading')).toBeTruthy();
    expect(screen.getByText('The last reading was 1 min ago; refresh the quota')).toBeTruthy();
    expect(container.querySelector('[data-lending-suggested]')).toBeNull();
  });

  it('writes the same hold note in the drawer: passed refill, minutes, and the low-window share', () => {
    const heldPool = pool([
      card({ auth_index: 'a', name: 'synthetic-a', tier: 1 }),
      holdCard({
        auth_index: 'refilled', name: 'synthetic-refilled', primary_reason: 'reading_stale',
        reason_codes: ['reading_stale'], reset_at_ms: NOW - 1, observed_at_ms: NOW - 26 * 3_600_000,
      }),
      holdCard({
        auth_index: 'recent', name: 'synthetic-recent', primary_reason: 'reading_stale',
        reason_codes: ['reading_stale'], reset_at_ms: NOW + DAY, observed_at_ms: NOW - 60_000,
      }),
      holdCard({
        auth_index: 'low', name: 'synthetic-low', primary_reason: 'remaining_below_ten',
        reason_codes: ['remaining_below_ten'], remaining_percent: 6, window_kind: 'weekly',
      }),
    ]);
    const renderDetail = (authIndex: string) => renderView({
      lending: detail(heldPool), mode: 'detail', authIndex, nowMS: NOW,
    });

    const refilled = renderDetail('refilled');
    const refilledText = refilled.container.querySelector('[data-lending-view="detail"]')?.textContent ?? '';
    expect(refilledText).toContain('The refill time has passed (no later than now); the reading is no longer accurate; refresh the quota');
    expect(refilledText).not.toContain('1 hours ago');
    refilled.unmount();

    const recent = renderDetail('recent');
    const recentText = recent.container.querySelector('[data-lending-view="detail"]')?.textContent ?? '';
    expect(recentText).toContain('The last reading was 1 min ago; refresh the quota');
    expect(recentText).not.toContain('1 hours ago');
    recent.unmount();

    const low = renderDetail('low');
    const lowText = low.container.querySelector('[data-lending-view="detail"]')?.textContent ?? '';
    expect(lowText).toContain('Weekly limit: 6% left; kept in reserve and not ranked');
  });

  it('names an unknown reading time on the page and in the drawer without a placeholder', () => {
    const heldPool = pool([
      card({ auth_index: 'a', name: 'synthetic-a', tier: 1 }),
      holdCard({
        auth_index: 'unknown-age', name: 'synthetic-unknown-age', primary_reason: 'reading_stale',
        reason_codes: ['reading_stale'], reset_at_ms: NOW + DAY, observed_at_ms: undefined,
      }),
    ]);

    const page = renderView({ lending: overview([heldPool]), nowMS: NOW });
    fireEvent.click(page.container.querySelector('[data-lending-disclosure-toggle="hold"]') as HTMLElement);
    const row = page.container.querySelector('[data-lending-hold-card="unknown-age"]') as HTMLElement;
    expect(row.textContent).toContain('The reading time is unknown; refresh the quota');
    expect(page.container.textContent).not.toContain('{');
    page.unmount();

    const drawer = renderView({
      lending: detail(heldPool), mode: 'detail', authIndex: 'unknown-age', nowMS: NOW,
    });
    const drawerText = drawer.container.querySelector('[data-lending-view="detail"]')?.textContent ?? '';
    expect(drawerText).toContain('The reading time is unknown; refresh the quota');
    expect(drawerText).not.toContain('{');
  });

  it('answers a card in no pool with the outside sentence and the reason', () => {
    const { container } = renderView({
      lending: detail(null, ['pool_too_small']),
      mode: 'detail',
      authIndex: 'solo',
      nowMS: NOW,
    });
    expect(container.querySelector('[data-lending-view="detail"]')?.getAttribute('data-lending-detail-state')).toBe('outside');
    expect(screen.getByText('This credential takes part in no lending pool; leave it as is')).toBeTruthy();
    expect(screen.getByText('Fewer than two credentials of this plan.')).toBeTruthy();
    const link = container.querySelector('[data-lending-detail-link="true"]') as HTMLAnchorElement;
    expect(link.textContent).toBe('View all on the lending page');
  });

  it('keeps the pool table on the page and states the difference from reset advice in the drawer', () => {
    const { container } = renderView({
      lending: detail(changeOnePool()),
      mode: 'detail',
      authIndex: 'secondary',
      nowMS: NOW,
    });
    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="evidence"]') as HTMLElement);
    expect(container.querySelector('[data-lending-evidence-table]')).toBeNull();
    expect(screen.getByText(/Lending advice covers usage before a natural reset/)).toBeTruthy();
    const evidence = container.querySelector('[data-lending-disclosure-body="evidence"]') as HTMLElement;
    expect(evidence.textContent).not.toContain('there is no apply action');
    expect(screen.getByText('Advice only, routing unchanged')).toBeTruthy();
  });

  it('points the drawer basis at the lending page for the outside numbers', () => {
    const withOutside = pool([
      card({ auth_index: 'a', name: 'synthetic-a', tier: 1, suggested_priority: 0, needs_change: false }),
      card({ auth_index: 'b', name: 'synthetic-b', tier: 2, suggested_priority: -10, needs_change: true }),
    ], {
      outside_cards: [outsideCard({ auth_index: 'o1', name: 'synthetic-outside', pool_label: 'codex · Team' })],
    });
    const { container } = renderView({ lending: detail(withOutside), mode: 'detail', authIndex: 'b', nowMS: NOW });
    fireEvent.click(container.querySelector('[data-lending-disclosure-toggle="evidence"]') as HTMLElement);
    const body = container.querySelector('[data-lending-disclosure-body="evidence"]') as HTMLElement;
    expect(body.textContent).toContain('the lending page lists what each outside credential of the same model is set to now');
    expect(body.textContent).not.toContain('the list below');
  });
});
