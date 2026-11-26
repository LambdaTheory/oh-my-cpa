import React from 'react';
import clsx from 'clsx';
import { Link } from 'react-router-dom';
import type { TFunc } from '../../i18n';
import { useT } from '../../i18n';
import { EmptyState } from '../../components/common/EmptyState';
import { Panel } from '../../components/common/Panel';
import { Tag, type TagTone } from '../../components/common/Tag';
import { DownOutlined, RightOutlined } from '../../components/icons';
import type {
  LendingCard,
  LendingDetail,
  LendingOutsideCard,
  LendingOverview,
  LendingPool,
} from '../../types/quota';
import {
  groupLendingUnpooled,
  lendingCardName,
  lendingHoldCounts,
  lendingHoldLabel,
  lendingHoldRowNote,
  lendingOutsidePoolLabel,
  lendingPoolHeadline,
  lendingPoolSentence,
  lendingPriorityText,
  lendingRankLabel,
  lendingReasonLabel,
  type LendingNamedCard,
  type LendingUnpooledGroup,
} from './lendingAdvice';
import { formatCountdown, formatShortDateTime, quotaWindowLabel } from './quotaFormat';
import { QUOTA_LENDING_PATH, oauthPrioritySettingsPath } from '../../types/quotaLendingMount';
import styles from './LendingAdviceView.module.css';

export type LendingAdviceMode = 'page' | 'detail';

interface LendingAdviceViewProps {
  lending?: LendingOverview | LendingDetail | null;
  mode?: LendingAdviceMode;
  authIndex?: string;
  nowMS: number;
  className?: string;
  /** Drawer only: move to the existing priority field. The page links there instead. */
  onEditPriority?: () => void;
}

function isDetailLending(lending: LendingOverview | LendingDetail): lending is LendingDetail {
  return 'pool' in lending;
}

/** The name split for a middle ellipsis: the tail stays readable when the head overflows. */
function splitDisplayName(name: string): { start: string; end?: string } {
  if (name.length <= 22) return { start: name };
  return { start: name.slice(0, name.length - 10), end: name.slice(-10) };
}

function DisplayName({ card, className }: { card: LendingNamedCard; className?: string }) {
  const name = lendingCardName(card);
  const { start, end } = splitDisplayName(name);
  return (
    <span className={clsx(styles['display-name'], className)} title={name} data-lending-name={name}>
      <span className={styles['display-name-start']}>{start}</span>
      {end && <span className={styles['display-name-end']}>{end}</span>}
    </span>
  );
}

function resetText(card: LendingCard, now: number, t: TFunc): string {
  if (card.reset_at_ms == null) return t('quota.lending.value.missing');
  const approximate = card.reset_accuracy != null && card.reset_accuracy !== 'exact' ? '~' : '';
  const time = `${approximate}${formatShortDateTime(card.reset_at_ms)}`;
  const countdown = formatCountdown(card.reset_at_ms, now, t);
  return countdown
    ? t('quota.lending.card.reset_with_countdown', { time, countdown })
    : t('quota.lending.card.reset_at', { time });
}

function priorityValue(card: LendingCard, t: TFunc): string {
  if (!card.priority_known) return t('quota.lending.value.missing');
  return lendingPriorityText(card.priority);
}

type CardAction = 'change' | 'keep' | 'unknown';

function cardAction(card: LendingCard): CardAction {
  if (card.needs_change === true) return 'change';
  if (card.needs_change === false) return 'keep';
  return 'unknown';
}

function actionTag(action: CardAction, t: TFunc): { text: string; tone: TagTone } | null {
  if (action === 'change') return { text: t('quota.lending.card.change'), tone: 'warn' };
  if (action === 'keep') return { text: t('quota.lending.card.keep'), tone: 'neutral' };
  return null;
}

interface GoToSettingsProps {
  card: LendingCard;
  onEditPriority?: () => void;
  className?: string;
}

/**
 * The way to the priority field, not a write.
 *
 * The drawer already has the field, so it switches tabs in place. The page has no drawer and must
 * not fetch the credential rosters itself, so it links to the OAuth workspace with the one-shot
 * `open` intent that opens this credential's configuration section.
 */
