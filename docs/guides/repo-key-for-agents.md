# Repository keys: an agent runbook

**For the human, first:** this page is the operating manual a coding agent follows to use a
CLIProxyAPI (CPA) key that belongs to the repository it is working in, not to the person
driving it. Oh My CPA (OMC) issues the key as an X25519 envelope (ciphertext); only a machine
holding the matching private key can open it, and a person approves the first issue. Console:
`https://omc.madebynight.top/omc` · model gateway: `https://api.concertcalendar.cloud`. The
machine-readable summary of this page is [`repo-key-agent.json`](repo-key-agent.json); the
protocol reference is [`docs/agent-capabilities.md`](../agent-capabilities.md); the longer
client design note is [`docs/repo-key-client.md`](../repo-key-client.md).

Everything below is written for the agent. Follow the steps in order. Each step says how to
recognise success and failure; do not continue past a failure you cannot classify.

## Hard rules

1. **Never print the plaintext key, the private key file, or the decrypted envelope.** Not to
   stdout, a log, a transcript, a commit, a chat message, an issue, or a screenshot.
2. **Never write the key, the envelope, or `$OMC_HOME` into a repository.** `$OMC_HOME`
   defaults to `~/.omc`; keep it there (or another path outside any worktree).
3. **Never create or edit `$OMC_HOME/envelopes/*` yourself.** Only `oh-my-cpa key accept`
   stores an envelope: it validates the envelope against your private key and writes it
   owner-only.
4. **Never put the key in a command line, a tool argument, or a runtime's project-level
   configuration.** `key exec` injects it into one child process; `key install` is the only
   command allowed to persist it, and only in a user-level file.
5. **Approval is a human action.** Hand the `approval_url` to the operator and wait. Never
   open it, never approve on their behalf, never simulate a decision.
6. **`key purge` is local.** It removes this machine's material. It does not revoke the key
   in CPA; only the operator can stop or reissue it in the console.
7. **Do not call `repo_key_ciphertext` before the application is approved.**

## 1. Preconditions

- Linux **amd64 or arm64** (the primary targets). macOS arm64/amd64 and Windows amd64
  archives are published too, but the tested path is Linux.
- `curl`, `tar`, `sha256sum` (coreutils), `git`, and a writable home directory.
- Network access to the OMC console and to the model gateway.
- One credential, provided by the operator through the environment:
  - `OMCPA_CPA_MANAGEMENT_KEY` — the CPA management key (signs in to OMC; can apply and read);
    or
  - a **read-only MCP token** — can call the read tools (`repo_key_query`,
    `repo_key_status`, `repo_key_ciphertext`), but **cannot apply** (`repo_key_apply` is a
    write; it answers `capability_forbidden`). Apply with the management key, or ask the
    operator to file the application from the console (the repository-key panel on the API
    keys page, which records the device as `console`).

Never echo the credential. Do not put it in a file inside a repository.

## 2. Install the CLI

Pick the archive that matches `uname -m`. `x86_64` → `amd64`, `aarch64`/`arm64` → `arm64`.

```bash
VERSION=key-cli-v0.3.4.1
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)"; exit 1 ;;
esac
BASE="https://github.com/LambdaTheory/oh-my-cpa/releases/download/${VERSION}"
ASSET="oh-my-cpa_${VERSION}_linux_${ARCH}.tar.gz"
curl -fsSL -o "/tmp/${ASSET}" "${BASE}/${ASSET}"
curl -fsSL -o /tmp/checksums.txt "${BASE}/checksums.txt"
(cd /tmp && grep "linux_${ARCH}\.tar\.gz$" checksums.txt | sha256sum -c -)
tar -xzf "/tmp/${ASSET}" -C /tmp oh-my-cpa
install -m 0755 /tmp/oh-my-cpa "$HOME/.local/bin/oh-my-cpa"
oh-my-cpa key --help
```

**Success:** `sha256sum -c` prints `<name>: OK`, and `key --help` prints
`Usage: oh-my-cpa key <command> [options]` followed by the six commands `accept`, `exec`,
`gen`, `install`, `purge`, `status`.
**Failure:** a checksum mismatch means the download is corrupt — delete it and download
again; do not run the binary. `command not found` for `sha256sum` means coreutils is missing.
If `key --help` prints no `key` commands, the binary is older than this feature.

This archive is the CLI build of `oh-my-cpa` for `key` users; it does not embed the console.
The console is served by the OMC deployment (`https://omc.madebynight.top/omc`), not by this
download.

