# Using a repository key (for a coding agent)

This runbook is for a coding agent that has to make a request to a CLIProxyAPI (CPA)
gateway with a key owned by the repository it is working in, instead of a key owned by the
person driving it. Oh My CPA (OMC) issues those keys through its MCP capabilities; this
runbook covers the client side — the `oh-my-cpa key` subcommands that hold the private key,
open the delivery envelope, and hand the plaintext to one process.

The protocol side of the same feature — what `repo_key_apply`, `repo_key_status` and
`repo_key_ciphertext` accept and return — is `docs/agent-capabilities.md`. This page does
not repeat it; it says what to run, what a successful run looks like, and what to do when
it fails.

For the shorter, copy-paste runbook written for an agent that has to get a repository key
from scratch — install, MCP headers, the seven steps, exit codes and the machine-readable
summary — see `docs/guides/repo-key-for-agents.md` (JSON: `docs/guides/repo-key-agent.json`).

Everything below assumes the OMC binary is already installed (`docs/install.md`) and that
the console the key comes from is reachable over MCP. `key` is dispatched before OMC loads
its configuration, opens its database, or builds a CPA client: it works on a machine with
no console data directory, and it needs no `.env`.

## Rules

1. **Get the plaintext into one process, not onto a disk.** `key exec` is the default path.
   `key install` is the only command that leaves plaintext in a file, and it says so.
2. **Never print the key.** Not to stdout, not to a log, not into a chat message, not into a
   commit, not into a screenshot of a terminal. `key` itself never does; do not add a step
   that does.
3. **Never commit a runtime configuration that holds the key.** Claude Code's
   `.claude/settings.json`, Codex's `config.toml` and OpenCode's `opencode.json` are
   plaintext stores. A project-level copy of any of them goes to the repository.
4. **Pass the ciphertext result to `key accept`; do not write the envelope file yourself.**
   A shell redirect produces whatever mode the shell's umask happens to give, and Windows
   has no `0600` semantics at all. `key accept` validates the envelope against the local
   private key before storing it.
5. **`key purge` is local.** It removes what this machine holds. The key stays active in
   CPA until someone stops or reissues it on the console's Repository keys page (`/repo-keys`).

## 1. Before the first command

### The binary

```bash
oh-my-cpa key --help
```

That prints the six subcommands and the environment variables below. A missing `key`
subcommand means an OMC build older than this feature; upgrade before continuing.

### `OMC_HOME`

`key` keeps everything it owns under one directory: the private keys, the delivery
envelopes, the configuration backups, the local state, and the remembered gateway address.

| Resolution | Value |
| --- | --- |
| `--home <dir>` | used as given; a relative path resolves against the current directory |
| `OMC_HOME` | used as given |
| neither | `~/.omc` (`%USERPROFILE%\.omc` on Windows) |

The directory is created with owner-only permissions: `0700` on Unix, and on Windows a
DACL that names only the current user with inheritance removed. A directory that cannot be
locked down stops the command with `home_insecure` rather than writing a private key other
accounts can read. On Windows, `key` restricts a directory it creates; it checks one that
already exists but does not re-ACL it. An existing directory that still inherits its
parent's permissions is refused even when the inherited entries are not broad, so let `key`
create `OMC_HOME`, or remove inheritance and grant only your own account first:

```powershell
icacls "$env:USERPROFILE\.omc" /inheritance:r /grant:r "${env:USERNAME}:(OI)(CI)F"
```

FAT32, exFAT and some network shares cannot carry an ACL at all; put `OMC_HOME` on an NTFS
path under the user profile.

```text
$OMC_HOME/
  keys/        private keys
  envelopes/   delivery envelopes (ciphertext)
  backups/     copies of runtime configuration taken before an install
  state.json   repositories, applications, envelopes, installed slots
  config.json  the remembered gateway address
  .lock        short write lock
```

**One directory is one set of keys.** The default is one per operating-system user, so two
agents running as the same user share one key pair. Where each agent is supposed to hold
its own key, give each its own directory and set the variable in that agent's environment
(not only in the shell that happens to run `key gen`):