function GoToSettings({ card, onEditPriority, className }: GoToSettingsProps) {
  const t = useT();
  if (card.needs_change !== true || !card.can_edit_priority) return null;
  if (onEditPriority) {
    return (
      <button type="button" className={clsx(styles.go, className)} onClick={onEditPriority} data-lending-go="detail">
        {t('quota.lending.card.go')}
      </button>
    );
  }
  return (
    <Link
      to={oauthPrioritySettingsPath(card.auth_index)}
      className={clsx(styles.go, className)}
      data-lending-go="page"
    >
      {t('quota.lending.card.go')}
    </Link>
  );
}

interface CardRowProps {
  card: LendingCard;
  action: CardAction;
  now: number;
  onEditPriority?: () => void;
}

/** One ranked credential: name and reading, the number now, the number to set, and the verdict. */
function CardRow({ card, action, now, onEditPriority }: CardRowProps) {
  const t = useT();
  const tag = actionTag(action, t);
  return (
    <div
      className={clsx(styles.card, styles[action])}
      data-lending-card={card.auth_index}
      data-lending-card-role={card.role}
      data-lending-action={action}
      data-lending-tier={card.tier}
    >
      <div className={styles['card-id']}>
        <DisplayName card={card} className={styles['card-name']} />
        <span className={styles['card-reading']}>
          <span className={styles['remaining-bar']} aria-hidden="true">
            <i style={{ width: `${Math.max(0, Math.min(100, card.remaining_percent ?? 0))}%` }} />
          </span>
          <span className={styles['card-caption']}>{t('quota.lending.card.remaining')}</span>
          <b className={styles['remaining-value']}>
            {card.remaining_percent == null
              ? t('quota.lending.value.missing')
              : t('quota.lending.value.percent', { n: Math.round(card.remaining_percent) })}
          </b>
        </span>
        <span className={styles['card-reset']}>{resetText(card, now, t)}</span>
      </div>
      <div className={styles['card-move']}>
        <span className={styles['card-now']} data-lending-now={card.priority_known ? card.priority : undefined}>
          <span className={styles['card-caption']}>{t('quota.lending.card.now')}</span>
          <b className={styles['card-value']}>{priorityValue(card, t)}</b>
          {card.priority_known && card.priority_is_default && (
            <span className={styles['card-default']}>{t('quota.lending.card.default')}</span>
          )}
        </span>
        <span className={styles['card-arrow']} aria-hidden="true">→</span>
        <span className={styles['card-suggest']} data-lending-suggested={card.suggested_priority}>
          <span className={styles['card-caption']}>{t('quota.lending.card.suggest')}</span>
          <b className={styles['card-value-big']}>{lendingPriorityText(card.suggested_priority)}</b>
        </span>
        <span className={styles['card-action']}>
          {tag && <Tag tone={tag.tone} data-lending-action-tag={action}>{tag.text}</Tag>}
          {action === 'change' && (
            <GoToSettings card={card} onEditPriority={onEditPriority} />
          )}
        </span>
      </div>
      {!card.priority_known && (
        <span className={styles['card-note']} data-lending-priority-unknown="true">
          {t('quota.lending.card.unknown')}
        </span>
      )}
      {action === 'change' && !card.can_edit_priority && (
        <span className={styles['card-note']} data-lending-cannot-edit="true">
          {t('quota.lending.card.cannot_edit')}
        </span>
      )}
    </div>
  );
}

interface DisclosureProps {
  id: string;
  label: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  open?: boolean;
}

/** A collapse whose label is the whole summary; state is local because jsdom cannot toggle details. */
function Disclosure({ id, label, children, className, open: initiallyOpen = false }: DisclosureProps) {
  const [isOpen, setIsOpen] = React.useState(initiallyOpen);
  return (
    <section className={clsx(styles.disclosure, className)} data-lending-disclosure={id}>
      <button
        type="button"
        className={styles['disclosure-toggle']}
        aria-expanded={isOpen}
        onClick={() => setIsOpen((open) => !open)}
        data-lending-disclosure-toggle={id}
      >
        <span className={styles.chevron} aria-hidden="true">{isOpen ? <DownOutlined /> : <RightOutlined />}</span>
        {label}
      </button>
      {isOpen && <div className={styles['disclosure-body']} data-lending-disclosure-body={id}>{children}</div>}
    </section>
  );
}

