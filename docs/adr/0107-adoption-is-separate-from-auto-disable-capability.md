# ADR 0107: Credential adoption is separate from automatic-disable capability

- Status: Accepted
- Date: 2026-10-20
- Amends: ADR 0105's console candidate contract. Its automatic-action admission
  rules and capability registry are unchanged.

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

## Decision

The candidate projection exposes two independent booleans:

- `adoptable` is true only when the credential is controllable through CPA. A false
  value carries `adoption_blocked_reason`; currently an unnamed credential reports
  `no_controllable_credential`.
- `auto_disable_supported` is true only when the provider owns a verified account-level
  weekly capability in `internal/automation`.

Every adoptable credential may join the roster. When adoption enables an unverified
provider, its initial persisted plan is `state=monitoring` and
`monitor_reason=unverified_account_weekly_capability`, so the state is visible without
waiting for a background pass. The console names that state **Monitor only**, includes
it in Select all, and filters the roster by the canonical `monitoring` state.

The service, not only the browser, excludes uncontrollable credentials submitted to the
adoption endpoint. The action paths retain ADR 0105's verified-capability and identity
admission checks. An unverified provider cannot be automatically disabled, cannot be
enabled from a saved recovery plan, and cannot receive a hand-filled recovery time.

The inventory summary names both scopes: adopted roster rows and live CPA credentials.
It does not call the adopted-row count a total account count.

## Consequences

The `/api/v1/account-automation` candidate wire contract replaces `eligible` and
`ineligible_reason` with `adoptable`, `adoption_blocked_reason`, and
`auto_disable_supported`. The frontend type, demonstration recording, component tests
and browser fixture change with it.

Adoption becomes useful for providers whose capability work is incomplete, without
expanding automatic control. Adding a provider to the capability registry remains a
separate deliberate change backed by verified evidence semantics.
