# OMC First-Batch Specification

| Field | Content |
| --- | --- |
| Document | OMC first-batch specification (account auto-management, Ollama, Feishu) |
| Version | v0.1 — approved scope, for local implementation and verification |
| Date | 2026-10-15 |
| Owner | — (integration owner to be assigned) |
| Status | Approved scope. Local implementation and verification only — not a release authorization |
| Parent route | `docs/omc-consolidation-roadmap.md` |
| Relation to existing docs | Supplements the roadmap and existing PRDs. It does not override an old PRD or spec; where they conflict, the latest approved scope decides. |

> This document authorizes **local implementation and verification only**. It does **not** authorize commit, push, PR, merge, deploy, production access, or enabling any of this in production. A planned test is not a passing test until it runs.

Every statement below carries one classification:

| Tag | Meaning |
| --- | --- |
| **Confirmed** | A user-approved requirement; do not change it silently. |
| **Assumption** | A reasonable implementation assumption filling a gap the user did not specify; reviewable. |
| **To verify** | Must be checked against code or infrastructure before relying on it. |

## 1. Purpose

CPA-buddy (cpab) stops permanently as an independent service. Its assets, code and data are retained — not deleted, and not required to be re-run. Its capability is absorbed into OMC by actual capability, not by moving the OMC client, fallback paths, duplicate collection, or the JSONL state implementation. This document scopes the first batch of that absorption: **A** account auto-management, **B** Ollama, **C** Feishu.

## 2. Line A — Account auto-management

### 2.1 Confirmed

- Adoption is a **one-time batch selection** of the account list; **new accounts default to not adopted** (excluded).
- A user-specified set of existing accounts that were **manually disabled because of quota exhaustion** may be adopted (first takeover).
- Disable **only** on explicit, new **account-level 7d** evidence of "used 100% / remaining 0".
- These must **not** trigger a disable: the 5h window, model-level or group-level exhaustion, an ordinary 429, an authentication error, or a collection failure.
- Enable happens **directly at the recorded 7d absolute reset time**; no pre-enable probe is required.
- If the enable fails when it is due, back off and retry automatically, **persisted across restart**; the state becomes complete only when CPA success is confirmed.
- After a reset, the **same-cycle stale cache must not re-close** the account; a **new** exhaustion evidence is required.
- A reset time that is already in the past, or missing, does **not** create a new date.
- **Missing reset** ⇒ do not auto-disable; show that the recovery time is missing; the user may hand-fill a **one-time** recovery time to take over.
- No explicit account-level 7d evidence, or no controllable CPA credential ⇒ **monitor only, marked not-applicable**.
- All providers are shown uniformly, but it is **not promised** that every one can actually block requests.
- Each account has an auto-management switch; turning it off cancels recovery and retry.
- Races require **version check and idempotency**; the user does not confirm every action.
- The intent of a **manual external CPA change cannot be inferred from `disabled` alone**; this limitation is recorded.
- A **global emergency-off env** takes priority; it is treated as pausing automatic control and must not continue modifying CPA.

### 2.2 Assumptions

- The per-account switch defaults to **off**, consistent with new-account default exclusion.
- Persistence uses the existing OMC SQLite store; no new database.
- The retry backoff is bounded (exact schedule chosen during implementation).
- "Controllable CPA credential" means the writable management-`api-call` credential already modeled by OMC.
- Batch selection is recorded as a snapshot so the adopted set is auditable.

### 2.3 To verify

- Whether OMC's existing per-account quota scheduling provides the hook for this without a new loop.
- Whether the CPA management `api-call` path can reliably target `disabled` in production, and what success signal it returns.
- Whether account-level 7d evidence is derivable from existing observations without new collection.

## 3. Line B — Ollama

### 3.1 Confirmed

- First version reuses the cpab ollama cookie web/settings collection approach.
- Multi-account on the web: add, update, delete, enable/disable collection, and status display.
- Unified into the main OAuth workspace: Ollama accounts appear alongside CPA credentials with top-level provider filter tabs, search by label, status indicators, and session/weekly quota display on desktop and mobile.
- Strict action isolation: CPA batch actions and single actions (download, model rules, CPA quota refresh, cooldown clear, credit redeem) do not target Ollama accounts. Row switch toggles periodic scraper collection only.
- Cookie is **encrypted at rest**; the secret must not be echoed in DTOs or logs and must not enter the Agent.
- The user **manually inputs** existing cookies; do **not** auto-read an old env or the DB.
- First version keeps the **real used%** and a **relative reset derived mark**; missing fields must **not** be filled with 0 or 100.
- No API-key requirement, no plugin install, no inference proxy; the official API is a **deferred** alternative.
- When Ollama is not in the CPA control chain it is **monitor-only**; it must not claim to disable Ollama's external inference.

### 3.2 Assumptions

- Cookies are encrypted with the same master-key mechanism OMC already uses for credentials.
- Collection reuses OMC's existing scheduling interval rather than adding a new scheduler.

### 3.3 To verify

- The exact cpab ollama cookie endpoint/field shape and which fields the cookie flow actually yields.
- Where the cookie secret can be encrypted at rest without a new database.
- That the collector can be tested against a local mock (`httptest`-style) without external network.

## 4. Line C — Feishu

### 4.1 Confirmed

