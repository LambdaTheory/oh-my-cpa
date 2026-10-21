# OAuth runner provider adaptation report

Date: 2026-10-21. Scope: GitHub Issue #60, based on runner commit 73ff11e and OMC PR #54 contract at 469a30a. Branch: feat/reauth-runner-providers. Implementation identity is the commit containing this report; the coordinator completion receipt records its exact hash.

## Delivered behavior

All eight canonical OAuth providers are represented, with anthropic → claude, openai → codex and grok → xai aliases. Non-OAuth and unknown IDs fail before calling CPA. Only tools/reauth-runner was changed.

| Flow | Providers | Offline evidence |
| --- | --- | --- |
| redirect_otp | claude, codex | Email, six-digit code, callback capture/submission, OMC one-use code and status reports |
| redirect_browser | antigravity | Human browser completion, callback 51121 or independent CPA completion; no code reads |
| redirect_manual_callback | devin | No forwarder flag; tunnel/manual-submit notice; captured and externally submitted callback cases |
| device_code | kimi, kimi-ai, xai, meta | Device metadata, clean HTTPS navigation, separate user code, expiry, CPA completion; no callback/code reads |

OMC integration uses one management-key login and a same-origin session for next, code, status and callback. Empty queue returns idle before CPA initiation. Only consumed OMC OTP produces code_submitted. Device query data never reaches displayed URLs or OMC metadata. Startup transport/5xx failure explicitly warns and permits standalone operation; code-endpoint outage permits file fallback. Permission/conflict failures do not bypass OMC.

Success requires positive CPA status and acceptance of the OMC success report when a job is active. Upstream messages/bodies are omitted to avoid credential echoes. Cancellation, browser closure, expiry, unknown status and timeouts fail explicitly and attempt an allowlisted OMC failure report. Failed reporting emits an inspection warning. CPA sessions are not deleted/revoked by local cancellation.

## Verification

Run runner commands from this directory:

| Command | Observed result |
| --- | --- |
| pnpm --ignore-workspace typecheck | Passed |
| pnpm --ignore-workspace test | Passed: 50 tests, 0 failures/skips |
| pnpm --ignore-workspace fixture | Passed; original synthetic fixture-code / fixture-state output retained |
| Root pnpm test:fast | Go, frontend, component, docs, workflow and other static lanes passed; repository self-tests failed on three native-release Windows cases: symlink EPERM and GNU tar options |
| Root pnpm verify | Go test/vet and frontend passed; aggregate failed on the same three Windows native-release self-tests |
| Root pnpm verify:secrets:worktree | Passed: no leaks found |
| Root pnpm check-docs and git diff --check | Passed |

Baseline typecheck, all 20 existing tests and fixture passed before changes. Existing claims were retained; OTP fixture input now uses six digits while keeping its synthetic OAuth callback code. Tests no longer require upstream free-text messages in public results because that output was removed at the credential boundary.

A cancellation test exposed concurrent browser-close calls releasing the profile too early on Windows. The runner now awaits one shared close operation; timeout/cancellation tests prove profile cleanup succeeds without retries or longer waits.

Environment: Windows PowerShell, Node 24.16.0, pnpm 11.19.0, Playwright 1.62.1. Repository toolchain reports compatibility-only drift from pinned Node/Go. No product code, gate, real provider, production credential or Feishu service was changed/contacted. No push or PR is part of this delivery.

## Remaining real acceptance

The README provides per-flow commands and operator steps. All live provider results remain unverified in this task:

1. OTP: disposable Claude or Codex account, system Chrome, real email/OTP selectors, callback and CPA credential confirmation.
2. Browser: Antigravity Google login/consent with system Chrome and callback/status confirmation without OTP reads.
3. Device: Kimi, Kimi AI, xAI or Meta real response metadata, query-free page usability, human code entry and CPA completion.
4. Manual callback: Devin with CPA callback reachability/tunnel, plus second-terminal submit and original-run completion.
5. OMC deployment: allowed direct runner CIDR, real job lifecycle and later quota verification; Feishu delivery is a separate authorized acceptance step.
6. Docker image was not built in this adaptation. Existing image/browser constraints and protected profile retention still need deployment characterization.

Vendor DOM, split inputs, password/SSO and challenge behavior are not inferred from fixtures. No CAPTCHA, 2FA or device approval behavior was observed against a real account. One runner at a time is required; this CLI does not schedule jobs or manage profiles across accounts.
