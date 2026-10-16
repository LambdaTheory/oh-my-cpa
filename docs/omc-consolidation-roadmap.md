# OMC Consolidation Roadmap

| Field | Content |
| --- | --- |
| Document | OMC consolidation roadmap (capability absorption, route adjustment) |
| Version | v0.2 — first batch approved for local implementation |
| Date | 2026-10-15 |
| Owner | — (prediction side and data side, jointly) |
| Status | First batch approved for local implementation and verification; not a release authorization |
| Primary input | `predict-omc-conclusions-handoff-2026-10-15.md` |
| Corrected input | `predict-data-delivery-status-2026-10-15.md` (older status; the primary input supersedes it where they conflict) |
| Relation to existing docs | A route-change supplement. It does not silently override any existing PRD or observation spec; where they disagree, the primary input decides. |

> The approved first batch may be implemented and verified locally. This is **not** an authorization to commit, push, open a PR, merge, deploy, or perform any production write or production access.

Evidence tags used below: **Fact** (locally verified), **Report** (code/report based, not re-run), **Unknown** (not confirmable). These stay separate; a merged pull request is not itself proof that production runs it.

## 1. Background

The goal of the quota-prediction work has not changed: identify a quota shortage or an idle subscription early, help a person or an agent use the subscription more fully, and avoid needless work. Every change to a subscription must rest on evidence. What changes is the route: the primary data-and-analysis platform moves from CPA-buddy's prediction feature to Oh My CPA (OMC), which already holds the quota data and a built-in agent. A corrected finding is that OMC is not missing real data — its built-in agent can already read real historical observations, usage and error records. This document records the agreed route, its boundaries and its open questions; it is not a settled implementation PRD.

## 2. Goal and non-goals

### 2.1 Goal (unchanged)

| Goal | Measure |
| --- | --- |
| Identify shortage or idle early | A read-only SOP flags a window before its reset when the evidence supports it |
| Use subscriptions more fully | Findings distinguish "likely to stop early" from "under-used", reported separately |
| Evidence-based adjustment | Every conclusion separates current fact, scenario projection and not-obtainable |

### 2.2 Non-goals (this route does not do)

- No commit, push, PR, merge, deploy or production access under this approval.
- No default second service, second database or duplicated collection when absorbing a capability.
- No assumption that a stopped or paused capability is deleted.
- Do not move the OMC client, fallback paths, duplicate collection or the JSONL state implementation.

## 3. Current state

| Area | State | Tag |
| --- | --- | --- |
| OMC PR #3 / #4 / #5 | User reports redeployed; page returns 200 and an in-container key `initialize` succeeds | Fact |
| Production SHA, scheduler, backfill | Exact SHA, whether scheduling runs and whether backfill completed are unconfirmed | Unknown |
| cpab source + denoise PR | User confirms it merged, but it is stopped | Report |
| Real sample | One 1/24 shard, exactly 200 rows, `codex / weekly / passive`, `observed` span ≈ 67.6 minutes, write delay ≈ 26 h | Fact |
| Sample representativeness | Not the full database, not a complete cycle, no precision claim | Fact |
| Sample provenance | The export transform hard-codes a single account identity; the guarantee that these rows share one account depends on the export side | Fact |
| Prior session answer | Claims no credential exhausts before reset, yet its own figures give 87 h < 167 h reset; a self-contradiction, not counted as a repeated error | Report |

The single 1/24 shard, its 200 rows and its short span are enough to describe fields, not enough to judge accuracy. Full coverage is only needed when accuracy is actually evaluated; the first SOP pass must not stall on a full 24-interaction export.

## 4. Capability disposition

| Item | Disposition |
| --- | --- |
| CPAb as an independent service | **Permanently stopped**; assets, code and data retained — not deleted, and no rerun required |
| Independent cpab MCP, whole-program migration, new database, prediction auto-push | Not pursued |
| First batch: account auto-management, Ollama, Feishu | Approved for **local implementation and verification**; see `docs/omc-first-batch-spec.md` |
| Read-only analysis SOP, evidence reconciliation, prediction assets | Continue |
| OMC aggregation tools | Add on demand |
| Capacity estimation | Passive-first; agent key allocation deferred |
| Hidden playground local UI changes | Being wrapped up; MCP and agent approval are untouched |

