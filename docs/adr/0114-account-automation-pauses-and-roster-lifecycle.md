# ADR 0114: Account automation preserves paused plans and tombstones removed roster entries

- Status: Superseded in part by ADR 0116 (the protected-pending-plan removal rule). Accepted as a record of the earlier decision; amends ADR 0105's per-account cancellation semantics and ADR 0108's roster lifecycle and missing-credential reasons.
- Date: 2026-10-22

## Context

Roster membership, permission to execute automation, and the gateway credential's
enabled state describe independent operator choices. Switching automation off previously
left reset timestamps on an idle row that recovery no longer recognized. Operators also
need a per-account Credit exception and a way to remove obsolete roster entries without
implicitly enabling credentials or deleting the history that explains earlier actions.

## Decision

The account switch pauses execution. Cycle and unconsumed manual resets, cycle keys,
evidence watermarks and persisted retry schedules survive pause and process restart.
Recovery projection remains readable while paused; execution still requires the account
switch, master switch, verified provider capability and matching live credential identity.
Resuming a disabled credential continues the original waiting plan. A live credential ends
the old plan and retains the evidence watermark against repeated disables.

Migration 054 adds a default-false burn_credit flag. It suppresses automatic disable
only; saved recovery is unaffected. Adoption defaults to enabled when the request omits
enable. Existing explicit enable=false remains a supported pause at adoption.

Removing membership sets is_adopted=0, is_enabled=0 and removed_at_ms without calling
CPA's status patch. Single and batch session-authenticated unadopt routes expose this
operation. Active lists, counts and background decisions exclude tombstones. Re-adoption
is explicit, clears the marker and starts a new plan on the same account row. Historical
audit records and adoption snapshots remain associated with that identity.

RunOnce removes a missing account only after two consecutive complete successful gateway
inventories under the same intent version. Failed or incomplete inventories reset the
process-local counter. A restart, reappearance or operator intent change also resets the
confirmation sequence. A pending retry, disabled/waiting state, any saved cycle reset or
unconsumed manual reset protects the row. Cleanup can update the local roster without
performing a gateway status action. Removal and its account_automation.auto_remove or
account_automation.unadopt audit event commit in one SQLite transaction, so audit failure
refuses removal.

credential_not_found identifies an absent gateway credential. credential_missing_name
identifies a present credential CPA cannot address by name. The old
no_controllable_credential reason remains readable historical data but is no longer
generated. Incomplete credential inventories are degraded rather than successful absence.

## Consequences

Pause and removal have distinct durable meanings. Restoring a database can restore a
paused or active plan, so roster state and audit history belong in the same backup.
Tombstones and snapshots remain permanent instead of reclaiming a small amount of space.
Process-local absence counters conservatively delay cleanup after restart and avoid a
new scheduling table. Gateway cleanup is silent at the data layer; no removed credential
is automatically re-adopted when it reappears.

The ordinary API allowlists expose burn_credit and removed; the read-only Agent/MCP
summary exposes the same safe facts for active rows. No write capability or provider
action scope is added. The next UI change can consume these contracts independently.