```bash
export OMC_HOME="$HOME/.omc/claude"     # Unix
```

```powershell
$env:OMC_HOME = "$env:USERPROFILE\.omc\codex"   # Windows PowerShell
```

A shell without `OMC_HOME` sees the default directory, so `key status` there reports the
default home's keys, not the agent-scoped ones. That is expected; pass `--home` to look at
a specific one.

### The gateway address

The key is used against one gateway. The address is resolved in this order:

1. `--base-url <url>` on `key gen`, `key exec` or `key install`
2. `OMCPA_CPA_BASE_URL`
3. `base_url` in `$OMC_HOME/config.json`
4. none of them: `base_url_required`, exit code 3

`key gen` is the recommended place to set it once. A successful `key gen` or `key install`
given `--base-url` remembers the value in `config.json`; `key exec` uses the address for
that one run and writes nothing. The environment variable and the file are never rewritten
behind the operator's back.

Only `http` and `https` URLs with a host are accepted, without userinfo, query or fragment.
The value is stored as the gateway root: a trailing `/` and one trailing `/v1` are removed.
A concrete endpoint such as `/v1/messages` or `/v1/responses` is refused with
`base_url_invalid` instead of being trimmed. A non-loopback `http://` URL is accepted.
`key exec` and `key install` each print a plain-language warning on stderr for it, because
a plaintext gateway on a private Docker network is a normal deployment; their wording
differs, and neither warning carries a stable code. `key gen` only records the address and
does not warn. Whether remote plaintext is acceptable is the operator's call, not this
tool's.

Each runtime gets the address in the shape it expects: Claude Code the root, Codex and
OpenCode the root plus `/v1`. Do not append `/v1` yourself.

### The console credential, for the MCP steps only

`key` itself never talks to OMC. Two things do: the MCP tools that apply, poll and fetch,
and the optional `key status --remote` refresh. Those need:

| Variable | Used for |
| --- | --- |
| `OMCPA_SERVER_URL` | the console URL, base path included, for `key status --remote` |
| `OMCPA_CPA_MANAGEMENT_KEY` | the credential for the MCP transport and for `--remote` |
| `OMCPA_AGENT_NAME` | the agent name the approval page shows |
| `OMCPA_DEVICE_NAME` | the device name the approval page shows |

Set `OMCPA_AGENT_NAME` and `OMCPA_DEVICE_NAME` before applying. A person approving the
request reads them, and a blank or copy-pasted identity is exactly what the approval page
is there to prevent. Never print `OMCPA_CPA_MANAGEMENT_KEY`.

## 2. The seven steps

The whole flow is: check what this machine holds, make a key pair, ask OMC for a key with
it, wait for a decision, take the sealed key, use it in one process, and clean up when
done. Steps 3 and 4 are MCP calls, not `key` commands.

```text
status -> gen -> apply (MCP) -> wait for the decision -> ciphertext (MCP) + accept
       -> exec -> install (only when it must persist) -> purge
```

### Step 1 — see what is already here

```bash
oh-my-cpa key status
oh-my-cpa key status --json
```

Offline by default: it reads `$OMC_HOME`, not OMC. Expect the home path, the remembered
gateway address (and which of the three sources supplied it), and one block per repository
with the private key, the envelope, the application, the last known approval state and the
runtimes that have it installed.

`--json` is the stable shape for a script. The top level carries `home`, `base_url`,
`base_url_source` (`flag` / `env` / `config` / `absent`), `orphan_files`
(`{"keys": n, "envelopes": n}`), `repos[]`, and `runtimes{}`.

Each `repos[]` entry carries:

