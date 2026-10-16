# ADR 0096: Account auto-management disables only on weekly exhaustion and enables at the recorded reset

- Status: Accepted
- Date: 2026-10-15
- Amends: ADR 0094's and ADR 0095's quota observation timeline (it consumes the
  standard weekly reading but adds no collection of its own).

## Context

The OMC first batch absorbs CPA-buddy's account auto-management: stop using a
credential when its account-level weekly (7d) quota is exhausted, and start using it
again when the cycle resets. CPA-buddy did this against its own JSONL state and its
own client; OMC has to do it against the quota observations it already stores
(ADR 0094, ADR 0095) and CPA's own credential state.

The hard part is not the write. It is deciding when the write is warranted, and not
doing it otherwise:

- A provider's exhaustion is stated at several scopes. Claude reports a five-hour
  window, a weekly total and model-scoped weekly windows; Codex reports a primary
  and a secondary window whose length is not fixed - its reserve limit can arrive as a
  weekly primary. "The 5h window ran out" and "the Opus weekly window ran out" are not
  "the account is out of weekly quota".
- A `429`, an authentication failure or a collection failure says nothing about a
  weekly window, and disabling a credential on one would remove capacity that is
  still there.
- A weekly reading that is already old, whose stated reset is in the past, or that
  belongs to a cycle already handled is not new evidence. Acting on it re-closes a
  credential that has since reset, which is the failure a naive "disable while used
  == 100" loop produces.
- OMC's own operator can disable a credential by hand, and so can anyone with the
  management key. `disabled` alone cannot say who did it or why.

## Decision

Add `internal/automation`, a background manager and a small console surface, backed
by `account_automation_accounts`, `account_automation_batches` and
`account_automation_settings` (migration 043). A credential is under automatic control
only when an operator adopts it into the roster; a credential discovered later is not
adopted by default, and an account that was already disabled is adopted explicitly and
is never reported as disabled by this feature.

**Disable only on explicit account-level weekly exhaustion evidence.** The reading must
be a standard-scope window whose stated period is exactly a real seven days (10080
minutes, not a 168h ± 1h near-miss), from a provider whose account-level weekly window
OMC has verified, at a used share of exactly 100% with a reset still in the future,
observed within `OMCPA_ACCOUNT_AUTOMATION_MAX_EVIDENCE_AGE` (default 24h), newer than the
account's evidence watermark, and in a cycle the account has not already closed. A
five-hour window, a model- or group-scoped window, a partial reading, an ordinary 429, an
authentication failure or a collection failure can never trigger it. A missing or
already-passed reset produces no disable; the console shows that the recovery time is
missing, and the operator may hand-fill a one-time absolute recovery time.

**A provider is acted on only through a verified capability.** OMC keeps a registry of
the account-level weekly window it has verified for a provider: the window kind the
provider publishes it under and the exact period it meters. Claude's window is known by
its own mapping (the `seven_day` id, normalized to the shared `weekly` kind); Codex's by
the real duration it meters. The registry currently holds these two, and a provider is
added deliberately once its window is confirmed - it is a capability registry, not a
permanent two-provider whitelist. A provider not in it has no verified mapping yet and
stays monitor-only (`unverified_account_weekly_capability`); an unknown provider that
publishes a weekly 100 is not acted on. Every acting path shares one admission rule that
checks the whole identity chain at once - the roster account, the weekly reading and the
live gateway credential must name the same auth index and provider, and that provider
must own the verified capability whose kind and exact period the reading states. A
disagreement is monitored as `evidence_identity_mismatch`, so a credential whose provider
changed under the same auth index is never closed against, nor enabled from, another
provider's reading.

**Enable directly at the recorded reset.** The disable records the cycle's absolute
reset as `cycle_reset_at_ms` (or the operator's one-time `manual_reset_at_ms` takes
precedence). When it arrives, OMC calls CPA's status patch with `disabled=false` and
no pre-enable probe. The plan is complete only once CPA has answered; a failed enable
persists `next_attempt_at_ms` and an attempt count, and the retry resumes after a
restart instead of firing again immediately. A one-time manual reset is consumed by the
enable it schedules.

**A first takeover plans the recovery but records no disable.** Adopting a credential
that is already disabled upstream with the switch on is the documented first takeover.
It passes the same admission a disable would - the account, the reading and the live
credential share one identity, the provider owns a verified account-level weekly
capability, and the newest standard weekly reading is at 100% with a reset still in the
future - and OMC persists that reset as `cycle_reset_at_ms`
together with the evidence watermark (`disable_observed_at_ms` and `cycle_key`) in the
adoption itself, so the account is enabled directly at the reset without a pre-enable
probe. Adoption performs no disable: it writes no `last_disable_at_ms`, so the account is
never reported as disabled by OMC, and it never sends a status patch. A reading that is
partial, stale, already expired or has no reset yields no plan; the account is shown as
`missing_reset` and the operator may hand-fill a one-time recovery time. Re-adopting an
account already on the roster keeps the plan it already had. A Claude account-level
weekly window is the same seven-day window whether it is stored under the normalized
`weekly` kind or under the provider's own `seven_day` id, and both are accepted.

