# ADR 0108: Credential adoption is separate from automatic-disable capability

- Status: Accepted; amends ADR 0105's console candidate contract by separating
  roster admission from provider capability. ADR 0105's automatic-action admission
  rules and capability registry are unchanged.
- Date: 2026-10-20

## Context

ADR 0105 made one candidate field, `eligible`, mean both that CPA exposed a
controllable credential and that OMC had verified the provider's account-level weekly
window. The console used that combined value for its checkboxes and Select all action.
As a result, controllable credentials from providers such as Antigravity could not join
the roster at all, even though the decision layer already kept an adopted unverified
provider in `monitoring` and refused automatic actions.

Roster ownership and action safety are different questions. An operator may need OMC to
track a credential before OMC knows how to interpret that provider's weekly evidence,
but adoption must not turn a seven-day-shaped observation into permission to disable or
re-enable it.

The recovery projection also has to preserve takeover plans written before this ADR.
Those rows stored a cycle reset and its evidence watermark while remaining `idle`,
without either an automatic disable timestamp or a completed enable timestamp. Treating every idle reset as history
would silently discard executable work, while treating every idle reset as active would
revive completed plans.

## Decision

Candidate and roster projections expose two independent facts:

- `adoptable` is true only when the credential is controllable through CPA. A false
  value carries `adoption_blocked_reason`; currently an unnamed credential reports
  `no_controllable_credential`.
- `auto_disable_supported` is true only when the provider owns a verified account-level
  weekly capability in `internal/automation`. The roster uses this field for its
  monitor-only status, count and filter; the persisted `monitoring` state retains its
  ordinary meaning for every provider.

Every adoptable credential may join the roster. When adoption enables an unverified
provider, its initial persisted plan is `state=monitoring` and
`monitor_reason=unverified_account_weekly_capability`, so the reason is visible without
waiting for a background pass. The console renders one **Monitor only** status for
providers without verified capability or accounts without a controllable credential,
with a switch-off qualifier when applicable. Verified controllable accounts retain
ordinary runtime state labels. Issue #44 refines this presentation; persisted states
and action admission are unchanged.

The service, not only the browser, excludes uncontrollable credentials submitted to the
adoption endpoint. The action paths retain ADR 0105's verified-capability and identity
admission checks. An unverified provider cannot be automatically disabled, cannot be
enabled from a saved recovery plan, and cannot receive a hand-filled recovery time.

Recovery execution recognizes the old takeover row only when it is adopted, enabled,
`idle`, has a cycle reset with its evidence watermark, and has neither a disable nor an enable action timestamp.
This compatibility rule preserves already-scheduled takeover work without rewriting
stored history. Any completed plan with an enable timestamp remains inert.

The inventory summary names both scopes: adopted roster rows and live CPA credentials.
It annotates adopted accounts absent from a successful live inventory in both statistics
and footer. An unavailable inventory leaves the total unknown and makes no absence claim.

## Consequences

The `/api/v1/account-automation` candidate wire contract replaces `eligible` and
`ineligible_reason` with `adoptable`, `adoption_blocked_reason`, and
`auto_disable_supported`. Its roster rows also expose `auto_disable_supported`, derived
from the same provider capability registry. The frontend type, demonstration recording,
component tests and browser fixture change with it.

Adoption becomes useful for providers whose capability work is incomplete, without
expanding automatic control. Adding a provider to the capability registry remains a
separate deliberate change backed by verified evidence semantics. Legacy takeover plans
continue to execute, while completed recovery history remains non-executable.
