import React from 'react';
import type { TFunc } from '../../i18n';
import { useT } from '../../i18n';
import clsx from 'clsx';
import { EmptyState } from '../../components/common/EmptyState';
import { Panel } from '../../components/common/Panel';
import { Tag, type TagTone } from '../../components/common/Tag';
import { DownOutlined, RightOutlined } from '../../components/icons';
import type { LendingCard, LendingDetail, LendingOverview, LendingPool } from '../../types/quota';
import { deriveLendingView, groupLendingUnpooled, lendingReasonLabel, lendingRoleLabel, lendingRoleTone, type LendingUnpooledGroup } from './lendingAdvice';
import { formatShortDateTime, formatTimeWithCountdown, quotaWindowLabel } from './quotaFormat';
import styles from './LendingAdviceView.module.css';

export type LendingAdviceMode = 'page' | 'detail';

interface LendingAdviceViewProps {
  lending?: LendingOverview | LendingDetail | null;
  mode?: LendingAdviceMode;
  authIndex?: string;
  nowMS: number;
  className?: string;
}

function isDetailLending(lending: LendingOverview | LendingDetail): lending is LendingDetail {
  return 'pool' in lending;
}

function missingOr(t: TFunc, value: number | undefined, render: (value: number) => string): string {
  return value == null ? t('quota.lending.value.missing') : render(value);
}

function remainingText(card: LendingCard, t: TFunc): string {
  return missingOr(t, card.remaining_percent, (value) => t('quota.lending.value.percent', { n: Math.round(value) }));
}

function daysText(card: LendingCard, t: TFunc): string {
  const text = missingOr(t, card.days_to_reset, (value) => t('quota.lending.value.days', { n: value.toFixed(1) }));
  return card.days_to_reset != null && card.reset_accuracy && card.reset_accuracy !== 'exact' ? `~${text}` : text;
}

function dailyText(card: LendingCard, t: TFunc): string {
  return missingOr(t, card.daily_available_percent, (value) => t('quota.lending.value.daily', { n: value.toFixed(1) }));
}

function poolLabel(pool: LendingPool, t: TFunc): string {
  const plan = pool.plan_label || pool.plan_type;
  const window = pool.window_kind ? quotaWindowLabel(pool.window_kind, pool.window_kind, t) : '';
  return t('quota.lending.pool_label', { provider: pool.provider, plan, window: window || '—' });
}

function serviceableCount(pool: LendingPool): number {
  return pool.cards.filter((card) => (
    card.is_comparable
    && (card.role === 'prefer' || card.role === 'avoid' || card.role === 'normal' || card.role === 'balanced')
  )).length;
}

function stateTag(pool: LendingPool, t: TFunc): { text: string; tone: TagTone } {
  switch (pool.state) {
    case 'advised':
      return { text: t('quota.lending.pool_count', { n: serviceableCount(pool) }), tone: 'accent' };
    case 'balanced':
      return { text: t('quota.lending.state_balanced'), tone: 'neutral' };
    case 'limited':
      return { text: t('quota.lending.state_limited'), tone: 'warn' };
    case 'unavailable':
      return { text: t('quota.lending.state_unavailable'), tone: 'neutral' };
  }
}

/**
 * The pool's one-sentence reason. It names the facts the three columns already show, so the
 * conclusion and the numbers cannot say different things; it never adds a judgment the pool's
 * state does not support.
 */
function poolReason(pool: LendingPool, t: TFunc): string {
  if (pool.state === 'advised') {
    const prefer = pool.cards.find((card) => card.role === 'prefer');
    const avoid = pool.cards.find((card) => card.role === 'avoid');
    if (prefer && avoid) {
      return t('quota.lending.reason_advised', {
        prefer: prefer.name,
        preferDays: daysText(prefer, t),
        preferRemaining: remainingText(prefer, t),
        preferDaily: dailyText(prefer, t),
        avoid: avoid.name,
        avoidDays: daysText(avoid, t),
        avoidRemaining: remainingText(avoid, t),
        avoidDaily: dailyText(avoid, t),
      });
    }
  }
  if (pool.state === 'balanced') {
    const first = pool.cards
      .filter((card) => card.is_comparable && card.days_to_reset != null)
      .sort((left, right) => (left.days_to_reset ?? 0) - (right.days_to_reset ?? 0))[0];
    if (first) return t('quota.lending.reason_balanced', { first: first.name, firstDays: daysText(first, t) });
  }
  if (pool.state === 'limited') return t('quota.lending.reason_limited');
  return t('quota.lending.reason_unavailable');
}

interface PoolBlockProps {
  pool: LendingPool;
  now: number;
  detail: boolean;
  authIndex?: string;
}