**An evidence watermark stops a closed cycle from re-closing.** `disable_observed_at_ms`
and `cycle_key` (the credential, provider and reset instant) are stored with every
disable. A later pass needs a strictly newer reading and a different cycle. If CPA
reports the credential enabled again while OMC had disabled it, OMC clears the plan
without re-disabling it from the old reading and without inferring who changed it.

**One shared service, one per-account lock, one intent version.** The HTTP routes and
the background pass use the same `automation.Service`. Every per-account mutation runs
under a keyed lock and is guarded by `intent_version`, which an operator action bumps;
the loop re-reads the row inside the lock immediately before the CPA write. A cancel
that has returned therefore cannot be followed by the loop turning the credential back
on, and turning an account off clears its pending enable or retry.

**The master switch is an environment variable, and unset is not false.**
`OMCPA_ACCOUNT_AUTOMATION_ENABLED` defaults off. The console can turn automation on
while the variable is unset; an explicit `false` is an out-of-band emergency off that
the web switch cannot override, and an explicit `true` forces it on. The console shows
which source is in effect. The loop is never started in demo mode, and the read-only MCP
key reaches only the summary tool, never a write route.

## State and monitor-reason vocabulary

`account_automation_accounts.state` is the plan's coarse phase and `monitor_reason` names
why an account has no automatic control. Both are canonical API values, returned by
`GET /api/v1/account-automation` and the read-only `account_automation_status` tool; a
client must not invent its own spelling (there is no `EXHAUSTED_MISSING_RESET`).

- `state`: `idle`, `monitoring`, `disabled`, `waiting`, `error`.
- `monitor_reason`: `not_adopted`, `switch_off`, `no_controllable_credential`,
  `no_weekly_evidence`, `not_exhausted`, `missing_reset`, `expired_reset`, `stale_evidence`,
  `backoff`, `recovery_scheduled`, `externally_disabled`, `externally_re_enabled`,
  `unverified_account_weekly_capability`, `evidence_identity_mismatch`.

A disabled account with no known recovery instant is always reported as `missing_reset`,
never as a UI-specific variant.

## Console list contract

`GET /api/v1/account-automation` returns three parts:

- `settings`: the resolved master switch and its source.
- `accounts`: the persisted managed roster, one row per adopted credential, with its plan.
- `candidates`: the live gateway credentials offered for adoption, read from CPA's own
  management credential list. Each candidate carries only safe metadata - `auth_index`,
  `provider`, `name`, `account_label`, `disabled`, `controllable`, `adopted`, `eligible`
  and `ineligible_reason` - never credential material or upstream quota. `eligible` is
  true only when the credential is controllable and its provider owns a verified
  account-level weekly capability; an unverified provider is reported ineligible with
  `ineligible_reason` `unverified_account_weekly_capability`. The read is
  read-only: a GET never writes the roster, so the first page can offer adoption before any
  account has been adopted. When the gateway read fails the response still returns the
  persisted roster and marks `candidates_degraded` with a `candidates_error` message
  instead of reporting an empty candidate list as a success.

## Consequences

The feature is opt-in twice over - the roster and the per-account switch - because the
risk of an unwanted disable is a credential that stops serving traffic. A deployment
that never adopts anything behaves exactly as before.

The evidence is read from the observation timeline, so the feature adds no collection of
its own and inherits the passive stream's freshness and the scheduler's active reads. A
deployment that disables the quota scheduler still gets evidence from request headers,
but a quiet credential with no traffic and no refresh has no new evidence and is
monitored only.

The exact-period test is the reason a Codex weekly primary is accepted while a five-hour
primary and a 168h ± 1h near-miss are not: identity comes from the window's own stated
duration, the same rule the quota normalizer already follows. Provider identity comes from
the capability registry instead of a guess: today it holds the verified account-level
weekly mappings for Claude and Codex, and every other provider stays monitor-only until its
window is verified and added deliberately - the goal remaining that every provider be
managed by its real capability, not by a hardcoded pair.

The persisted backoff and the watermark mean a restart never re-applies an action or
forgets a wait. The cost is two more columns and a retry row that a crash can leave set;
the next pass clears them on success.

The read-only summary exposes the plan, not credential material, and the roster's display
label is redacted from the operator's raw SQL read path.
