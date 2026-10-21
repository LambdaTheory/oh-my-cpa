# Reset announcement advice validation

Issue #48 integrates stored reset announcements with the shared reset_advice
operation and the console's existing quota overview and detail responses. ADR 0111
records the approved precedence: a trusted timed notice within the configured
horizon can override local emergency advice only with known positive eligible local
capacity on an enabled, available credential. Local evidence remains independently
visible; unknown or zero capacity is never invented.

## Behavior evidence

- internal/operations/reset_advice_announcements_test.go covers confirmed/probable
  timing, unverified and untimed evidence, expired and outside-horizon notices,
  exact provider/credential/model scope, disabled and zero/unknown capacity,
  emergency override, preserved reset-time conflicts and matching beyond 100 notices.
- The same suite invokes Agent and read-only MCP, compares their conclusions and
  evidence after identity projection, and checks that neither auth_index nor the
  credential target appears in read-only output. Existing advice tests also refuse
  upstream client acquisition, live quota reads and refreshes.
- internal/api/reset_advice_test.go exercises the real HTTP router over a synthetic
  CPA roster: registration changes keep_for_emergency to use_before_expiry, retains
  local evidence and produces identical overview/detail advice.
- web/tests/reset-advice.component.test.tsx has six cases for the three row labels,
  unknown suppression, detail reasons and provenance, distinct conflicting reset
  times and evidence-only uncertain advice.
- The oauth-management browser scenario registers a synthetic notice through the
  form and observes the row changing without reload. It checks both evidence
  sections and horizontal fit at 1440px and 320px.

## Rendered synthetic evidence

| State | Capture |
| --- | --- |
| Before registration: keep for recovery | [Desktop](images/reset-advice/before-desktop.png) |
| After registration: use before expiry | [Desktop](images/reset-advice/after-desktop.png), [phone](images/reset-advice/after-phone.png) |
| Local and announcement evidence with conflict | [Desktop](images/reset-advice/detail-desktop.png), [phone](images/reset-advice/detail-phone.png) |

These captures contain synthetic example.test identities. The browser receives
synthetic API responses; Go tests independently certify the backend calculation.

## Commands and actual results

Run on Windows with pnpm 11.19.0, Node 24.16.0 and Go 1.25.0 selected by
GOTOOLCHAIN=auto from the installed Go launcher.

| Command | Result |
| --- | --- |
| go test ./internal/operations -run ResetAdvice -count=1 | Passed, including final unknown-capacity and pagination cases |
| go test ./internal/api -run TestQuotaConsoleResetAdvice -count=1 | Passed on the final detail projection |
| pnpm --dir web exec vitest run tests/reset-advice.component.test.tsx | Passed: 6 cases |
| pnpm test:fast | All Go, frontend, translation, style, documentation and feedback checks passed; aggregate failed on 3 native-release Windows self-tests |
| pnpm verify | Go tests/vet and frontend passed; repository checks failed on the same 3 native-release Windows self-tests |
| pnpm check:ui | Passed: all 76 affected scenarios, 484.8 seconds |
| pnpm demo:generate | Passed; generated dataset and provenance committed |
| pnpm check:demo | Passed: 137 responses, 14 console routes |
| pnpm check-docs | Passed; the repository script is spelled check-docs |
| pnpm check-i18n and pnpm check-css-modules | Passed |
| pnpm build | Passed; embedded SPA synchronized |
| pnpm verify:secrets:worktree | Passed; no leaks found |
| pnpm verify:full | Stopped at strict toolchain gate: expected Node 22.23.2 and Go 1.27.1 |
| pnpm readme:screenshots | All English/Chinese captures generated with TZ=UTC and an isolated local demo directory/port; Windows process-group cleanup did not terminate the demo, which was stopped after verifying its owned PID chain |

The three native-release failures are a symlink creation EPERM and two archive
tests requiring GNU tar flags unsupported by the Windows tar executable. Their
assertions and gates were not skipped or modified. The final fast run has exactly
these three failures. Earlier browser navigation/shutdown failures were retained
as diagnostics; the final full affected catalog completed successfully.

## Remaining verification limits

Full CI-equivalent verification requires the pinned toolchain and a host supporting
the native-release filesystem/archive tests. Built-artifact browser acceptance was
not reached by verify:full. Independent code review and CI remain PR gates; no merge,
deployment, real announcement, credential mutation or CPA write was performed.

Model scope is intentionally limited to exact persisted model windows; absent
membership stays unmatched. The committed public demo lacks the stored credential
roster needed for advice, so its regenerated responses do not fabricate advice;
the synthetic captures above demonstrate this feature. README captures retain only
the affected OAuth and phone images.
