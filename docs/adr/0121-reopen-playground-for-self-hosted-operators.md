# ADR 0121: Reopen Playground for self-hosted operators

- Status: Accepted
- Date: 2026-11-06
- Supersedes: [ADR 0120](0120-hide-playground-from-the-console.md)

## Context

Playground was hidden because it had been considered not useful, not because its
implementation had been removed. Haowen (Newoahil) decided on 2026-11-06 to reopen
the existing model-testing page. No additional rationale was recorded with that decision.

The page lets operators send a test request from their console to any call point in
CPA's live model directory.

The self-hosted console and the public demonstration have different authority.
An authenticated self-hosted operator may intentionally spend their configured
provider entitlement. Anonymous visitors to the demonstration must not gain that
ability simply because a console route becomes visible again. A hidden navigation
entry alone cannot protect an inference endpoint.

## Decision

1. **Restore the existing self-hosted workspace.** `/playground` renders the retained
   page inside the authenticated shell. Restore its operate-group navigation item,
   lazy loading, intent preloading and full-height conversation layout. Keep existing
   selection, request inspection, streaming and conversation recovery behavior.
2. **Keep CPA as the model-directory and routing authority.** Operators choose an
   existing gateway caller key by its server-derived fingerprint, then a call point
   from that key's live CPA directory. That directory spans the upstream credentials
   CPA serves. The console does not introduce provider-specific routing or promise to
   pin a request to one upstream credential. Key resolution, session and same-origin
   boundaries remain those of the existing facade.
3. **Keep Playground closed in the public demonstration.** Demo `/playground` still
   redirects to `/dashboard`; neither the rail nor its navigation settings lists the
   entry. Do not mount the retained recorded Playground replay as a reopened page.
   The Go demo policy and Cloudflare Worker continue to refuse inference, irrespective
   of the browser UI. Existing fixture-only directory reads remain available to the
   shared model picker; reading that directory does not authorize spending entitlement.
4. **Restore verification of the accessible surface.** Browser probes exercise real
   self-hosted Playground interactions and phone layout again. Separate checks require
   the demo redirect, missing navigation entry and execution refusal. Local verification
   uses synthetic credentials and a fake upstream, never a real provider account.

## Consequences

Self-hosted operators regain an intentional, potentially billable model-testing action
without copying caller keys into the browser. Existing admission bounds, redaction,
cancellation and streaming constraints remain necessary; reopening a route does not
weaken them. The change needs no schema migration, new credential or gateway setting.

The demonstration stays unsuitable for live inference even if a deployment is pointed
at a real upstream. Its server-side refusal is the spending boundary; its redirect and
missing navigation entry avoid advertising a feature visitors cannot use.
