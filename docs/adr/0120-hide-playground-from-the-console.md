# ADR 0120: Hide Playground from the console

- Status: Superseded by [ADR 0121](0121-reopen-playground-for-self-hosted-operators.md)
- Decision date: 2026-10-16
- Recorded retrospectively: 2026-11-06

## Context

[ADR 0022](0022-session-authenticated-model-playground.md) introduced an authenticated
console workspace for testing models through CPA without exposing a gateway caller key
to the browser. The page, model directory and inference handlers already existed.

Commit `e7fb8bc3b90a190ea8d0f721beeeebfb9ab0b232` hid that workspace on
2026-10-16. Haowen (Newoahil), in the decision relayed on 2026-11-06, explained that
Playground had simply been considered not useful at the time.

**This ADR is a retrospective reconstruction from git history and that later oral
account. No ADR recorded the hiding decision when it was made.** The commit establishes
what changed; the later account supplies the stated reason. This document is not a
contemporaneous record and does not infer additional reasons from unrelated work in
the same commit.

## Decision

Hide Playground as a console destination: redirect `/playground` to `/dashboard`,
remove its navigation item, lazy page export and intent preloader, and remove it from
the shell's full-height conversation workspaces. Browser expectations were changed
to assert the redirect and missing entry instead of exercising the hidden page.

Keep the Playground page implementation and its authenticated backend interfaces.
This was a visibility decision, not removal of model inference or a replacement for
server-side authorization and demonstration-mode refusals.

## Consequences

Operators could no longer open the model-testing workspace through the console or its
old address even though the implementation remained present. The public demo also
continued to redirect that address, and its API and Worker continued to refuse
inference.

[ADR 0121](0121-reopen-playground-for-self-hosted-operators.md) supersedes the
self-hosted visibility decision; the public demo remains closed.