| Field | Meaning |
| --- | --- |
| `repo`, `repo_display` | the normalized identity and the spelling that was submitted |
| `public_key`, `public_key_fingerprint` | the key pair's public half, omitted when there is no key |
| `private_key_file`, `private_key_present` | the path under `$OMC_HOME` and whether it exists |
| `envelope_file`, `envelope_present`, `envelope_received_at` | the stored envelope |
| `application_id`, `application_kind` | the recorded application |
| `approval_status`, `status_source`, `status_updated_at`, `decided_at` | the application's last known state, where it came from, and when |
| `key_mask` | the display mask CPA assigned |
| `repo_state`, `repo_state_updated_at` | the repository's own key state, which is a different question |
| `remote_error` | `remote_error:<code>` when the last `--remote` read for this repository failed |
| `installed` | `{"claude": true, ...}`, one entry per runtime that holds this key |

Each value in `runtimes{}` is `{"repo", "installed_at", "config_path", "backup_path"}`.
Read `approval_status` (`not_applied`, `unknown`, `pending`, `approved`, `rejected`,
`stopped`) and `status_source` (`gen`, `local`, `remote`, `accept`) together: the second
says where the first came from. `repo_state` (`active`, `stopped`, `none`, `unknown`) is the
repository's key state, which is a different question from this application's outcome — an
application can be rejected while the repository still has someone else's active key.

No base URL is not an error here; `base_url_source: absent` is a normal answer for a
machine that has a key and no gateway yet.

A repository with no key is reported as such; `key gen` (step 2) is the fix. Files in
`keys/` or `envelopes/` that `state.json` does not account for are reported as orphans —
they are not attributed to a repository, and `key gen --repo <repo>` rebuilds the record.

### Step 2 — make the key pair

```bash
oh-my-cpa key gen --repo org/repo --base-url http://127.0.0.1:8317
```

Expected stdout, one line and nothing else:

```text
x25519:<base64>
```

That public key is the only thing on stdout; the notice that a key was reused or that the
gateway address was remembered goes to stderr. The private key is written under
`$OMC_HOME/keys/`, derivable from the repository identity and never from a name you invent.

Running it again for the same repository is safe: the existing key is reused, its public
key is printed again, and any missing public key or fingerprint in `state.json` is restored.
`gen` never rotates a key silently. To replace one, `key purge` first, then `gen` — which
means a new application and a new decision, because the old one was approved for the old
public key.

The public key is not a secret. It is what you send in step 3.

### Step 3 — apply (MCP)

Call `repo_key_apply` with the repository, a purpose, and the `x25519:<base64>` public key
from step 2. The tool description and the result shape are in `docs/agent-capabilities.md`.

Record the `application_id` it returns. A caller the repository has been approved for
before, with the same public key, is approved without a person; anything else waits.

### Step 4 — wait for the decision

Associate the application with the repository locally so `status` can show it:

```bash
oh-my-cpa key status --repo org/repo --application-id <application-id>
```

This records the id and marks the approval state `unknown`, because a local command cannot
know what a person will decide. To read the real state, either poll `repo_key_status` over
MCP, or let `key` do it:

```bash
oh-my-cpa key status --remote
```

`--remote` needs `OMCPA_SERVER_URL` and `OMCPA_CPA_MANAGEMENT_KEY`; missing either is
`remote_not_configured` (exit 3) and changes nothing. It refreshes every recorded
application and repository, and it is the only networked path in the whole subcommand
family. A failure for one repository is reported as `remote_error:<code>` while the others
still print, and the command then exits 1. Without `--remote`, `status` stays fully offline
— use it that way whenever the console is unreachable.

Do not fetch the ciphertext before the application is approved.

### Step 5 — take the sealed key

The result of `repo_key_ciphertext` comes back in the conversation. Hand it to `key accept`
on stdin; never write the envelope file by hand. On a shell that supports heredocs, feeding
it that way keeps the value out of the process arguments:

```bash
oh-my-cpa key accept --repo org/repo --application-id <application-id> <<'JSON'
{"application_id":"<application-id>","kind":"issue","key_mask":"sk-cpa-ab***wxyz",
 "algorithm":"x25519-hkdf-sha256-aes256gcm",
 "ciphertext":"{\"v\":1,\"alg\":\"x25519-hkdf-sha256-aes256gcm\",\"epk\":\"…\",\"nonce\":\"…\",\"ciphertext\":\"…\"}"}
JSON
```