function HoldCountTags({ pool }: { pool: LendingPool }) {
  const t = useT();
  const counts = lendingHoldCounts(pool);
  return (
    <>
      {counts.map((entry) => (
        <Tag key={entry.code} size="small" data-lending-hold-reason={entry.code}>
          {lendingHoldLabel(entry.code, t)} {entry.count}
        </Tag>
      ))}
    </>
  );
}

function HoldRows({ pool, now, generatedAtMS }: { pool: LendingPool; now: number; generatedAtMS: number }) {
  const t = useT();
  return (
    <div className={styles['hold-rows']} data-lending-hold-rows="true">
      {pool.cards.filter((card) => card.role === 'hold').map((card) => {
        return (
          <div className={styles['hold-row']} key={card.auth_index} data-lending-hold-card={card.auth_index}>
            <DisplayName card={card} className={styles['hold-name']} />
            {card.reason_codes.map((code) => (
              <Tag key={code} size="small">{lendingHoldLabel(code, t)}</Tag>
            ))}
            <Tag size="small">{t('quota.lending.card.hold')}</Tag>
            {card.primary_reason && (
              <span className={styles['hold-note']}>
                {lendingHoldRowNote(card, t, {
                  generatedAtMS,
                  nowMS: now,
                  windowLabel: quotaWindowLabel(card.window_kind, undefined, t),
                })}
              </span>
            )}
          </div>
        );
      })}
    </div>
  );
}

interface OutsideSectionProps {
  cards: LendingOutsideCard[];
}

function OutsideRow({ card }: { card: LendingOutsideCard }) {
  const t = useT();
  const priority = card.priority_known
    ? card.priority_is_default
      ? t('quota.lending.outside_priority_default', { n: lendingPriorityText(card.priority) })
      : lendingPriorityText(card.priority)
    : t('quota.lending.outside_priority_unknown');
  return (
    <li className={styles['outside-row']} data-lending-outside-card={card.auth_index}>
      <DisplayName card={card} className={styles['outside-name']} />
      <span className={styles['outside-pool']}>{lendingOutsidePoolLabel(card)}</span>
      <span className={styles['outside-priority']}>{t('quota.lending.outside_now', { priority })}</span>
    </li>
  );
}

function OutsideSection({ cards }: OutsideSectionProps) {
  const t = useT();
  if (cards.length === 0) return null;
  return (
    <Disclosure
      id="outside"
      className={styles.outside}
      label={<span data-lending-outside-toggle="true">{t('quota.lending.outside_toggle', { n: cards.length })}</span>}
    >
      <ul className={styles['outside-list']}>
        {cards.map((card) => <OutsideRow key={card.auth_index} card={card} />)}
      </ul>
    </Disclosure>
  );
}

interface EvidenceProps {
  pool: LendingPool;
  now: number;
  detail: boolean;
}

/**
 * The calculation, request path and validity, folded away so the main screen stays one line per
 * credential. Opening it changes no number and asks for no data.
 */