/** One pool as a block: title, state tags, validity, the reason, and one row per credential. */
function PoolBlock({ pool, now, detail, authIndex }: PoolBlockProps) {
  const t = useT();
  const state = stateTag(pool, t);
  const validText = pool.valid_until_ms != null
    ? t('quota.lending.valid_until', { time: formatTimeWithCountdown(pool.valid_until_ms, now, t) })
    : t('quota.lending.valid_none');
  return (
    <section className={styles.pool} data-lending-pool={pool.id} data-lending-state={pool.state}>
      <div className={styles['pool-head']}>
        <span className={styles['pool-title']}>{poolLabel(pool, t)}</span>
        <Tag tone={state.tone} data-lending-state-tag={pool.state}>{state.text}</Tag>
        {pool.is_reference && <Tag>{t('quota.lending.reference')}</Tag>}
        <span className={styles['pool-valid']}>{validText}</span>
      </div>
      {detail && authIndex && (
        <div className={styles.membership}>
          {t('quota.lending.detail_belongs', { pool: poolLabel(pool, t), n: pool.cards.length })}
        </div>
      )}
      <p className={styles.reason}>{poolReason(pool, t)}</p>
      <div className={styles.cards}>
        {pool.cards.map((card) => (
          <div
            className={styles.card}
            key={card.auth_index}
            data-lending-card={card.auth_index}
            data-lending-role={card.role}
            data-lending-current={detail && card.auth_index === authIndex ? 'true' : undefined}
          >
            <div className={styles.role}>
              <Tag tone={lendingRoleTone(card.role)} size="small">{lendingRoleLabel(card.role, t)}</Tag>
              {detail && card.auth_index === authIndex && <Tag size="small">{t('quota.lending.this_card')}</Tag>}
            </div>
            <div className={styles.name}>{card.name}</div>
            <div className={styles.metric} data-lending-metric="remaining">
              <span className={styles['metric-value']}>{remainingText(card, t)}</span>
              <span className={styles['metric-label']}>{t('quota.lending.metric.remaining')}</span>
            </div>
            <div className={styles.metric} data-lending-metric="days">
              <span className={styles['metric-value']}>{daysText(card, t)}</span>
              <span className={styles['metric-label']}>{t('quota.lending.metric.days')}</span>
            </div>
            <div className={styles.metric} data-lending-metric="daily">
              <span className={styles['metric-value']}>{dailyText(card, t)}</span>
              <span className={styles['metric-label']}>{t('quota.lending.metric.daily')}</span>
            </div>
            {card.reason_codes.length > 0 && (
              <ul className={styles.reasons}>
                {card.reason_codes.map((code) => <li key={code}>{lendingReasonLabel(code, t)}</li>)}
              </ul>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

interface UnpooledGroupRowProps {
  group: LendingUnpooledGroup;
  t: TFunc;
}

function UnpooledGroupRow({ group, t }: UnpooledGroupRowProps) {
  const [isOpen, setIsOpen] = React.useState(false);
  return (
    <div className={styles['unpooled-group']} data-lending-unpooled-group={group.code}>
      <button
        type="button"
        className={styles['unpooled-group-toggle']}
        aria-expanded={isOpen}
        onClick={() => setIsOpen((open) => !open)}
        data-lending-unpooled-group-toggle="true"
      >
        <span className={styles['unpooled-chevron']} aria-hidden="true">
          {isOpen ? <DownOutlined /> : <RightOutlined />}
        </span>
        <span>{lendingReasonLabel(group.code, t)}</span>
        <span className={styles['unpooled-group-count']}>{group.members.length}</span>
      </button>
      {isOpen && (
        <ul className={styles['unpooled-names']} data-lending-unpooled-names="true">
          {group.members.map((member) => <li key={member.auth_index}>{member.name}</li>)}
        </ul>
      )}
    </div>
  );
}

interface UnpooledSectionProps {
  lending: LendingOverview;
}

/**
 * The collapsed "not in any pool" section.
 *
 * The one-line summary counts by reason, so the operator sees why credentials were left out
 * without the long list. Opening a reason is a second, separate disclosure before any credential
 * name appears. A credential with several reasons appears once in each of its groups, while the
 * total stays the backend's distinct count.
 */
function UnpooledSection({ lending }: UnpooledSectionProps) {
  const t = useT();
  const [isOpen, setIsOpen] = React.useState(false);
  const groups = groupLendingUnpooled(lending.unpooled);
  if (lending.unpooled_count === 0) return null;
  return (
    <section className={styles.unpooled} data-lending-unpooled-section="true">
      <button
        type="button"
        className={styles['unpooled-summary']}
        aria-expanded={isOpen}
        onClick={() => setIsOpen((open) => !open)}
        data-lending-unpooled-summary="true"
      >
        <span className={styles['unpooled-chevron']} aria-hidden="true">
          {isOpen ? <DownOutlined /> : <RightOutlined />}
        </span>
        <span className={styles['unpooled-title']}>{t('quota.lending.unpooled_title', { n: lending.unpooled_count })}</span>
        {groups.map((group) => (
          <React.Fragment key={group.code}>
            <span className={styles['unpooled-sep']} aria-hidden="true">·</span>
            <span className={styles['unpooled-count']}>{lendingReasonLabel(group.code, t)} {group.members.length}</span>
          </React.Fragment>
        ))}
      </button>
      {isOpen && (
        <div className={styles['unpooled-groups']}>
          {groups.map((group) => <UnpooledGroupRow key={group.code} group={group} t={t} />)}
        </div>
      )}
    </section>
  );
}

/**
 * The read-only cross-credential advice.
 *
 * `page` draws the secondary page's body: one column of pool blocks, the unpooled section and the
 * read-only note. `detail` draws the one pool a credential belongs to inside its drawer. Neither
 * mode offers an apply, write, route-switch or entitlement-spending control.
 */
export function LendingAdviceView({ lending, mode = 'page', authIndex, nowMS, className }: LendingAdviceViewProps) {
  const t = useT();
  const detail = mode === 'detail';
  const now = nowMS > 0 ? nowMS : lending?.generated_at_ms ?? 0;
  const pools = !lending ? [] : isDetailLending(lending) ? (lending.pool ? [lending.pool] : []) : lending.pools;
  const views = pools.map((pool) => deriveLendingView(pool, now));
  const overview = lending && !isDetailLending(lending) ? lending : undefined;
  const unpooledReasons = detail && lending && isDetailLending(lending) ? lending.unpooled_reason_codes : [];
  const basisMS = views
    .map((pool) => pool.observed_at_ms)
    .filter((value): value is number => value != null)
    .sort((left, right) => left - right)[0] ?? lending?.generated_at_ms;
  const readOnly = <Tag data-lending-read-only="true">{t('quota.lending.read_only')}</Tag>;

  // A gateway that predates the field cannot say why no pool exists, so the page states the
  // absence instead of inventing pool conditions or a breakdown it does not have.
  if (!lending) {
    // A drawer without lending renders no advice block at all: a gateway that predates the field
    // and a read that returned null both mean "nothing to advise", not "here is an empty panel".
    if (detail) return null;
    return (
      <div className={clsx(styles.page, className)} data-lending-view="page" data-lending-empty="true" data-lending-absent="true">
        <div className={styles['page-head']}>{readOnly}</div>
        <EmptyState title={t('quota.lending.read_failed_title')} description={t('quota.lending.read_failed_desc')} />
        <p className={styles.note}>{t('quota.lending.read_only_note')}</p>
      </div>
    );
  }

  if (views.length === 0) {
    if (detail) {
      return (
        <Panel
          title={t('quota.lending.title')}
          extra={readOnly}
          className={clsx(styles.panel, className)}
          data-lending-view="detail"
          data-lending-empty="true"
        >
          <div className={styles.empty}>
            <strong>{t('quota.lending.empty_title')}</strong>
            <p>{t('quota.lending.empty_desc')}</p>
            {unpooledReasons.length > 0 && (
              <p>{t('quota.lending.detail_unpooled', { reasons: unpooledReasons.map((code) => lendingReasonLabel(code, t)).join(' · ') })}</p>
            )}
          </div>
          <p className={styles.note}>{t('quota.lending.read_only_note')}</p>
          <p className={styles.distinction}>{t('quota.lending.distinction')}</p>
        </Panel>
      );
    }
    return (
      <div className={clsx(styles.page, className)} data-lending-view="page" data-lending-empty="true">
        <div className={styles['page-head']}>{readOnly}</div>
        <EmptyState title={t('quota.lending.empty_title')} description={t('quota.lending.empty_desc')} />
        {overview && <UnpooledSection lending={overview} />}
        <p className={styles.note}>{t('quota.lending.read_only_note')}</p>
      </div>
    );
  }

  if (!detail) {
    return (
      <div className={clsx(styles.page, className)} data-lending-view="page">
        <div className={styles['page-head']}>{readOnly}</div>
        <div className={styles.pools}>
          {views.map((pool) => <PoolBlock key={pool.id} pool={pool} now={now} detail={false} />)}
        </div>
        {overview && <UnpooledSection lending={overview} />}
        <p className={styles.note}>{t('quota.lending.read_only_note')}</p>
      </div>
    );
  }

  return (
    <Panel
      title={t('quota.lending.title')}
      extra={readOnly}
      className={clsx(styles.panel, className)}
      data-lending-view="detail"
    >
      <p className={styles.basis}>
        {basisMS != null
          ? t('quota.lending.basis', { time: formatShortDateTime(basisMS) })
          : t('quota.lending.basis_generated')}
      </p>
      <div className={styles.pools}>
        {views.map((pool) => <PoolBlock key={pool.id} pool={pool} now={now} detail authIndex={authIndex} />)}
      </div>
      <p className={styles.note}>{t('quota.lending.read_only_note')}</p>
      <p className={styles.distinction}>{t('quota.lending.distinction')}</p>
    </Panel>
  );
}
