# OMC first-batch local verification

| Field | Result |
| --- | --- |
| Date | 2026-10-15 |
| Scope | Account weekly automation, Ollama monitoring, Feishu rule-based notifications and queries, and the retired Playground route |
| Requirements | [First-batch specification](omc-first-batch-spec.md) |
| Independent functional QA | PASS for the bounded local scope |
| Repository verification | Windows invocation remains nonzero; its native-release environment failures were closed by a focused Linux container run, not a later single all-green invocation |
| Publication | No commit, push, pull request, merge, or production deployment performed |

## Verified behavior

- Account adoption requires explicit selection. Candidates are separate from the persisted roster; reading candidates does not adopt them.
- Automatic disable requires matching account/evidence/live-credential identities and a verified account-level weekly capability. Claude and Codex are the currently verified adapters, not a permanent supplier whitelist.
- Invalid, missing, or out-of-range weekly readings do not become fabricated exhaustion evidence. Five-hour or model/group exhaustion does not disable an account.
- Recovery enables directly at the stored reset time. Persistent retries, cancellation, first takeover, and evidence-watermark behavior have regression tests.
- Ollama cookies are encrypted, write-only through the management API, and absent from ordinary DTOs and read-only tools. Missing readings remain unknown. Collection enablement does not claim to control external inference.
- Feishu queries use stored data. Explicit authentication-failure signals, not generic availability flags, drive credential-invalid notifications.
- Quiet-hour notifications merge unresolved items; stale readings do not trigger tight-channel summaries. Editing the target chat does not restart the long connection.
- The Playground address redirects to the dashboard, its navigation entry is hidden, and the Agent workspace remains reachable.

## Evidence

The coordinator ran the complete Go test suite and vet in the real worktree, without a source overlay. The static frontend lane passed type checking, 78 logic suites, 95 component tests, and Ant Design lint after the fixes.

Independent QA reran the following packages with an uncached test count and received exit zero:

```text
go test -count=1 ./internal/automation ./internal/quota ./internal/repository ./internal/api ./internal/feishu ./internal/ollama
```

Independent QA also executed these local Vite/mock browser scenarios, each with exit zero:

```text
node scripts/check-ui.mjs --scenario omc-account-automation
node scripts/check-ui.mjs --scenario omc-ollama-accounts
node scripts/check-ui.mjs --scenario omc-feishu-settings
node scripts/check-ui.mjs --scenario playground
```

The coordinator's full 69-scenario development-browser run initially failed the existing settings-page subtitle invariant. The product fix was followed by successful focused settings and Feishu-settings runs; this is retained-run plus focused-retest evidence, not a claim that a later complete 69-scenario invocation passed.

Production frontend build completed successfully. A locally staged demo verifier rendered all 17 runtime routes with exit zero after the two Feishu reads were added to the real handler export and Worker routing. Dataset validation reports 127 responses. Dataset checks, documentation checks, style/feedback checks, and the worktree secret scan passed during integration.

README image files were refreshed and representative desktop/mobile images inspected. The screenshot process emitted the images but did not exit within the command budget; its exit is not reported as a passed gate. No owned demo listener remained afterward.

## Remaining verification and operational limits

- The Windows full repository verification invocation was nonzero because native-release tests need symbolic-link privilege and compatible archive tools. Its remaining three environment failures were subsequently closed in a disposable local Linux container: the existing Node image was used, zip/unzip were installed only inside that container, and the repository was mounted read-only. All seven tests in `scripts/native-release.test.mjs` passed, with no skips. No project dependency, host permission, or test assertion was changed; the container was removed on exit.
- Evidence is composed across the Windows verification run, focused repaired-check reruns, and the Linux native-release run. There is no claim that a later single `pnpm verify` command passed. The remaining failures from the last Windows run were demo freshness checks, which passed after regenerating the real demo export. A future release should still run the normal repository CI gates against its exact committed state. Publishing or running remote CI requires separate authorization.
- Windows could not launch the demo wrapper's direct pnpm spawn; the runtime demo was checked through an explicitly staged local server instead. This does not establish that the wrapper is portable.
- Real Ollama scraping, Feishu delivery/long-connection behavior, and production CPA state writes were not exercised. Real credentials must be entered by the operator, never copied into test fixtures or reports.
- The new migrations have local repository/API test coverage; production migration and rollback operations have not been performed.
- The signal-state column relies on repository enum normalization rather than a SQLite CHECK constraint. Independent QA recorded direct-database-write hardening as non-blocking for the approved API/tool boundary.

These results establish local functional readiness for review, not authorization to publish or a production-readiness guarantee.

## Follow-up: Ollama in the unified credential list

The follow-up integrates independent Ollama monitoring accounts into the OAuth workspace's provider filters, search and rows. Collection controls call the Ollama API only; these rows cannot participate in CPA credential batch actions, downloads, model rules, cooldown clearing or quota refresh. Request telemetry is explicitly unknown for Ollama, not zero: request-count sorting orders known CPA counts first and uses stable name/key ordering for unknown rows.

Independent QA passed this bounded follow-up after the unknown-telemetry sorting fix. It independently ran all 38 workspace logic tests and the Ollama and OAuth management browser scenarios, each with exit zero. The coordinator additionally passed the mobile-console browser scenario, frontend type checking, production build, current demo-data validation and the Worker routing tests. Shared fixtures and demo routing declare both new read-only inventory endpoints without weakening undeclared-request or write refusal checks.

The offline HTML preview contains real local fixture-rendered screenshots, including desktop and mobile Ollama-filtered main lists. It is not a live administration surface. The follow-up changes OMC only and requires no CPA deployment. Real-provider verification and publication remain outside these local results. Paused-row copy could be clearer about pausing collection rather than inference; independent QA recorded it as a non-blocking wording improvement.