Other platforms (same release, same `checksums.txt`):

| OS | Architecture | Asset |
| --- | --- | --- |
| Linux | amd64 | `oh-my-cpa_key-cli-v0.3.4.1_linux_amd64.tar.gz` |
| Linux | arm64 | `oh-my-cpa_key-cli-v0.3.4.1_linux_arm64.tar.gz` |
| macOS | arm64 | `oh-my-cpa_key-cli-v0.3.4.1_darwin_arm64.tar.gz` |
| macOS | amd64 | `oh-my-cpa_key-cli-v0.3.4.1_darwin_amd64.tar.gz` |
| Windows | amd64 | `oh-my-cpa_key-cli-v0.3.4.1_windows_amd64.zip` |

## 3. Configure the environment

Set these once per shell (or in the agent's own environment). Values below are the real
deployment.

```bash
export OMC_HOME="$HOME/.omc"                                   # private keys, envelopes, state
export OMCPA_CPA_BASE_URL="https://api.concertcalendar.cloud"  # gateway root; never add /v1
export OMCPA_SERVER_URL="https://omc.madebynight.top/omc"      # console URL including base path
export OMCPA_AGENT_NAME="<your-agent-name>"                    # shown on the approval page
export OMCPA_DEVICE_NAME="$(hostname)"                         # shown on the approval page
export OMCPA_CPA_MANAGEMENT_KEY="<provided-by-operator>"       # never echo this
```

**Success check:**

```bash
oh-my-cpa key status --json
```

prints `"base_url": "https://api.concertcalendar.cloud"`, `"base_url_source": "env"`, and
`"repos": []` on a fresh machine.
**Failure:** `home_insecure` (exit 1) means `$OMC_HOME` cannot be restricted to your user;
let `key` create it under your home directory rather than pointing at a shared path.

## 4. Connect to OMC

The four repository-key tools are MCP-only. Use whichever transport your client supports;
all three carry the same self-reported identity.

### A. Streamable HTTP (preferred when the client speaks MCP over HTTP)

- Endpoint: `POST https://omc.madebynight.top/omc/api/mcp`
- Headers:
  - `Authorization: Bearer <credential>`
  - `X-Omc-Agent: <agent name>`
  - `X-Omc-Device: <device name>`

A client configuration looks like this (adapt the key name to your client; `${VAR}` is
expanded from the environment, so the secret need not be written down):

```json
{
  "mcpServers": {
    "omc": {
      "type": "http",
      "url": "https://omc.madebynight.top/omc/api/mcp",
      "headers": {
        "Authorization": "Bearer ${OMCPA_CPA_MANAGEMENT_KEY}",
        "X-Omc-Agent": "${OMCPA_AGENT_NAME}",
        "X-Omc-Device": "${OMCPA_DEVICE_NAME}"
      }
    }
  }
}
```

### B. stdio bridge (when the client can start a command)

The same binary serves an MCP server over stdin/stdout and adds the identity headers itself:

```json
{
  "mcpServers": {
    "omc": {
      "command": "oh-my-cpa",
      "args": ["mcp"],
      "env": {
        "OMCPA_SERVER_URL": "https://omc.madebynight.top/omc",
        "OMCPA_CPA_MANAGEMENT_KEY": "${OMCPA_CPA_MANAGEMENT_KEY}",
        "OMCPA_AGENT_NAME": "${OMCPA_AGENT_NAME}",
        "OMCPA_DEVICE_NAME": "${OMCPA_DEVICE_NAME}"
      }
    }
  }
}
```

### C. Plain HTTP capability calls (when the client speaks neither)

Every tool is also callable as JSON over the capability endpoint. This is the path used by
the examples below, and it needs no MCP handshake:

```bash
INVOKE="https://omc.madebynight.top/omc/api/v1/capabilities/invoke"
curl -fsS -X POST "$INVOKE" \
  -H "Authorization: Bearer $OMCPA_CPA_MANAGEMENT_KEY" \
  -H "X-Omc-Agent: $OMCPA_AGENT_NAME" \
  -H "X-Omc-Device: $OMCPA_DEVICE_NAME" \
  -H "Content-Type: application/json" \
  -d '{"name":"repo_key_query","arguments":{"repo":"<org>/<repo>"}}'
```

An authenticated call answers HTTP 200 with a result envelope: success is
`{"status":"success","data":{...}}`, a refused call is
`{"status":"error","code":"<code>","detail":"..."}` (the detail is written for the caller).
Authentication failure answers HTTP 401 with `{"code":"authentication_required"}`. A tool the
credential may not use, or arguments the schema rejects, answers HTTP 400 with
`{"code":"capability_forbidden"}` / `{"code":"invalid_tool_arguments"}`.

### The tools

| Tool | Permission | Arguments | Returns (in `data`) |
| --- | --- | --- | --- |
| `repo_key_query` | read | `repo` | `state` (`active`/`none`/`stopped`), `key_mask`, `next_kind` (`reuse`/`issue`/`reissue`), `auto_approve` |
| `repo_key_apply` | write | `repo`, `purpose`, `public_key` | `application_id`, `status`, `kind`, `approval_url`, `approval_expires_at_ms` |
| `repo_key_status` | read | `application_id` | `status` (`pending`/`approved`/`rejected`), `reject_reason`, `ciphertext_ready` |
| `repo_key_ciphertext` | read | `application_id` | `application_id`, `kind`, `key_mask`, `algorithm`, `ciphertext`, `instructions` |

`public_key` is required on the first application (`x25519:<base64>` from `key gen`); later
applications may omit it and reuse the registered key. The caller's identity comes from the
headers, never from tool arguments — adding an `agent` argument is rejected.

## 5. The flow

### Step 1 — see what this machine already holds

```bash
oh-my-cpa key status
```

**Success:** exit 0; the home path, the gateway address, and any repositories this machine
already holds.
**Failure:** `base_url_required` (exit 3) only affects `exec`/`install`; `status` still works.

### Step 2 — generate the key pair

```bash
oh-my-cpa key gen --repo <org>/<repo> --base-url https://api.concertcalendar.cloud
```

**Success:** stdout is exactly one line, `x25519:<base64>` — that is the public key you send
to OMC. Stderr says `generated a key for <repo> at keys/<name>.key` and `saved the gateway
address in .../config.json`. Running it again reuses the key and prints the same public key.
**Failure:** `repo_required` (exit 2) — pass `--repo`; `home_insecure` (exit 1) — move
`$OMC_HOME` under your home directory. Never replace a key by deleting files: `key purge`
then `gen` is the only supported rotation, and it requires a new approval.

### Step 3 — apply (MCP `repo_key_apply`)

Call the tool with the repository, a one-sentence purpose, and the public key:

```bash
curl -fsS -X POST "$INVOKE" \
  -H "Authorization: Bearer $OMCPA_CPA_MANAGEMENT_KEY" \
  -H "X-Omc-Agent: $OMCPA_AGENT_NAME" \
  -H "X-Omc-Device: $OMCPA_DEVICE_NAME" \
  -H "Content-Type: application/json" \
  -d '{"name":"repo_key_apply","arguments":{
        "repo":"<org>/<repo>",
        "purpose":"<one sentence: what the key will be used for>",
        "public_key":"x25519:<base64 from step 2>"
      }}'
```

**Success:** `data.status` is `pending` on the first application — record `application_id`
and give `approval_url` to the operator. If the caller was already approved for this
repository with this public key, `data.status` is `approved` and you skip to step 5.
**Failure:**
- Missing `X-Omc-Agent` → result `{"status":"error","code":"invalid_parameters","detail":"The agent
  name is required and must be at most 128 characters."}`.
- Missing `X-Omc-Device` → the same shape with `The device name could not be determined;
  reconnect with a device header.`
- Read-only token → HTTP 400 `{"code":"capability_forbidden"}`. Apply needs the management
  key, or the operator files it from the console.
- `purpose` empty or over 500 characters → `invalid_parameters` with a detail naming the
  limit.
- Existing MCP connections keep the headers they connected with; after changing
  `X-Omc-Agent`/`X-Omc-Device`, reconnect the MCP session.

### Step 4 — wait for the human decision

Tell the operator exactly what to open (`approval_url`) and what it approves (repository,
agent, device, purpose). The one-time link is valid for about 24 hours
(`approval_expires_at_ms`); the application itself does not expire.

Record the application locally and optionally read the state:

```bash
oh-my-cpa key status --repo <org>/<repo> --application-id <application-id>
oh-my-cpa key status --remote --repo <org>/<repo>
```

Or call `repo_key_status` with `{"application_id":"<id>"}`.

**Success:** `status` is `approved` (`ciphertext_ready: true`).
**Failure branches:**
- `rejected` — read `reject_reason`, stop, and report it. Do not re-apply unless the operator
  asks.
- The link is older than 24 hours (`approval_link_expired` on the approval page) — call
  `repo_key_apply` again for a fresh application and link; or ask the operator to re-issue
  the link from the console.
- Fetching `repo_key_ciphertext` before approval returns a result with
  `{"status":"error","code":"operation_failed"}` and no detail (the server does not expose
  the pending reason). Nothing was delivered — wait for the decision.
- `remote_not_configured` (exit 3) — `OMCPA_SERVER_URL` or the credential is missing locally;
  local state is unchanged.
- `remote_unavailable` (exit 1) — OMC could not be reached. Keep polling offline with
  `key status`; no local change was made.

Poll no more often than every 30 seconds.

### Step 5 — fetch the envelope and accept it

```bash
curl -fsS -X POST "$INVOKE" \
  -H "Authorization: Bearer $OMCPA_CPA_MANAGEMENT_KEY" \
  -H "X-Omc-Agent: $OMCPA_AGENT_NAME" \
  -H "X-Omc-Device: $OMCPA_DEVICE_NAME" \
  -H "Content-Type: application/json" \
  -d '{"name":"repo_key_ciphertext","arguments":{"application_id":"<application-id>"}}' \
  | oh-my-cpa key accept --repo <org>/<repo> --application-id <application-id>
```

If the ciphertext arrived through an MCP tool result instead, pipe that result object into
the same command. `key accept` unwraps the capability envelope, a bare data object, or the
inner envelope.

**Success:** exit 0 and one line `accepted <repo>: key <mask> at <path>` (or `replaced the
envelope ...` when one existed).
**Failure:** `envelope_invalid` (input is not an envelope), `envelope_key_mismatch` (sealed
to a different key pair — check `$OMC_HOME` and the repository), `decrypt_failed` (the
envelope and private key disagree — fetch again; if it persists, purge and generate a new
key, which needs a new approval). The envelope is ciphertext, not a public artifact: do not
commit it, paste it into an issue, or send it anywhere.

### Step 6 — run something with the key

```bash
oh-my-cpa key exec --runtime claude   --repo <org>/<repo> -- claude -p "<prompt>"
oh-my-cpa key exec --runtime codex    --repo <org>/<repo> -- codex exec "<prompt>"
oh-my-cpa key exec --runtime opencode --repo <org>/<repo> -- opencode run "<prompt>"
```

`exec` prints nothing of its own. **Success:** the child runs and its output appears; the
child's exit code becomes `key exec`'s exit code once the child has started.
**Failure before the child starts:** `key_not_found` (exit 4) — run `key gen`; 
`envelope_missing` (exit 4) — run step 5; `base_url_required` (exit 3); `runtime_unknown`
(exit 2).
**OpenCode only:** `exec` and `install` read `GET <base-url>/v1/models` with the repository
key before starting. If the gateway cannot be reached, the command stops with
`models_unavailable` (exit 1) and nothing is written or started. Fix the gateway address or
network; if the machine must stay offline, pass `--models id1,id2` (only valid for
`--runtime opencode`). If the gateway answers but names no models, `models_required` (exit 3).

Persisting (only when the runtime cannot be started through `exec`):

```bash
oh-my-cpa key install --runtime <claude|codex|opencode> --repo <org>/<repo>
```

`install` is the only command that leaves the plaintext on disk; it backs up the previous
file and refuses a slot it did not write or one owned by another repository (exit 5,
`config_owned_by_another_repo`; `--replace` to take it over). Only user-level files are
written — a project-level `.claude/settings.json` would be committed with the key in it.

### Step 7 — clean up

```bash
oh-my-cpa key purge --repo <org>/<repo> --dry-run
oh-my-cpa key purge --repo <org>/<repo>
oh-my-cpa key exec --runtime claude --repo <org>/<repo> -- claude -p "should fail"
```

**Success:** `--dry-run` lists what would go; the real run removes the private key, envelope,
installed values and the local record (exit 0); the final `exec` fails with `key_not_found`
(exit 4). That failure is the proof the local material is gone.
**The key in CPA is still active after a purge.** Ask the operator to stop it in the console
if the repository should no longer have one.

## 6. Exit codes and failure branches

| Exit | Meaning |
| --- | --- |
| 0 | success, including "already existed, reused" and "already gone" |
| 1 | operation failed (I/O, decrypt, backup, gateway or remote refusal, an item left behind) |
| 2 | usage error (unknown subcommand, missing `--runtime`, a command Codex will not accept) |
| 3 | configuration missing or invalid |
| 4 | a key, envelope, application or installed value was not found |
| 5 | conflict: a different key or value already occupies the place, or state is corrupt |

`key exec` returns the child's exit code once the child has started, so the table applies only
to failures before that point. With `--json`, a failure writes one line
`{"code":"<code>","message":"<sentence>"}` to stderr; the exit code is unchanged. The full
code list is in [`repo-key-agent.json`](repo-key-agent.json).

| Code | Exit | What to do |
| --- | --- | --- |
| `agent required` (result detail: `The agent name is required ...`) | `invalid_parameters` result | Set `X-Omc-Agent` (or `OMCPA_AGENT_NAME`) and reconnect the MCP session. |
| `The device name could not be determined` | `invalid_parameters` result | Set `X-Omc-Device` (or `OMCPA_DEVICE_NAME`) and reconnect. |
| `capability_forbidden` | HTTP 400 | The credential is read-only or lacks the tool. Apply needs the management key or a console-filed application. |
| `invalid_tool_arguments` | HTTP 400 | The arguments do not match the tool schema (for example an extra `agent` field). Use only the documented arguments. |
| `invalid_parameters` | error result | A required field is missing or too long. Read the detail. |
| `operation_failed` on `repo_key_ciphertext` | error result | The application is not approved yet; the server does not expose the pending reason. Wait for the decision. |
| `approval_link_expired` | approval page | Apply again for a fresh application and link. |
| `models_unavailable` | 1 | OpenCode could not read the gateway's `/v1/models`. Fix the gateway address/network, or pass `--models`. Nothing was written or started. |
| `models_required` | 3 | The gateway named no usable model ids. Pass `--models id1,id2`. |
| `key_not_found` | 4 | No private key on this machine. Run `key gen`. After a purge this is expected. |
| `envelope_missing` | 4 | Fetch the ciphertext and run `key accept`. |
| `envelope_key_mismatch` | 1 | The envelope is sealed to another key pair; check `$OMC_HOME` and the repository. |
| `config_owned_by_another_repo` | 5 | Purge the other repository's install, or pass `--replace`. |
| `home_insecure` | 1 | `$OMC_HOME` cannot be made owner-only. Let `key` create it under your home directory. |
| `remote_not_configured` | 3 | `status --remote` lacks `OMCPA_SERVER_URL` or the credential; local state is unchanged. |
| `remote_unavailable` | 1 | OMC could not be reached; local state is unchanged. |
| `state_locked` | 1 | Another `key` write holds the lock. Wait for it; do not delete `.lock`. |

## 7. Done means

- `oh-my-cpa key status --json` shows the repository with `envelope_present: true` and
  `approval_status: "approved"`.
- One real request through `key exec` succeeds (its exit code is the child's).
- If you purge: the same `key exec` fails with `key_not_found`, exit 4.

## Short version for a system prompt

```text
You have an Oh My CPA repository key. Work only through `oh-my-cpa key`:
1. export OMC_HOME=~/.omc OMCPA_CPA_BASE_URL=https://api.concertcalendar.cloud
   OMCPA_SERVER_URL=https://omc.madebynight.top/omc OMCPA_AGENT_NAME=<agent> OMCPA_DEVICE_NAME=<host>
2. `oh-my-cpa key gen --repo <org>/<repo>` -> stdout is the public key (x25519:...).
3. Apply through OMC MCP (`repo_key_apply`) or POST the capability endpoint with
   `Authorization: Bearer $OMCPA_CPA_MANAGEMENT_KEY`, `X-Omc-Agent`, `X-Omc-Device`.
   Give `approval_url` to the human; never approve it yourself. Poll `repo_key_status`.
4. Fetch `repo_key_ciphertext` and pipe it into
   `oh-my-cpa key accept --repo <org>/<repo> --application-id <id>`.
5. Run `oh-my-cpa key exec --runtime claude|codex|opencode --repo <org>/<repo> -- <cmd>`.
6. Never print the key, the private key or the envelope; never commit them; never put the
   key in a command line or project config. `key purge` removes local material only.
Exit codes: 0 ok, 1 failed, 2 usage, 3 config, 4 not found, 5 conflict. Read the full
runbook at docs/guides/repo-key-for-agents.md.
```
