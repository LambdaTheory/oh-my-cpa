# ADR 0112: Claude reset grant read outcomes

- Status: Accepted; extends ADR 0109 with independent supplemental-read outcomes
- Date: 2026-10-21

## Context

Claude quota windows can refresh successfully while the supplemental reset grant
read fails. Previously a failed read, an absent block, an empty grant array and a
block without `eligible` all lost their distinction before advice was evaluated.
The display is read-only; redemption eligibility is not evidence of grant ownership.

## Decision

Migration 050 adds `quota_snapshots.reset_grants_status`, projected on the existing
quota responses. Its values are `available` (a validated nonempty grant list),
`unavailable` (a successful response with no grant block, a null block, an empty
list, or explicit ineligibility without grants), and `failed` (transport, timeout,
non-2xx, normalization or malformed grant data). The empty default identifies
historical snapshots whose read outcome was not recorded; it is not backfilled.
An available list can contain spent grants and does not promise redeemability.

A valid grant array is displayed when `eligible` is absent or false. Counts must
be present non-null nonnegative integers, each ID unique, and every parsed field
valid; one malformed grant rejects the complete list. The supplemental GET keeps
the fixed `cedar_ember=1&skip_spend=1` endpoint and OAuth beta header, and adds the
Claude CLI user agent used by CPAMC. No claim endpoint or routing change is added.

For a fresh snapshot, advice distinguishes `reset_reading_failed` from
`reset_not_provided`. Both retain nullable unknown balance and unknown verdict;
absence of published grants is not an observed zero balance. Explicit spent grants
still produce the existing known-zero conclusion. Historical snapshots retain the
prior validation rules. The existing freshness limit remains authoritative.
Failed reads replace the supplemental evidence without invalidating successful
quota windows or retaining an earlier grant as newly observed evidence.

## Consequences

This is an additive database and JSON contract. Codex credits and announcement
precedence are unchanged. Only the enum and normalized allowlisted grants persist;
no upstream response body, error text, token or cookie is recorded. ADRs 0109 and
0111 remain unchanged as historical decision records.

CPAMC comparison used commit `603539ab5c5343f32c3945c6e0cdb8333991c493`:
[claudeResetGrants.ts](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/603539ab5c5343f32c3945c6e0cdb8333991c493/src/services/api/claudeResetGrants.ts)
reads `body.cedar_ember.grants`; its shared request constants include
`claude-cli/2.1.280 (external, cli)` and `oauth-2025-04-20`. This comparison establishes
the request and field contract, not the response of any operator's deployment.

## Empty grant responses (Issue #61)

A successful response with a `cedar_ember` object and omitted, null or empty
`grants` is `unavailable`, including when `eligible` is true. Eligibility alone
does not promise published grants. Invalid JSON, non-array non-null grants and
malformed entries remain `failed`, as do transport failures and non-2xx responses.
The existing migration 050 status field preserves this distinction without a new
migration. Fresh advice supplies the distinct reason in both the credential list
and detail; neither result asserts a zero balance.