Substitute the actual tool result for the body — the values above are the shape, not sample
data. Piping works the same way: whatever holds the result, `… | oh-my-cpa key accept --repo
org/repo`.

`accept` unwraps one level: the `repo_key_ciphertext` result object, the same object inside a
capability envelope (`{"status":"success","data":{…}}`), the inner envelope as a JSON string
or as an object, or a bare envelope
(`{"v":1,"alg":"…","epk":"…","nonce":"…","ciphertext":"…"}`). Anything else is
`envelope_invalid`.

Expected stdout, one line:

```text
accepted <repo-display>: key <key-mask> at <envelope-file>
```

`<repo-display>` is the spelling submitted, followed by the normalized identity in
parentheses when the two differ (`Org/Repo (org/repo)`); `<envelope-file>` is the path under
`$OMC_HOME/envelopes/`. Accepting again for the same repository prints
`replaced the envelope for <repo-display>: ...` instead of `accepted ...`.

`--json` prints a pretty-printed object instead of that line, with `repo`, `repo_display`,
`application_id`, `application_kind`, `key_mask`, `approval_status`, `status_source`,
`envelope_file`, `envelope_received_at`, `envelope_algorithm`, and `replaced`. Nothing from
the input is echoed, and input above 1 MiB is refused.

Before storing anything, `accept` checks the algorithm and the envelope version, then opens
the envelope once in memory with the local private key. Only if that succeeds does it write
`$OMC_HOME/envelopes/`. This is deliberate: an envelope sealed to a different key is refused
before it can reach the disk, so "wrong repository, wrong key pair" fails here instead of at
request time.

Repeating an accept for the same repository replaces the stored envelope, as long as the
local key can still open it.

The ciphertext is not a plaintext, but it is also not a public artifact: it can be forwarded
and only the matching private key stops it. Do not commit it, paste it into an issue, or
attach it to a message, and do not use `key accept` output as a place to keep it.

### Step 6 — run something with the key

```bash
oh-my-cpa key exec --runtime claude --repo org/repo -- claude -p "read the files in this repository and summarise them"
```

`exec` finds the key and the envelope, resolves the gateway address, decrypts in memory, and
starts the child with the variables its runtime reads. The plaintext exists only in this
process's memory and in the child's environment: no temporary file, no command-line
argument, no stdout, no log line.

`--repo` may be omitted: the repository is inferred from the current directory's git
origin, or from its first remote, or from the directory name. Inside a repository, running
from its root is the reliable choice; `--repo` is the explicit one.

The child inherits stdin, stdout, stderr, the rest of the environment, and the working
directory. `key exec` prints nothing of its own; warnings go to stderr. Once the child has
started, its exit code is `key exec`'s exit code, so a child that exits 3 is not a
configuration error. The 0–5 table in §5 applies only to failures before the child starts.

Ctrl-C reaches parent and child together. Stop the child with whatever the runtime expects
instead of killing `key exec` on its own; a parent that is terminated alone can leave the
child running.

A missing private key is `key_not_found` (exit 4) with the repository and a ready-to-copy
`key gen` line; a missing envelope is `envelope_missing` (exit 4) pointing at `key accept`.
Neither starts the child.

### Step 7 — make it persist, or clean up

`exec` covers the normal case. Write the key into a runtime's own configuration only when
the runtime cannot be started through `key exec`:

```bash
oh-my-cpa key install --runtime codex --repo org/repo
```

`install` is the only command that leaves plaintext on disk, and it says so on stderr. When
the target file already exists, it copies it to `$OMC_HOME/backups/` first and refuses to
write if the copy fails; a first install has no previous file to back up. After writing it
re-reads the file to confirm the change and the preserved keys, and restores the backup if
that check fails. A slot already holding a value this tool did not write needs `--replace`.
One slot per runtime per machine: installing another repository replaces the slot only with
`--replace` (or after `key purge --repo <the other repository>`); without it the install
stops with `config_owned_by_another_repo` (exit 5), and the previous values survive only in
the backup.

