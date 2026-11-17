# Repository-key delivery harness (`//go:build e2e`)

Cross-process acceptance for the loop that hands a repository-owned key to a
caller: **apply -> approve -> ciphertext -> local open -> the gateway accepts the
key**. It runs the real Oh My CPA binary against a loopback gateway stand-in and
decides the application over the console's HTTP surface, so nothing here depends
on a browser.

This slice builds the harness and the non-CLI half of the loop. Everything the
`oh-my-cpa key` command will own is a marked stand-in; see "What the CLI slice
replaces" below.

## Run it

One command, from the repository root:

```bash
pnpm test:key-e2e
```

It needs a Go toolchain. When `go` is not on `PATH` and Docker is available, the
script runs the same test in `golang:1.27.1` with the network off, the checkout
read-only, and the repository's `omc-go-mod` / `omc-gocache` volumes, so nothing
is downloaded and nothing in the checkout is written. On this Windows machine
that Docker path is the one that works; there is no local Go.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OMC_E2E_EVIDENCE_DIR` | `$TMPDIR/omc-key-e2e-evidence` | Where `key-loop.json` and `omc.log` are written |

The command exits 0 on a pass and non-zero on a failure, and prints one passing
line with the key's mask (never the key) plus the evidence path.

## What the run proves

1. `repo_key_apply` over `POST /omc/api/v1/capabilities/invoke` with
   `X-Omc-Agent` / `X-Omc-Device` returns a pending application and a one-time
   approval link on this console's origin.
2. **Failure path**: the envelope before any decision is refused with
   `application_not_approved`, from the console route and from the capability.
3. `repo_key_status` reads `pending` with no ciphertext.
4. `POST /omc/api/v1/management/repo-keys/applications/{id}/approve` (a session
   cookie from `POST /omc/api/auth/login`) approves it, and the gateway's access
   list grows by exactly one key.
5. `repo_key_ciphertext` returns an envelope whose mask matches the approval's,
   and the envelope does not contain the key.
6. A child process opens the envelope with the caller's private key using this
   harness's own implementation of the construction (X25519, HKDF-SHA256,
   AES-256-GCM), and the plaintext is byte-for-byte the key the gateway holds.
7. A second keypair applying for the same repository with a **changed** public
   key is `pending`, not auto-approved. **Failure path**: a rejection with no
   reason is refused; a rejection with a reason ends the application, and its
   ciphertext then stays unavailable.
8. Each runtime's child process sees only what its injection sets (and, for
   Claude, no `ANTHROPIC_API_KEY` that would take precedence), prints masked
   variable shapes, and **uses the injected key successfully against the
   gateway** - so the masking report is backed by a real request, not by a
   variable name alone.
9. The service log and the evidence file contain no plaintext key.

The fixture's gateway starts with an empty access list, so no request carrying a
key can succeed before the approval writes one.

## Isolation

- `HOME`, `TMPDIR`, `OMCPA_DATA_DIR` and `OMCPA_ENV_FILE` point into a
  `t.TempDir()`; the child processes get the same home.
- The service's environment is built from nothing but `PATH`, so a variable in
  the operator's shell cannot change what a run proves.
- Nothing listens or connects outside `127.0.0.1`.
- `OMCPA_USAGE_INGEST_MODE=off` and the schedulers are off: the harness never
  pops the gateway's usage queue and never polls it.
- The harness does not touch `~/.claude`, `~/.codex` or `~/.config/opencode`.

`wait.go` holds the harness's only waiting primitive: every step waits on its own
condition (the health endpoint, a capability's answer, a child process exiting)
and fails with the last thing it observed. There is no fixed sleep.

## What the CLI slice replaces

The harness marks its stand-ins in `e2e_test.go`. The follow-up slice (the `key`
command) replaces them and should leave the rest of the loop intact:

1. **Key generation and storage** - `newCaller` keeps the pair in memory. Replace
   with `oh-my-cpa key gen --home <dir> --repo <repo> --base-url <gateway>` and
   read the public key from its stdout.
2. **Envelope intake** - `openDelivery` / `runDecryptHelper` open the envelope in
   a child process. Replace with `oh-my-cpa key accept --home <dir> --repo <repo>`
   reading the capability's `data` object on stdin.
3. **Runtime injection** - `claudeInjection` / `codexInjection` /
   `openCodeInjection` build each runtime's environment. Replace with
   `oh-my-cpa key exec --home <dir> --runtime <name> --repo <repo> -- <child>`,
   where the child is this harness's masking helper. The assertions that the
   child saw the injected variables, that the removed variable is gone, and that
   the key was accepted by the gateway stay as they are.

Two later steps belong to the follow-up slice as well: `key status --json`, and
`key purge` followed by `key exec` exiting 4 with `key_not_found` without running
the child.

## What it does not prove

- A real CLIProxyAPI's routing, provider or model configuration. The stand-in
  proves only that a gateway which checks its access list accepts the key.
- The console's approval page in a browser. The decision is exercised over the
  same HTTP route the page posts to; the page's own geometry and hit-testing are
  a browser probe's claim, not this harness's.
