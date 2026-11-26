# Implementation notes: quota lending revamp (frontend)

- PRD: `docs/delivery/2026-11-25-omc-quota-lending-revamp/prd.md` (AC-9, 11, 12, 14b, 15, 18-29)
- Contract: `docs/delivery/2026-11-25-omc-quota-lending-revamp/contract.md`
- Layout reference: `docs/delivery/2026-11-25-omc-quota-lending-revamp/review.html`

## Data flow and clock

- The page, the OAuth header entry and the credential drawer all read the same
  `['management-quota']` query. No request, route or CPA write was added.
- The projection is authoritative: `role`, `tier`, `suggested_priority`,
  `needs_change`, `priority`, `priority_known`, `priority_is_default`,
  `can_edit_priority`, `needs_change_count`, `hold_count`, `outside_cards` and
  `valid_until_ms` come from the payload. The frontend does not re-derive them.
- `useVisibleNow` is used only to format the reset countdown and the "N hours ago"
  note on a stale reading. It does not re-rank, re-number or flip roles: AC-23 says
  the numbers hold until `valid_until_ms`, the earliest reset in the ranking.
  `deriveLendingView(pool, nowMS)` and the per-clock role/state arithmetic were
  deleted rather than kept behind a flag.
- `lendingBasisMS` names the oldest pool reading. The page subtitle, the entry and
  the drawer agree because they read the same overview.

## Degraded payloads

- PRD §7 defines the fallback for an unreadable account name: `lendingCardName` takes
  `display_name`, then `name`, then `auth_index`, and the sentence, card rows, basis and
  outside list all use it, so no surface shows a blank name or throws.
- PRD §7 also covers a pool without `outside_cards`: `api.getQuotaOverview` normalizes the
  missing list to an empty array once, at the boundary that hands the response to every lending
  surface. The page then omits the outside reminder and renders the rest unchanged.

## Page layout

- `QuotaLendingPage` keeps the shell: title, basis subtitle, and a back control
  only when the browser history has an entry behind it.
- `LendingAdviceView mode="page"` draws one section per pool: pool label, window
  tag, reference tag, verdict; one sentence; one `CardRow` per ranked credential;
  the folded basis; the hold fold; the outside fold; then the unpooled section.
- `CardRow` shows the name and remaining bar, the reset time with countdown,
  `now -> suggested` with the suggested number in the large type, the verdict tag,
  and "去设置" only for `needs_change === true && can_edit_priority`.
- A `hold` card never carries a suggested number. Holds are counted by primary
  reason and listed inside the "不动 N 张" fold.
- A held row's one sentence follows its primary reason (PRD 5.6). A stale reading
  whose refill is not later than the pool's `generated_at_ms` says the refill has
  passed; otherwise its age is stated in minutes below an hour and hours above.
  A below-ten card names its window and remaining share when both are present.
  The drawer's hold block calls the same helper, so the page and the drawer agree.
  A stale reading with no `observed_at_ms` says the reading time is unknown instead
  of leaving `{n}` on screen. The other placeholder sentences were audited: each
  supplies its value or falls back to a sentence without variables (`value.missing`,
  an em dash, the card name) or is not rendered without its data, so none leaks.
- A `no_advice` pool collapses to one line with its reason counts; a second fold
  lists the held card names.
- `needs_change_count === 0` says "不用改"; `is_balanced` says "差不多" and the
  reason sits in the basis.
- The basis table gives every card in one tier the same position and marks it
  tied ("第 1，并列"); a card without a tier has no ranking to show.
- `outside_cards` renders as a fold under the pool, with each card's current
  priority, default marker or unknown state.
- The basis' outside line says later numbers are 10, 20… lower than the group's
  current maximum and points at the fold below for the outside cards' numbers.
- `DisplayName` applies middle ellipsis; the full name is printed in the basis.

## OAuth list row

- The first-phase row carried a small lending-role chip. The contract now carries only
  `suggested` and `hold`, and the PRD removes the role vocabulary, so the chip had no
  label to show and was removed. The row's countdown, fast-reset mark, sort and quota
  content are unchanged.
- The header entry is the only lending surface left in the OAuth workspace (AC-14b).

## Read-only boundary

- The page header keeps the fixed "只提示，不改路由" tag beside the title in every
  state (PRD 5.3: title and tag on one line, the basis on the next). The tag is a
  sibling of the title rather than a child, so the phone rule that hides the page
  title does not hide it.
- The full sentence (`quota.lending.read_only_note`) lives inside the pool's
  "看依据" disclosure on the page, as AC-22 lists it. It is not repeated at the
  page bottom when a basis exists. The empty and absent states keep it at the
  bottom because they have no basis to open. The drawer's detail block omits it
  (AC-28) and keeps the distinction from reset advice; the drawer head already
  carries the tag.