Then remove what this machine holds:

```bash
oh-my-cpa key purge --repo org/repo --dry-run
oh-my-cpa key purge --repo org/repo
oh-my-cpa key purge --repo org/repo --runtime codex
```

`--dry-run` prints what would be removed and changes nothing. Without `--runtime` it removes
the private key, the envelope, every installed value belonging to this repository, and the
local state entry. With `--runtime` it removes only that runtime's installed values and
keeps the key and the envelope. A value in a runtime's configuration is removed only when
its fingerprint matches this repository's key; a value someone else has changed since is
left in place and reported as `install_not_owned`. Backups are kept, except those that
contain this repository's plaintext. Anything that belongs to this repository and could not
be removed is listed and the command exits 1 with `purge_pending`, so it can be rerun.

Afterwards the same `key exec` fails with `key_not_found`, which is the expected proof that
the local material is gone.

**The key in CPA is still active after a purge.** To revoke it, stop or reissue it in the
console. Deleting local material and deactivating the key are two different actions, and
this command only does the first.

## 3. The three runtimes

### Claude Code

`exec`:

```bash
oh-my-cpa key exec --runtime claude --repo org/repo -- claude -p "<prompt>"
```

| Item | Value |
| --- | --- |
| Gateway address | `ANTHROPIC_BASE_URL=<root>` |
| Credential | `ANTHROPIC_AUTH_TOKEN=<plaintext>` |
| Removed for the child | `ANTHROPIC_API_KEY`, and any inherited `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_BASE_URL` |

The removal is not tidying: an inherited `ANTHROPIC_API_KEY` can take precedence and send
the request with the wrong credential, so it is dropped for this child and named on stderr
without its value.

`install` writes the user-level `~/.claude/settings.json` (or
`$CLAUDE_CONFIG_DIR/settings.json`, or `--config <path>`): `env.ANTHROPIC_BASE_URL` and
`env.ANTHROPIC_AUTH_TOKEN`, edited structurally so every other key in the file survives. An
existing `env.ANTHROPIC_API_KEY` is refused with `config_conflict` rather than silently
outranked.

**Do not use project-level settings.** `--config` pointed at a repository's
`.claude/settings.json` writes the plaintext into a file that is committed and shared with
everyone who clones the repository. Install at user level, or use `exec`.

If the machine is signed in to Claude Code with an OAuth account, check the credential that
is actually used before reporting success: whichever of the environment variable and the
stored login wins decides whether the repository key was used at all.

> TODO(实现确认): whether `ANTHROPIC_AUTH_TOKEN` outranks an OAuth login in the installed
> Claude Code version is not measured yet. Until it is, run `claude auth status` and sign
> out if the run must be certain to use this key.

### Codex

`exec`:

```bash
oh-my-cpa key exec --runtime codex --repo org/repo -- codex "read the files in this repository and summarise them"
```

| Item | Value |
| --- | --- |
| Gateway address | `model_providers.oh-my-cpa.base_url=<root>/v1` |
| Credential | `OMC_REPO_KEY=<plaintext>`, referenced by `env_key` |
| Added arguments | `-c model_provider="oh-my-cpa"`, `-c model_providers.oh-my-cpa.name`, `.base_url`, `.env_key`, `.wire_api="responses"` |

The token goes through an environment variable, not `-c`, because a command line is visible
to other local processes. The `-c` overrides are session-level: they beat the user's
`config.toml` without editing it.

**The command must be `codex` itself** (`codex` or `codex.exe`, any case). A wrapper script,
a shell function, or `npx` is refused with `codex_command_required` (exit 2), because
`key exec` inserts its overrides immediately after the program name and cannot know where a
wrapper would put them. Codex's own subcommands, `codex exec` included, are fine. Use
`key install` or call `codex` directly.

`install` writes `$CODEX_HOME/config.toml` or `~/.codex/config.toml`: a managed
`[model_providers.oh-my-cpa]` table (marked, so it can be replaced and removed cleanly) plus
the top-level `model_provider`. The token is stored as `experimental_bearer_token` in
plaintext, and `wire_api` is `responses` — `chat` is refused by Codex itself. A
same-named provider table that this tool did not write is refused instead of adopted.