Absorbing a capability into OMC means moving the capability, not the program: OMC's SQLite stays the single store, and retention rebuildability holds only within the original coverage. CPAb is stopped permanently as an independent service, but its assets, code and data are retained and are not required to run again. Its capability is absorbed by actual capability, not by moving the OMC client, fallback paths, duplicate collection or the JSONL state implementation. Existing CPA-buddy's `predict` and `backtest` code stays a baseline and test asset. Deterministic calculation tools are reconsidered only after a repeated error or clear cost evidence appears.

## 5. Phased roadmap and acceptance gates

| Phase | Work | Dependency / gate |
| --- | --- | --- |
| 1. Evidence alignment + read-only SOP | Check which data is already available; run the built-in agent's read-only SOP without new scheduling | Enter before any tooling; must not block on a full export |
| 2. First batch (parallel) | Account auto-management, Ollama and Feishu — local implementation and verification approved | See `docs/omc-first-batch-spec.md`; no deploy or production enablement |
| 3. Further capability migration | Move remaining tools and notifications into OMC item by item, each after approval | Each item needs its own approval and a parity result |
| 4. Daily automation | Daily analysis, result recording, agent handling of to-dos | Needs a separate later approval |

Phase 1 produces a fixed as-of convention, per-window sample age and segmented facts; its gate is that conclusions separate current fact, scenario projection and not-obtainable. Phase 3 migrates nothing until the parity check says the OMC path covers what is being left behind.

### 5.1 First batch (approved)

Account auto-management, Ollama and Feishu are approved as the first batch of local implementation and verification. The approved requirements, interface draft, migration boundary and planned test evidence live in `docs/omc-first-batch-spec.md`. This approval does not authorize commit, push, PR, merge, deploy, production access or production enablement.

## 6. Boundaries, exceptions, compatibility, rollback

- **Data contract**: do not change frozen history fields; no production read authorization is granted yet; keep raw and derived fields distinct; keep millisecond precision and separate `observed_at` from `created_at`; `window_kind` is a storage-identity candidate, not a guaranteed identity for every provider; a missing measurement is not a dormant state; prefer standard weekly windows. Ollama sits outside the CPA data sources and is the exception to that assumption.
- **No data migration**: no new database and no duplicated collection; OMC SQLite remains the single store.
- **Agent boundary**: `database_query` is read-only SQLite, at most 200 rows and 5 s, callable only by the built-in agent; there is no independent SOP/skills auto-load configuration, and saving a chat is not a system rule; the agent has no scheduled entry, although OMC already schedules quota background work; conversation records are not a structured persistence of prediction, actual and method version. SQL algebra plus a fixed-conclusion comparison are usable SOP measures; a prompt is not a hard guarantee. Tools need no extra service but do need a build and deploy.
- **Compatibility / rollback**: migrations stay forward-only; retiring Feishu, alerting, cooldown or Ollama requires a parity check first. Pausing is reversible; deletion is not assumed.

## 7. Ownership and handoff

- **Prediction side**: write the SOP and the deterministic-calculation contract; do not build a collector and do not require a second database.
- **Data side**: confirm run status and, within available permissions, expose read-only, stable identity, availability and event information.
- **Data side reports back**: how OMC stores the built-in agent's SOP/skills, and whether structured calculation tools plus daily automation and record persistence are feasible. No new integration is requested now.

First-batch scope and its interface draft: `docs/omc-first-batch-spec.md`. Relevant OMC references: `docs/adr/0094-passive-quota-observations-and-read-only-mcp-key.md`, `docs/adr/0095-per-account-quota-refresh-scheduling-and-429-backoff.md`, `docs/agent-capabilities.md`, `migrations/041_quota_observations.sql`, `migrations/042_quota_observation_source.sql`, `internal/repository/quota.go`, `internal/operations/quota_observations.go`, `internal/quota/observations.go`.

## 8. Open questions

1. The 95% target is only a relayed value from a prediction report; whether 100% is reachable is still undetermined.
2. The hosting method for the fixed SOP, and daily automation, each need a separate approval.
3. Production run confirmations: exact SHA, scheduler execution, backfill coverage; and full real coverage statistics.
4. Integration owner and migration number for the first batch.
5. ADR coordination for the consolidation decision (see `docs/omc-first-batch-spec.md` §7).
6. Line-specific items to verify: see `docs/omc-first-batch-spec.md` §2.3, §3.3, §4.3.

## 9. Authorization boundary

This approval covers **local implementation and verification** only. It does not authorize commit, push, pull request, merge, deploy, production access or production enablement, nor the retirement of any capability still under a parity check. The old PRD and observation specs remain in force for anything this document does not explicitly change.