- First batch reuses the old bot **long connection**: group @ and private chat for help, status, exception and channel queries.
- Queries are **read-only** against current OMC data and mark **freshness**; they must not trigger an upstream refresh; cards reuse the denoise logic.
- Confirmed denoise rules:
  - Immediate notify: a credential is invalid; **all** channels unavailable with the earliest recovery later than 30 min; continued no valid data for 30 min.
  - A problem notifies **once, immediately**; recovery is not immediate and is not repeatedly nagged.
  - Pending items are **persisted**.
  - Quiet default **23:00–08:00**, configurable; at quiet-end, still-unrecovered items are merged; if all recovered, nothing is sent.
  - Scheduled default **10:00 / 17:00**; send only when there are to-dos, or channels are tight (best available account-window remaining < 20%).
  - Counts must **not** go stale.
  - A **bootstrap exception** may send (the cpab cold-start card need not be copied; e.g. new-OMC bootstrap necessity is pending implementation design — do not invent a high-frequency start push).
- A **single** model group exhaustion is **not** whole-channel unavailable; invalid data is **not** exhaustion.
- Web config: the Feishu secret is encrypted; env can **force-disable notifications/auto-management** and **override** the Feishu connection config; the web shows the **override source**.
- A module failure must **not** fail OMC process startup; no unauthenticated backdoor is introduced.
- External monitoring of a main-process failure is **not this period**.
- Feishu **Agent is entirely deferred**; the intent to auto-transfer complex items to the Agent and to follow the web model is retained, but permissions and session model are undecided — do not implement arbitrarily.
- The prediction SOP keeps priority: do **not** move the predict kernel, the scheduled model, or auto analysis.

### 4.2 Assumptions

- The 10:00/17:00 cadence and quiet window reuse OMC's existing notification scheduling.
- Card rendering reuses the existing card components and denoise code.

### 4.3 To verify

- Whether OMC already holds a long-connection bot client or needs a new adapter.
- The exact source of the "channel tight" signal (account-window remaining < 20%).
- Long-connection reconnect semantics and how the quiet window is scheduled.

## 5. Interface draft

Concrete route names are an implementation concern; the authorization boundary is what matters.

| Intent | Access | Auth |
| --- | --- | --- |
| Read auto-management accounts and per-account state | HTTP read | Existing console session auth |
| Read Ollama accounts and collection status | HTTP read | Existing console session auth |
| Read Feishu pending items and current config | HTTP read | Existing console session auth |
| Set per-account auto-management switch; batch adopt; hand-fill one-time recovery time | HTTP write (management) | Existing console session auth |
| Ollama account add/update/delete and enable/disable | HTTP write (management) | Existing console session auth |
| Save Feishu config (encrypted secret) | HTTP write (management) | Existing console session auth |
| Any MCP exposure | MCP read-only | Existing read-only MCP key — **no write permission** |

- All management writes stay behind the existing console authentication; the MCP read-only key has no write path.
- Emergency-off env variables force-disable notifications and auto-management and take precedence over web config; the web surfaces which source is in effect.

## 6. Database and migration

- **Migration numbers are assigned by the integration owner:** `043_account_automation.sql`
  (account auto-management), `044_ollama_accounts.sql` (Ollama accounts) and
  `045_feishu_notifications.sql` (Feishu notifications).
- New persisted state (per-account auto-management, Ollama accounts with encrypted
  cookies, Feishu settings, the encrypted app secret, pending items and the outbox)
  uses the existing OMC SQLite store; no new database.
- Migrations stay forward-only per the repository rule. Every new table declares its
  lifecycle and its readable/hidden classification.

## 7. ADR

The account-auto-management control semantics were accepted as
[ADR 0096](adr/0096-account-auto-management-weekly-exhaustion.md) (account-level
weekly exhaustion disable and direct reset-time enable). Line B and line C are
recorded in `docs/ollama-monitoring.md` and `docs/feishu-notifications.md`; the
single-platform absorption decision remains with the parent session.

## 8. Test evidence matrix (planned — none verified yet)

| Evidence | Covers |
| --- | --- |
| Virtual-clock 7d state machine + mock CPA | On-time direct enable; an old reading does not re-close; cancel race; restart failure retry |
| SQLite migration lifecycle + read-only schema whitelist | Forward-only migration; the read-only path cannot see hidden or unredacted columns |
| Ollama `httptest`-style mock + secret redaction | Collection against a local mock; the cookie secret is absent from DTOs, logs and Agent input |
| Feishu simulated event / card validation | Card and denoise rules; assert **no production send** occurs |
| Config override isolation | Env force-off and override precedence; the web shows the effective source |
| UI component tests + local build / e2e | Web add/update/delete and switch flows; local build and end-to-end wiring |

The rows above have been implemented as tests in the worktree (see
`docs/testing.md`); none is a production result.

## 9. Out of scope / deferred

- Ollama official API integration; Ollama as a CPA-controlled block; any inference proxy.
- Feishu Agent, complex auto-transfer to the Agent, and any permission/session model for it.
- Moving the predict kernel, scheduled models or auto analysis.
- The OMC client, fallback paths, duplicate collection and the JSONL state implementation are not moved.

## 10. Open items

1. Integration owner assignment and migration number: assigned as `043`–`045` (see §6).
2. ADR coordination with the parent session: the account-auto-management semantics are
   [ADR 0096](adr/0096-account-auto-management-weekly-exhaustion.md); the single-platform
   absorption decision remains open with the parent session.
3. The line-specific **To verify** items in §2.3, §3.3 and §4.3: the account line's
   evidence/control questions are answered by ADR 0096; the Ollama and Feishu questions
   are answered in `docs/ollama-monitoring.md` and `docs/feishu-notifications.md`. The
   real-network behaviour of both modules remains unverified from this worktree.