### OpenCode

`exec`:

```bash
oh-my-cpa key exec --runtime opencode --repo org/repo -- opencode --version
```

| Item | Value |
| --- | --- |
| Gateway address | `provider.oh-my-cpa.options.baseURL=<root>/v1` |
| Credential | `provider.oh-my-cpa.options.apiKey=<plaintext>` |
| Models | `provider.oh-my-cpa.models`, one member per model id |
| Transport | `OPENCODE_CONFIG_CONTENT`, holding the user's configuration merged with ours |

Any OpenCode invocation works in place of the smoke check above, because the provider
arrives through the environment rather than through the command line.

**The model table is required.** OpenCode refuses a custom provider that declares no models
(`ProviderModelNotFoundError: Model not found: oh-my-cpa/<model>`), so both `exec` and
`install` name the models explicitly. By default they ask the gateway: `GET <root>/v1/models`
with this repository's key as a bearer token, and every id the gateway advertises becomes a
member. A gateway that cannot be reached, answers with a non-200 status, or answers
something that is not a model list stops the command with `models_unavailable` (exit 1)
before a child starts or a file is written: a provider OpenCode would refuse is never
produced. `--models a,b,c` (OpenCode only) names the list by hand, skips the request, and
keeps the run offline. Either path writes the same sorted, deduplicated ids.

There is no file to write for the duration of the run: the configuration travels in the
process environment. `key exec` reads `$OPENCODE_CONFIG` or
`~/.config/opencode/opencode.json`, merges an already-set `OPENCODE_CONFIG_CONTENT` on top
of it, adds the `provider.oh-my-cpa` entry, and passes the whole document in the child's
`OPENCODE_CONFIG_CONTENT`. Merged content above 24 KiB is refused with `config_too_large`
(exit 3) because the Windows environment block is limited; use `key install` there.
Configuration that is not pure JSON — a JSONC file with comments, for instance — is refused
with `config_not_json` rather than rewritten.

`install` writes `~/.config/opencode/opencode.json` (or `$OPENCODE_CONFIG`):
`provider.oh-my-cpa` with `npm`, `name`, `models`, `options.baseURL` and `options.apiKey`.
Other providers and keys are preserved. Each install replaces this tool's model table with
the list it just read or was given, so a model the gateway dropped does not linger; other
members under `provider.oh-my-cpa` and every other provider are left alone. A repeated
install over the same list changes nothing and takes no second backup.

One limit is a decision, not a gap:

- The **singular v1 `provider` key** is written. Some releases document a plural `providers`
  schema; the installed version may ignore it, so check `opencode --version` when a provider
  does not appear. A future schema change means an OMC change, not a hand edit here.

## 4. Never do these

| Do not | Why |
| --- | --- |
| Print the key, the private key file, or the decrypted envelope | Anything printed goes to a terminal scrollback, a build log, a CI log, or a shared screen. The value is then out of its one-process boundary. |
| Put the key in a prompt, a chat message, an issue, a PR body, or a review comment | A conversation is retained, posted, summarised, and may be exported. The plaintext is not recoverable by deleting one message. |
| Commit a runtime configuration that holds the key | Claude Code's project `.claude/settings.json`, Codex's `config.toml`, OpenCode's `opencode.json` are plaintext stores. Committed, the key is in history for everyone who clones, and history is not edited away. |
| Put `$OMC_HOME` inside a repository | The directory holds private keys and envelopes. Inside a worktree it is one `git add -A` from being committed, and it is copied by anything that copies the tree. |
| Write the envelope with a shell redirect | The resulting file mode is whatever the umask gives, and Windows has no `0600`. `key accept` is the only writer that validates and restricts. |
| Echo the key into a variable and then into a file | This is `install` without the backup, the warning, or the fingerprint check. Use `key install`. |
| Share the ciphertext because it is "only encrypted" | It is not a secret in the private-key sense, but anyone holding it can forward it, and nothing but the private key stands between it and the plaintext. |
| Use `key purge` as a revocation step | It clears this machine only. The key in CPA stays active. |