function Evidence({ pool, now, detail }: EvidenceProps) {
  const t = useT();
  const ranked = pool.cards.filter((card) => card.role === 'suggested');
  const names = pool.cards.map((card) => {
    const displayName = lendingCardName(card);
    return card.name && card.name !== displayName
      ? t('quota.lending.evidence.name_pair', { name: displayName, file: card.name })
      : displayName;
  });
  const outsideNames = pool.outside_cards
    .map((card) => `${lendingCardName(card)} (${lendingOutsidePoolLabel(card)})`)
    .join(t('quota.lending.list_separator'));
  return (
    <Disclosure id="evidence" className={styles.evidence} label={t('quota.lending.evidence.title')}>
      <p>
        <b>{t('quota.lending.evidence.order_title')}</b>
        {t('quota.lending.evidence.order')}
      </p>
      {!detail && (
        <div className={styles['evidence-table-wrap']}>
          <table className={styles['evidence-table']} data-lending-evidence-table="true">
            <thead>
              <tr>
                <th scope="col">{t('quota.lending.evidence.table_card')}</th>
                <th scope="col">{t('quota.lending.evidence.table_reset')}</th>
                <th scope="col">{t('quota.lending.evidence.table_remaining')}</th>
                <th scope="col">{t('quota.lending.evidence.table_rank')}</th>
              </tr>
            </thead>
            <tbody>
              {ranked.map((card) => (
                <tr key={card.auth_index}>
                  <td>{lendingCardName(card)}</td>
                  <td>{resetText(card, now, t)}</td>
                  <td>{card.remaining_percent == null
                    ? t('quota.lending.value.missing')
                    : t('quota.lending.value.percent', { n: Math.round(card.remaining_percent) })}</td>
                  <td>{lendingRankLabel(pool, card, t)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p>
        <b>{t('quota.lending.evidence.numbers_title')}</b>
        {t('quota.lending.evidence.numbers')}
      </p>
      <p>
        <b>{t('quota.lending.evidence.after_title')}</b>
        {t('quota.lending.evidence.after')}
      </p>
      <p className={styles['evidence-meta']}>
        {t('quota.lending.evidence.validity', {
          time: pool.valid_until_ms != null
            ? formatShortDateTime(pool.valid_until_ms)
            : t('quota.lending.value.missing'),
          observed: pool.observed_at_ms != null
            ? formatShortDateTime(pool.observed_at_ms)
            : t('quota.lending.value.missing'),
        })}
      </p>
      {pool.is_reference && <p className={styles['evidence-meta']}>{t('quota.lending.evidence.reference')}</p>}
      {detail && <p className={styles.distinction}>{t('quota.lending.distinction')}</p>}
      {pool.outside_cards.length > 0 && (
        <p className={styles['evidence-meta']}>
          {detail
            ? t('quota.lending.evidence.outside_detail', { n: pool.outside_cards.length, names: outsideNames })
            : t('quota.lending.evidence.outside', { n: pool.outside_cards.length, names: outsideNames })}
        </p>
      )}
      {names.length > 0 && (
        <p className={styles['evidence-meta']} data-lending-full-names="true">
          {t('quota.lending.evidence.names', { names: names.join(t('quota.lending.list_separator')) })}
        </p>
      )}
      {!detail && <p className={styles.note}>{t('quota.lending.read_only_note')}</p>}
    </Disclosure>
  );
}

interface PoolBlockProps {
  pool: LendingPool;
  now: number;
  generatedAtMS: number;
  onEditPriority?: () => void;
}

function poolLabel(pool: LendingPool, t: TFunc): string {
  return t('quota.lending.pool_label', {
    provider: pool.provider,
    plan: pool.plan_label || pool.plan_type,
  });
}

/** One pool: header verdict, one sentence, one row per ranked credential, then the folded detail. */
function PoolBlock({ pool, now, generatedAtMS, onEditPriority }: PoolBlockProps) {
  const t = useT();
  const sentence = lendingPoolSentence(pool, t);
  const ranked = pool.cards.filter((card) => card.role === 'suggested');
  return (
    <section className={styles.pool} data-lending-pool={pool.id} data-lending-state={pool.state}>
      <div className={styles['pool-head']}>
        <span className={styles['pool-title']}>{poolLabel(pool, t)}</span>
        {pool.window_kind && <Tag>{quotaWindowLabel(pool.window_kind, pool.window_kind, t)}</Tag>}
        {pool.is_reference && <Tag data-lending-reference="true">{t('quota.lending.reference')}</Tag>}
        <Tag
          tone={pool.needs_change_count > 0 ? 'warn' : 'neutral'}
          className={styles['pool-verdict']}
          data-lending-pool-verdict={pool.needs_change_count > 0 ? 'change' : 'keep'}
        >
          {lendingPoolHeadline(pool, t)}
        </Tag>
      </div>
      <p className={styles.say} data-lending-sentence="true">
        {sentence.text}
        {sentence.note && <small className={styles['say-note']}>{sentence.note}</small>}
      </p>
      <div className={styles.cards}>
        {ranked.map((card) => (
          <CardRow
            key={card.auth_index}
            card={card}
            action={cardAction(card)}
            now={now}
            onEditPriority={onEditPriority}
          />
        ))}
      </div>
      <Evidence pool={pool} now={now} detail={false} />
      {pool.hold_count > 0 && (
        <Disclosure
          id="hold"
          className={styles.hold}
          label={(
            <>
              <span data-lending-hold-toggle="true">{t('quota.lending.hold_toggle', { n: pool.hold_count })}</span>
              <HoldCountTags pool={pool} />
            </>
          )}
        >
          <HoldRows pool={pool} now={now} generatedAtMS={generatedAtMS} />
        </Disclosure>
      )}
      <OutsideSection cards={pool.outside_cards} />
    </section>
  );
}

/** A pool where no credential could be ranked: one line until the reasons are opened. */
function NoAdvicePool({ pool, now, generatedAtMS }: { pool: LendingPool; now: number; generatedAtMS: number }) {
  const t = useT();
  return (
    <section className={styles['no-advice']} data-lending-pool={pool.id} data-lending-state="no_advice">
      <Disclosure
        id="no-advice"
        label={(
          <>
            <span className={styles['pool-title']}>{poolLabel(pool, t)}</span>
            <Tag data-lending-pool-verdict="none">{t('quota.lending.pool_no_advice')}</Tag>
            <span className={styles['no-advice-count']}>
              {t('quota.lending.pool_all_hold', { n: pool.cards.length })}
            </span>
            <HoldCountTags pool={pool} />
          </>
        )}
      >
        <HoldRows pool={pool} now={now} generatedAtMS={generatedAtMS} />
      </Disclosure>
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
        <span className={styles.chevron} aria-hidden="true">{isOpen ? <DownOutlined /> : <RightOutlined />}</span>
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

/**
 * The collapsed "not in any pool" section, unchanged from the first release: the summary counts by
 * reason, and a second disclosure lists names.
 */
function UnpooledSection({ lending }: { lending: LendingOverview }) {
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
        <span className={styles.chevron} aria-hidden="true">{isOpen ? <DownOutlined /> : <RightOutlined />}</span>
        <span className={styles['unpooled-title']}>{t('quota.lending.unpooled_title', { n: lending.unpooled_count })}</span>
        {groups.map((group) => (
          <React.Fragment key={group.code}>
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

interface DetailBlockProps {
  detail: LendingDetail;
  authIndex: string;
  now: number;
  onEditPriority?: () => void;
}

/**
 * The drawer's block: only this credential's own numbers, never the whole pool again.
 *
 * It answers "what is it now, what should it be", or why it is not ranked; the pool itself, its
 * table and the request path stay on the lending page.
 */
function DetailBlock({ detail, authIndex, now, onEditPriority }: DetailBlockProps) {
  const t = useT();
  const pool = detail.pool;
  const card = pool?.cards.find((entry) => entry.auth_index === authIndex);
  const readOnly = <Tag data-lending-read-only="true">{t('quota.lending.read_only')}</Tag>;
  if (!pool || !card) {
    return (
      <Panel
        title={t('quota.lending.title')}
        extra={readOnly}
        className={styles.panel}
        data-lending-view="detail"
        data-lending-detail-state="outside"
      >
        <p className={styles.say}>{t('quota.lending.detail_outside')}</p>
        {detail.unpooled_reason_codes.length > 0 && (
          <p className={styles['detail-reason']}>
            {t('quota.lending.detail_outside_reason', {
              reasons: detail.unpooled_reason_codes.map((code) => lendingReasonLabel(code, t)).join(t('quota.lending.list_separator')),
            })}
          </p>
        )}
        <p className={styles['detail-link']}>
          <Link to={QUOTA_LENDING_PATH} data-lending-detail-link="true">{t('quota.lending.detail_open_all')}</Link>
        </p>
      </Panel>
    );
  }

  const onHold = card.role === 'hold';
  const action = onHold ? undefined : cardAction(card);
  return (
    <Panel
      title={t('quota.lending.title')}
      extra={readOnly}
      className={styles.panel}
      data-lending-view="detail"
      data-lending-detail-state={onHold ? 'hold' : action}
    >
      {onHold ? (
        <>
          <p className={styles.say}>
            {t('quota.lending.detail_hold', { name: lendingCardName(card), reason: lendingHoldLabel(card.primary_reason, t) })}
          </p>
          <p className={styles['detail-reason']}>
            {lendingHoldRowNote(card, t, {
              generatedAtMS: detail.generated_at_ms,
              nowMS: now,
              windowLabel: quotaWindowLabel(card.window_kind, undefined, t),
            })}
          </p>
          <div className={styles.cards}>
            <div className={styles['detail-hold-row']} data-lending-card={card.auth_index} data-lending-card-role="hold">
              <DisplayName card={card} className={styles['card-name']} />
              <span className={styles['card-reset']}>{resetText(card, now, t)}</span>
              <span className={styles['card-now']}>
                <span className={styles['card-caption']}>{t('quota.lending.card.now')}</span>
                <b className={styles['card-value']}>{priorityValue(card, t)}</b>
                {card.priority_known && card.priority_is_default && (
                  <span className={styles['card-default']}>{t('quota.lending.card.default')}</span>
                )}
              </span>
              <Tag data-lending-action-tag="hold">{t('quota.lending.card.hold')}</Tag>
            </div>
          </div>
        </>
      ) : (
        <>
          <p className={styles.say} data-lending-detail-sentence="true">
            {card.needs_change === false
              ? t('quota.lending.detail_suggested_keep', {
                name: lendingCardName(card), n: lendingPriorityText(card.suggested_priority),
              })
              : t('quota.lending.detail_suggested', {
                name: lendingCardName(card), n: lendingPriorityText(card.suggested_priority),
              })}
          </p>
          <div className={styles.cards}>
            <CardRow card={card} action={action ?? 'unknown'} now={now} onEditPriority={onEditPriority} />
          </div>
        </>
      )}
      <Evidence pool={pool} now={now} detail />
      <p className={styles['detail-link']}>
        <Link to={QUOTA_LENDING_PATH} data-lending-detail-link="true">{t('quota.lending.detail_open_page')}</Link>
      </p>
    </Panel>
  );
}

/**
 * The read-only cross-credential advice.
 *
 * `page` draws the secondary page's body: one column of pools and the unpooled section.
 * `detail` draws only the credential's own line and evidence inside its
 * drawer. Neither mode offers an apply, write, route-switch or entitlement-spending control.
 */
export function LendingAdviceView({
  lending,
  mode = 'page',
  authIndex,
  nowMS,
  className,
  onEditPriority,
}: LendingAdviceViewProps) {
  const t = useT();
  const detail = mode === 'detail';
  const now = nowMS > 0 ? nowMS : lending?.generated_at_ms ?? 0;

  if (detail) {
    // A gateway without the field and a failed read both mean "nothing to advise here"; the
    // drawer shows no block instead of an empty panel.
    if (!lending || !isDetailLending(lending)) return null;
    if (!authIndex) return null;
    return (
      <DetailBlock
        detail={lending}
        authIndex={authIndex}
        now={now}
        onEditPriority={onEditPriority}
      />
    );
  }

  // A gateway that predates the field cannot say why no pool exists, so the page states the
  // absence instead of inventing pool conditions or a breakdown it does not have.
  if (!lending) {
    return (
      <div className={clsx(styles.page, className)} data-lending-view="page" data-lending-empty="true" data-lending-absent="true">
        <EmptyState title={t('quota.lending.read_failed_title')} description={t('quota.lending.read_failed_desc')} />
        <p className={styles.note}>{t('quota.lending.read_only_note')}</p>
      </div>
    );
  }

  const overview = isDetailLending(lending) ? undefined : lending;
  const pools = overview?.pools ?? (isDetailLending(lending) && lending.pool ? [lending.pool] : []);
  if (pools.length === 0) {
    return (
      <div className={clsx(styles.page, className)} data-lending-view="page" data-lending-empty="true">
        <EmptyState title={t('quota.lending.empty_title')} description={t('quota.lending.empty_desc')} />
        {overview && <UnpooledSection lending={overview} />}
        <p className={styles.note}>{t('quota.lending.read_only_note')}</p>
      </div>
    );
  }

  return (
    <div className={clsx(styles.page, className)} data-lending-view="page">
      <div className={styles.pools}>
        {pools.map((pool) => (
          pool.state === 'no_advice'
            ? <NoAdvicePool key={pool.id} pool={pool} now={now} generatedAtMS={lending.generated_at_ms} />
            : <PoolBlock key={pool.id} pool={pool} now={now} generatedAtMS={lending.generated_at_ms} onEditPriority={onEditPriority} />
        ))}
      </div>
      {overview && <UnpooledSection lending={overview} />}
    </div>
  );
}