- No apply, save, switch or entitlement-spend control exists. The only controls
  are disclosure toggles and the "去设置" link.

## 去设置 (AC-26)

- The page renders `GoToSettings` as an anchor to
  `/oauth-management?open=<authIndex>` through `oauthPrioritySettingsPath`. It
  carries no priority value and performs no write.
- The drawer passes `onEditPriority` into `LendingAdviceView mode="detail"`.
  `CardRow` then renders a button instead of the anchor. The button moves a
  `sectionRequest` token on `AuthFileDetailDrawer`, which switches the already
  open drawer to the `configuration` section where the existing priority input
  lives. No global state, no new route and no second roster request.
- The OAuth workspace parses `open` in `parseOAuthWorkspaceQuery` (bounded to
  256 characters), consumes it once through `consumeOneShotIntent`, and calls
  `openCredential(record, 'configuration')` when the roster arrives. An unknown
  index leaves the workspace unchanged; the parameter is removed so a reload
  does not reopen the drawer.
- Saving uses the existing OAuth save path, which already invalidates
  `['management-quota']`; the page and the header entry then show the new
  "now" value without a manual refresh.
- `can_edit_priority === false` on a card that needs a change shows "这张卡的优先级不能在这里改"
  and no link; a card already correct gets neither.

## Copy, typography and responsive rules

- New copy is an `[zh, en]` pair in `web/src/i18n/index.tsx`; every catalog under
  `web/src/i18n/locales/`, including `ms.ts` and `zh-Hant.ts`, carries the keys.
- Main-screen copy uses `--text-base` or larger; secondary copy uses `--text-md`.
  Every tag on the page also reads at `--text-md`, so no 10px or 12px chip remains.
  Secondary ink uses `--fg-2`; tag verdict ink is mixed toward `--fg` so it clears
  AA on its own tint. The header basis is wrapped locally in `--fg-2`; the shared
  PageHeader and the global tokens are unchanged.
- Measured on the mocked page (base, hold, outside, no-advice and cannot-edit
  states, light and dark): minimum font 13px; minimum contrast 5.02:1 in light and
  6.74:1 in dark.
- At 760px and below a card row folds into three lines: name and bar, reset,
  then `now -> suggested` with the verdict and the link. The page has no
  horizontal scroll.
- Colours use design tokens and stay legible in both themes. State is written in
  words, never colour alone.

## Deleted logic and where its claims live

| Old logic or claim | Status | New owner |
| --- | --- | --- |
| `deriveLendingView` and per-clock role/state flips | Obsolete under AC-23; the backend fixes the ranking and the numbers hold until the earliest reset | `scripts/test-quota-lending.ts` (tiers, entry, basis, detail lookup), `quota-lending` probe `clock` checks |
| One-point-per-day spread and the one-hour daily floor | Obsolete under AC-7 and AC-19; the advice is a tiered integer priority | `scripts/test-quota-lending.ts` (`lendingPoolHeadline`, `lendingPoolSentence`), `lending-advice` component balanced/keep cases |
| Frontend freshness downgrade to `reading_stale` | Moved into the backend `hold` projection | `scripts/test-quota-lending.ts` (`lendingHoldLabel`, `lendingHoldRowNote`), `lending-advice` page and drawer hold cases |
| `limited` / `unavailable` page states | Replaced by `no_advice` and the absent state | `lending-advice` no-advice/absent cases, `quota-lending` probe |
| `prefer` / `avoid` / `normal` roles | Replaced by `suggested` / `hold` plus `tier` | `lending-advice`, `scripts/test-quota-lending.ts` |
| OAuth list row lending-role chip | Removed with the old role vocabulary; the row's countdown, fast-reset and sort are untouched | `credential-quota.component.test.tsx`, `quota-lending`/`oauth-management` probes |
| Reset sort key and fast-reset level helpers | No longer used by the lending page; the list claims remain | `credential-quota.component.test.tsx` (fast-reset mark), `scripts/test-oauth-sort.ts` (sort) |
| `lendingRoleTone` / `lendingRoleLabel` | Replaced by verdict tags and hold labels | `lending-advice`, `scripts/test-quota-lending.ts` |
| Detail view drawing the whole pool block | Replaced by the single-card detail block | `lending-advice` detail cases, `credential-quota` detail section, `auth-file-drawer-section` |

## Verification

The exact gate outputs and the integration smoke screenshots are recorded in the
task progress file and the delivery report.

## Open points

- The full read-only sentence is inside the basis for a page with a ranking, and
  at the bottom for the empty/absent states. If acceptance wants it visible
  without opening the basis, that is a one-line move.
- The `quota-lending` probe installs Playwright's controlled clock only around the
  countdown assertion so the minute-granularity copy is deterministic. Product
  code does not depend on the test clock.