## 5. Failures and what they mean

Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | success, including "already existed, reused" and "already gone" |
| 1 | the operation failed (I/O, decrypt, backup, remote refusal, an item left behind) |
| 2 | usage error (unknown subcommand, missing `--runtime`, a command Codex will not accept, unknown flag) |
| 3 | configuration missing or invalid |
| 4 | a key, envelope, application or installed value was not found |
| 5 | conflict: a different key or value already occupies the place, or the state is corrupt |

`key exec` returns the child's exit code once the child has started.

Error codes are stable and are written at the front of a text message. With `--json`, a
failure writes one JSON object to stderr instead, and leaves stdout as it is:

```json
{"code":"<code>","message":"<sentence>"}
```

The exit code is unchanged, and the object is one line, so a caller can read stderr a line
at a time. `unexpected` is the code for a failure that carried no stable code of its own.
`status` and `accept` use `--json` for their success document as well; the other subcommands
accept it to select this failure envelope and keep their normal output on success. The
common codes:

| Code | What to do |
| --- | --- |
| `base_url_required` | No `--base-url`, no `OMCPA_CPA_BASE_URL`, no `config.json` entry. Set one; `key gen --base-url` remembers it. |
| `base_url_invalid` | The URL is not http/https, has userinfo, a query or a fragment, or names a concrete endpoint. Give the gateway root. |
| `home_unavailable` | `$OMC_HOME` cannot be created or written. Fix the path or its ownership. |
| `home_insecure` | The directory cannot be restricted to this user, or it already lets group/other write. On Windows an existing directory that still inherits its parent's ACL is refused even when the inherited entries are not broad: `key` restricts a directory it creates but does not re-ACL one it did not. Let `key` create `OMC_HOME` under the user profile, or remove inheritance and grant only your account (`icacls`). Nothing was written. |
| `repo_required` | No `--repo`, and the current directory does not identify a repository. Run from the repository root or pass `--repo org/repo`. |
| `repo_invalid` | The repository identity is not usable. `org/repo` is the expected shape; a bare directory name works for a checkout with no remote. An absolute path or one with `..` segments is refused: run the command from the directory, or pass its name. |
| `key_not_found` | This repository has no private key here. `key gen --repo <repo>`. |
| `key_unreadable` | The private key file cannot be read or is not 32 bytes. Do not "fix" it by generating another one over it; `key purge` first. |
| `envelope_missing` | The application was approved but the envelope was never stored. Run `repo_key_ciphertext` and pipe it into `key accept`. |
| `envelope_invalid` | The input is not an envelope, or its version or algorithm is not the one OMC issues. Pass the tool result as it came back. |
| `envelope_key_mismatch` | The envelope is sealed to a different key pair, so nothing was written. Check the repository and which `OMC_HOME` this shell uses. |
| `decrypt_failed` | The envelope and the private key no longer agree, or the ciphertext is damaged. Nothing was started. Re-fetch the ciphertext, and `key purge` then `key gen` if the key pair itself is the problem. |
| `runtime_unknown` | `--runtime` is not `claude`, `codex` or `opencode`. |
| `command_required` | `exec` was given no `-- <command>`. |
| `codex_command_required` | `--runtime codex` was given a command that is not `codex`. Call `codex` directly or use `key install`. |
| `config_conflict` | A value or managed block this tool did not write occupies the slot, or Claude Code already has `env.ANTHROPIC_API_KEY`. Resolve it by hand or pass `--replace` where that is the meaning. |
| `config_owned_by_another_repo` | The slot belongs to another repository. `key purge --repo <that repo>`, or `--replace`. |
| `config_not_json` | The runtime configuration is not pure JSON — likely JSONC. Nothing was rewritten. Edit it by hand or point `--config` elsewhere. |
| `config_too_large` | The OpenCode configuration merged for `exec` exceeds 24 KiB. Use `key install`. |
| `models_required` | The OpenCode provider has no model ids. Pass `--models a,b,c`; OpenCode refuses a provider that declares no models, so nothing was written or started. |
| `models_unavailable` | `GET <root>/v1/models` could not be read with this key — the gateway was unreachable, answered non-200, or did not return a model list. Nothing was written or started; pass `--models a,b,c` to work offline. |
| `backup_failed` | The backup could not be written, so the target file was not touched. |
| `install_not_owned` | The configured value no longer matches this repository's key; it was left alone. Reinstall it if it should be this repository's. |
| `purge_pending` | Something belonging to this repository could not be removed; it is listed. Rerun after fixing it. |
| `remote_not_configured` | `status --remote` is missing `OMCPA_SERVER_URL` or `OMCPA_CPA_MANAGEMENT_KEY`. Local state is unchanged. |
| `remote_unavailable` | OMC could not be reached. Local state is unchanged. |
| `state_locked` | Another `key` write command holds the lock. Rerun once it finishes; do not delete `.lock` while a command is running. |
| `exec_failed` | The child could not be started at all (missing program, permission, working directory). |

