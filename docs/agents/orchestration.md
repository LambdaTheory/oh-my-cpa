# OMC Fork Issue Orchestration

## Scope and approved workflow

This repository remains an Oh My CPA fork. CPA-buddy is the operator's informal name, not a product or repository rename.

The coordinator schedules, decides scope, reconciles reports and accepts evidence; it does not implement business code. Use the version-matched `orca skills get orchestration` guide for lifecycle authority. The issue workflow is provided by the locally installed `orca-issue-orchestration` skill.

- GitHub target: `Newoahil/oh-my-cpa`. Always pass this repository explicitly to `gh`; the upstream repository must not receive these issues or PRs.
- Base and PR target: latest `origin/dev`, fetched before starting an independent task. Do not target `master` in this batch.
- Maximum concurrency: three active workers, including review workers. Each implementation lane uses an independent Orca worktree.
- Implementation: Orca Codex workers using the user's configured model. Preserve the existing design system for UI changes.
- Review: presentation-only changes receive one independent review. Request/data logic, backend jobs, build metadata and deployment changes receive two independent views, bounded to the actual diff. Only correctness, privacy, security or explicit requirement violations open a repair round. Optional cleanup does not block delivery.
- Delivery: commit, push and open PRs to `dev`. No automatic merge, deployment, real-data changes or real Feishu delivery. Dependent implementation starts only after its dependency is merged.
- User acceptance: one independent `needs-user` + `gate` issue, assigned to `Newoahil`, remains open until the user confirms the deployed experience. User questions must also be recorded there or in a dedicated user issue, with a comment beginning `需要你处理`.

## Privacy and execution boundaries

Use synthetic account labels, tokens, cookies, timestamps and notification transports in tests. Do not copy operator screenshots or real account identifiers into issues, PRs, fixtures or reports. Do not inspect production databases, secrets or environment files, and do not call production CPA, Ollama or Feishu endpoints.

Never modify `E:/works/CPA-Manager`, another task's worktree, the existing `issue-8-model-advice` worktree, or pre-existing untracked icon build artifacts. Do not reset, stash or switch the coordinator's checkout. Preserve unrelated work and do not stage it.

Reuse existing authentication, read-only MCP boundaries, cookie encryption, fixed outbound targets, request timeouts and upstream backoff. Do not expand this small trusted-operator application into a new permissions framework or absolute-consistency state machine.

## Verification and evidence

Read `AGENTS.md`, `CONTEXT.md`, `docs/architecture.md`, `docs/design.md` and `docs/testing.md`. Follow their affected-check and UI evidence contracts. Test the bug at the lowest effective layer; UI selection, action routing and hidden-identifier claims require rendered or browser evidence with synthetic data, not only helper tests.

Go is available at `C:/Users/lhw/tools/go1.24.13/go/bin/go.exe`; prepend that directory to PATH and use `GOTOOLCHAIN=auto`. Use the repository's pnpm scripts, inspect `pnpm test:fast --plan`, and reuse evidence only for an unchanged final scope. Report Windows/toolchain limitations explicitly instead of skipping checks or claiming full verification. Use isolated loopback ports; never connect test processes to the main service port.

Regenerate demo data through the real exporter when its inputs change; never hand-edit source digests. Keep technical documentation and code comments in English and update every complete UI language catalog. Read the contribution guide, PR template and title rules before opening a PR.

## Durable coordination state

Run-local facts live in `.orchestration/<run_id>/state.json` with append-only `events.md`, maintained by the installed `orch_state.py`. Read state before processing each event, record user decisions and dispatch receipts immediately, and use explicit `--run` on Orca calls. The runtime's actual command help wins over an older skill example.

Only one inbox waiter may exist per run. Process and record every delivery before acknowledgement. A missing status or timeout is not proof a worker exited. After accepted settlement, reuse, retain or release according to the runtime guide; do not stop or close an unsettled worker. Preserve worktrees after PR delivery unless cleanup is separately authorized.

Notifications use this conversation and sanitized GitHub issue comments. Real bot messages are not authorized for this batch.