Everything a failure prints is bounded to the repository name, the key mask, the public key
fingerprint, a path, the runtime, and a child exit code. No message repeats the private key,
the plaintext, the envelope body, the management key or an approval token.

## 6. When to do it again

| Situation | What to do |
| --- | --- |
| A different machine | The key pair is local, so the new machine needs its own: `key gen`, then apply with the new public key and get it approved. Copying `$OMC_HOME` moves the private key with every risk of a copied secret; do not. |
| A different agent name | Each agent that should hold its own key pair gets its own `OMC_HOME`. Set it in that agent's environment and run the seven steps there; the old directory is untouched. |
| The private key is lost or the home was deleted | This key cannot be recovered. `key gen` for a fresh pair, apply again with the new public key, and wait for the decision. The previous application was approved for a key that no longer exists. |
| The envelope was never stored, or was stored for the wrong key | `repo_key_ciphertext` again and pipe it into `key accept`. A mismatched envelope is refused, so accepting twice is safe. |
| The whole repository's access should stop | `key purge --repo <repo>` on each machine that holds it, then stop the key in the console. The local purge does not deactivate anything in CPA. |
| A runtime should stop using the key but keep the machine's key | `key purge --repo <repo> --runtime <runtime>`. |

## 7. Where the plaintext is, and what that costs

The design puts the plaintext in exactly two places for the length of a run: this process's
memory and the child's environment. That is a real boundary, but a narrow one:

- **Same-user processes can read it.** On Linux, `/proc/<pid>/environ` of the child is
  readable by the same user (and by root) for as long as the child lives. A process that can
  read your environment is a process that already runs as you; the boundary here is between
  accounts, not between programs.
- **A memory dump or a debugger gets it.** On Windows the environment block lives in the
  child's memory, so a full memory dump or an attached debugger contains the key.
- **`install` leaves plaintext on disk.** That is the trade it makes, and it is why it warns
  and is never the default. The runtime's own configuration file is a plaintext store from
  then on, readable by anything that reads your user configuration.
- **A backup can hold more than this key.** An install copies the whole previous file, which
  may contain other credentials. The backup lives under `$OMC_HOME/backups/`, restricted to
  this user, and `key purge` removes only the backups that contain this repository's key;
  the rest stay until someone deletes them.
- **The child can pass it on.** Whatever the runtime does with the key — writing it to its
  own logs, failing a request with it, sending it to a plugin — is outside `key`'s control.
  A runtime that logs its own environment defeats the boundary above.
- **`state.json` and `config.json` hold no secrets.** They keep paths, timestamps, the key
  mask, the public key and its fingerprint, and a SHA-256 fingerprint of the plaintext used
  to decide whether a configured value is still this repository's. None of those is the key.

Within your own account, treat the key as visible to anything running as you, and keep it
out of everything that leaves the account: logs, transcripts, repositories, tickets,
screenshots, and backups you do not control.
