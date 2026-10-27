# Agent capabilities

The console's **Agent** page (`/agent`), the remote MCP endpoint (`/api/mcp`) and the
`oh-my-cpa mcp` stdio bridge all drive Oh My CPA through one capability registry. This document is the contract for adding to
it; see `docs/adr/0026-one-capability-registry-over-shared-operations.md` for why the
registry exists in this shape.

## Shape

```text
Agent page ──▶ internal/agent.Runtime ──▶ CPA gateway (model + tool calls)
                      │
                      ▼
                internal/capability.Executor
                      │  validate → authorize → confirm → audit
                      ▼
                internal/operations.Service
                      │
        repository / pricing / quota / management client
```

- `internal/operations` holds the business operations. A console HTTP handler and a
  capability call the same method; neither re-implements validation, write gates,
  revisions, or CPA readback.
- `internal/capability` owns declarations, JSON Schema inference, argument and result
  validation, permission and risk rules, the pending-operation store, and the audit
  trail.
- `internal/agent` owns the model loop: conversation persistence, budgets, streaming,
  and resumption, plus the two things that belong to the conversation rather than to OMC -
  `ask_question` and the display tools. It never touches the database, configuration, or
  the CPA client directly. The console reaches it over AG-UI (`internal/agui`, ADR 0041).
- `internal/mcpbridge` maps the registry to MCP tools for external agents, once, for
  both MCP transports (ADR 0070). It carries no business logic and no approval policy.

## Declaring a capability

### What the model is offered

Every registered capability is declared to the model as a tool from the first round of
every turn, in name order, with its description and inferred input schema. Discovery is
not a step: gating the tool list behind a search and loading a subset per conversation is
the obvious way to shrink a request, and it costs one whole model round trip before any
work can start - while making "the agent does not do this" indistinguishable from "the
agent has not been told about this yet".

The consequence to keep in mind when adding capabilities is the opposite of the usual one:
**the catalogue is paid for on every model call of every turn**, so a long description is a
recurring cost rather than a one-off. `TestAgentCatalogueFitsTheSchemaBudget` in
`internal/api/agent_http_test.go` keeps the complete catalogue within 64 KiB as a regression
assertion. It is not a startup or runtime refusal gate: the runtime always declares the full
catalogue and does not truncate requests to an estimated context budget. Actual provider
context limits still apply.

The system prompt is paid for the same way. It is built from named sections in
`internal/agent/prompt.go` - identity, approach, data, safety, wording, presentation, context - bounded by
`agent.MAX_PROMPT_BYTES` (6 KiB), and every turn records the `agent.PROMPT_VERSION` it ran with.
A capability's guidance belongs in its `Description`, where it is paid for only by the tool that
needs it; a rule that holds across capabilities belongs in one section, and changing a section's
wording bumps the version.

The wording section is a contract on answers, not on capabilities: name a provider or channel by
its display name, a client key by its alias, a credential by its label, a model by the name
clients request, and keep identifiers out unless asked; describe a field instead of quoting it;
write token counts in the operator's unit style (`console_token_style`: `en-compact`, `zh` or
`full`), costs in USD, durations in ms / s / min, shares as percentages and times as clock times in
the deployment's zone. A capability helps it by returning the display name beside every
identifier it returns - a result that carries only ids leaves the model nothing readable to say.

A declaration is one call to `capability.Register` with:

| Field | Meaning |
| --- | --- |
| `Name` | Stable `snake_case` adapter-facing tool name; duplicates fail startup |
| `Description` | What the tool does and what it does not reveal; shown to the model |
| `Version` | Incremented when a pending operation's meaning changes |
| `Permission` | `read`, `write`, or `destructive` |
| `Risk` | `low` or `high`; `high` requires a prepare function and human confirmation |
| `HumanInput` | Optional browser handoff: `secret` or `oauth` on a `high` risk write, or `answer` on a read that waits for the operator's reply (`ask_question`) |
| `Adapters` | `agent`, `mcp`, or both; exposure is explicit, never implied |
| `Invalidates` | React Query keys the UI should refresh after a successful write |

The handler receives a typed input struct. The registry infers the input and output JSON
Schemas from those types, validates arguments against them (unknown fields are
rejected), re-validates the raw arguments at execution time, and validates the typed
result before it reaches a model. Both payloads are capped at 32 KiB.

### Read capabilities

Reads execute immediately and return their validated result. They must aggregate or
paginate on the server: a bounded page, a bounded group count, or a summary, never a
whole table. Follow the existing budget conventions - 20 rows per request page (100
maximum), 50 groups with the remainder folded into `other`, 120 trend buckets.

### Write capabilities

Low-risk writes (display-only metadata such as a client-key alias) may execute
automatically. Everything that changes routing, credentials, availability, or
configuration is `high` risk and goes through the executor's confirmation flow:

1. The model calls the tool.
2. The `Prepare` function reads the current state and returns a structured preview:
   `Target`, `Revision` (the identity and version the approval is bound to), and
   `Changes`.
3. The executor stores the pending operation encrypted, writes a `prepared` audit
   event, and returns `{status: "pending", operation_id: ...}` to the caller.
4. The Agent's run finishes with an AG-UI interrupt naming the operation and the call that
   raised it. The console draws an approval card under that call (ADR 0043), and a signed-in
   operator makes one decision: allow or deny (ADR 0035). Approval re-validates the
   capability version, the caller's authority, and the resource revision inside the same
   write gate that performs the change; deciding resumes the Agent's run without a separate
   step.
5. The result is recorded (`success`, `partial`, `error`, or `uncertain`) and audited.
   An operation is consumed once; a repeated approval returns the stored outcome.

Rules that are not optional:

- A destructive capability is marked `destructive`, which the approval card states as an
  irreversible change. A batch operation must show the resolved target list and its
  count, and must never expand a wildcard at execution time.
- The approval is bound to the revision captured by `Prepare`; if the target changed,
  the executor returns `resource_conflict` and the operator must re-confirm.
- A write that may have reached CPA but cannot be verified is reported as `uncertain`
  (`operation_outcome_unknown`), never as a plain error and never retried
  automatically.
- A write whose result fails output validation is also `uncertain`, because the change
  may already have been applied.
- An operation this process is executing right now reads as `executing`. Only an
  `executing` record nothing is running - one a restart left behind - reads as
  `uncertain`.

Pricing is the worked example of a revision-bound write. `pricing_set` decides how
one model is priced (`auto`, `linked` to an OpenRouter id, or `custom` rates with
optional tiers) and `pricing_channel_set` sets one channel multiplier; both are `high`
risk and are bound to the revision of the current price list or channel list, so an
edit made in the console between preparation and approval returns `resource_conflict`.
`pricing_delete` and `pricing_channel_delete` are destructive. None of them reprices a recorded request (ADR 0030).

A refusal the caller can correct says what to change. `operations.invalidParameters`
keeps the stable `invalid_parameters` code and implements `capability.DetailedError`,
and the executor returns a detailed refusal raised by `Prepare` as an `error` result
with that `detail` instead of a bare failure - on both adapters, before any pending
operation or approval exists. Pricing uses it for every rejected rate, tier, zone,
mode and OpenRouter id; reuse it rather than returning a bare code from a new
capability whose arguments an agent could plausibly get wrong.

### Asking the operator

`ask_question` is the one capability that belongs to the conversation rather than to OMC:
`internal/agent` registers it, for the built-in Agent only. The model asks 1-4 questions in one
call, each with 0 or 2-6 labelled options (single or multiple choice), and the operator can always
type an answer instead. It is a `read` with `HumanInput: "answer"`: the executor stores it as a
pending operation that expires after 24 hours, the console draws it in place of the composer, and
the operator's answer is validated against the questions before the operation is claimed, so a
malformed answer leaves it open. The model receives each question with the chosen labels and typed
text; Skip is a `rejected` result. It is not a way to ask for approval (writes already ask) or for
secrets.

### Display tools

`render_ui` is how the Agent gives an answer a shape and shows data rather
than retelling it (ADR 0042, 0072, 0073, 0082).
`internal/agent` owns it, and it is offered only when the client declares that it can draw
it - the console does, the MCP bridge does not. It is not a capability: it changes nothing,
never passes through the executor and never interrupts a run. `render_chart`, `render_table`,
`render_view` and `render_canvas` are withdrawn; a call to any of them is refused, and a stored trace that names one is
still read as a display call.

The runtime augments successful capability results in **private model messages** with up to
eight `data_sources` entries (`internal/agent/data_sources.go`, ADR 0078). Each names a stable
conversation-scoped `data_ref`, tool-call `call_id`, path relative to result data, up to twelve
selectable scalar fields and row count. It contains no row values and does not change the
capability executor result, console DTO, streamed receipt or MCP contract. Object paths are
inspected deterministically with bounded depth and node count; oversized and malformed datasets
are not advertised.

A Canvas copies `source {data_ref}` from that metadata and places `fields` beside `source`.
The server resolves the reference against a retained successful capability trace, projects and
checks the fields, freezes the dataset and returns a receipt (`rendered`, `rows`, `fields`)
instead of the rows. Legacy `source {call_id, path}` remains accepted: `call_id` is a tool-call
trace id, not `operation_id`; paths start inside result data (`data.groups` for `usage_aggregate`,
`rows` for SQL). Conflicting reference forms, missing/foreign handles, display results and
unsuccessful capability results are refused. A view is bounded to 1000 rows and 96 KiB.

Refusals name the field, required type or valid reference form without quoting submitted HTML
or row values. Canvas failures receive available source metadata in private model feedback;
consecutive failures also receive a repair instruction. This is guidance, not a fixed call limit
or automatic execution of corrected arguments. A receipt acknowledges validation and storage;
it does not certify browser rendering. Custom diagrams may still use a Canvas without a dataset.

Completed settled turns - successful, failed or stopped - enter later requests as questions,
images, conclusions and bounded dataset/mutation metadata rather than their full tool transcript,
so a write that ran before a run ended is still visible to the next question. Up to eight references are kept,
prioritizing datasets used by a successful Canvas; up to eight recent mutation/refusal outcomes
are retained and omission is disclosed. Stored messages, traces and frozen views remain unchanged,
so retained references work across follow-ups and reloads. Missing non-display details are read
again through a dedicated capability; no raw-result retrieval surface is added.

Stored `render_view` panels remain readable through the console's block renderer (ADR 0079),
and stored `render_canvas` views remain readable through the sandbox renderer. New calls use
`render_ui` for model-authored HTML, SVG and script (at most 48 KiB per payload, with no count
limit on displays). The server stores the markup as written and never sanitises it; the console only
ever shows it in a `sandbox="allow-scripts"` frame whose document forbids every network load, and
the console's `frame-src` policy stops the frame from navigating away (ADR 0072). The `html`
parameter's description states the frame's geometry as a layout contract, because a model that is
not told the frame's size designs for a page: about 720px wide on a desktop and 320px on a phone,
height following the content (bounded at 16384px). The default inline component supplies no padding,
card ground or visible title; an explicit `frame: card` supplies those. Use natural document height,
not viewport units, a fixed page height or a vertically scrolling page wrapper; fluid widths that fall to one column below 560px, no card inside a card,
wide tables in a horizontally scrolling block, and anything with parts and connections drawn with
`OMC.diagram` rather than as a grid of boxes. The description also names the frame's component
classes, which the model composes from before writing CSS of its own (ADR 0085): `omc-stack`,
`omc-row`, `omc-grid`, `omc-card`, `omc-stat`, `omc-badge`, `omc-callout`, `omc-kv`, `omc-field`
and `omc-tabs` (wired from `data-omc-tabs`, `data-tab` and `data-panel`). The frame's base styles
keep media and preformatted text inside its width. While the call's arguments are still arriving
the console draws the markup written so far in a script-free preview of the same frame, so the
component fills in as it is written. Model scripts become active only after the completed component
replaces its preview. The draft policy grants a fresh nonce only to the host bootstrap, so inline
event handlers and JavaScript URLs cannot execute model code during streaming. Local controls
operate on frozen rows; requests for new data or writes go through `OMC.compose` and the ordinary capability and approval flow. Inside the frame
the console provides a kit (ADR 0073), so a chart or a table is a call rather than hand-drawn marks:

| Call | Draws |
| --- | --- |
| `OMC.rows` | The frozen rows of `source`, as objects keyed by field (`window.OMC_DATA` is the same array) |
| `OMC.fmt(value, unit)` | A number as the console writes it: `tokens`, `usd`, `ms`, `percent` (a 0-1 share), `bytes`, `time` (epoch ms), `number` |
| `OMC.chart(target, {type, x, y, series, stacked, unit, rows})` | `line`, `area`, `column`, `bar` or `pie`; `y` one field or several, `series` a field to split by. Drawn at the frame's width, so axis text keeps its size on a phone |
| `OMC.table(target, {columns: [{field, label, unit}], rows})` | A sortable table with formatted cells |
| `OMC.compose(message)` | Offers a follow-up in the composer for operator review; does not send, read live data or execute a write |
| `OMC.diagram(target, {nodes: [{id, label, note, detail, tone, icon}], edges: [{from, to, label}], direction})` | An architecture, flow or topology: nodes layered by distance from a source and joined by arrows, side by side in a wide frame and as an indented tree in a narrow one; picking a node shows its detail and connections |

`rows` defaults to `OMC.rows`. The kit follows the operator's token unit style, language and the
deployment's time zone, and colours from the theme variables.

A turn can be held to a presentation by a composer command, sent as `forwardedProps.present`:
`ui` (`/ui`) offers the turn `render_ui`; `text` (`/text`) offers no display. Both keep
`suggest_next` when declared. The wire value `canvas` remains accepted for compatibility and
has the same tool selection as `ui`.

Displays are selective artifacts of the answer, not progress reports, and the model draws one without being asked whenever the answer has a shape: the system prompt names the shapes - a trend or a comparison of three or more items is a chart (with a table when exact figures matter), several resources' status is stat cards, an architecture, flow or dependency is `OMC.diagram`, a what-if is adjustable controls - and leaves one fact, a short list or an error to text. It states the conclusion beside the figure and does not repeat the figure's numbers in a Markdown table. The model investigates and verifies before preparing one, uses `render_ui` when structure or interaction is clearer than sentences (filters, forms, calculators, status, workflow explorers, charts, tables or diagrams), uses the smallest complementary set, and leaves exploration in the trace. The console draws a frozen view where the model made the call, between the text written before it and the text after it (ADR 0082), so the model places a figure where the reader needs it and may draw more than one. `render_ui` takes an optional `frame`: `none` (the default for new calls) sets the UI straight on the conversation without border, ground or title bar; explicit `card` is a titled, bordered figure. The server persists the new inline default as `none`, while card remains stored as absence, so earlier conversations retain their original framing (ADR 0087). A call that failed stays a call row with its code and detail.

`suggest_next` rides the same declaration and is not a display call. The model calls it in the
response that carries its final answer, with one to three follow-up questions (80 characters each);
the runtime takes it out of the round's calls before anything else reads them, so it produces no
trace, no receipt and no extra model round, and the questions are stored on the turn as
`suggestions`. A malformed call, or one made beside other calls - before the answer exists - is
dropped without a refusal. The console offers the newest turn's suggestions under its answer.

What this means for a new capability: a result that holds its rows as an array of objects with
scalar fields - or, like `database_query`, as positional arrays beside a `columns` list - can be
charted and tabulated with no further work. A result shaped only for prose - rows packed into
strings, figures nested inside objects - cannot.

### Read-only database queries

`database_schema` lists what `database_query` may read, and `database_query` runs one SQLite
`SELECT` against OMC's own database (ADR 0036). Both are offered to the built-in Agent only
(`Adapters: ["agent"]`); MCP clients do not see them. The policy lives in
`internal/repository/readonly_query.go`, not in the capability:

- a separate read-only connection (`mode=ro`, `query_only`, no attached databases, bounded value
  sizes);
- exactly one `SELECT` or `WITH` statement;
- the compiled program is read before it runs: every table or index opened must belong to a table
  in `QUERY_READABLE_TABLES`, no column in that table's redacted list may be read, no index
  covering a redacted column may be opened, and writes to stored tables, virtual tables (so
  `json_each` and `pragma_*`) and databases other than `main` are refused; SQLite's scratch
  b-trees (`ORDER BY ... LIMIT`, `UNION`, `DISTINCT`, recursive CTEs) stay usable;
- at most 200 rows, 24 KiB, 500 characters per cell and 5 seconds; credential-shaped text, URL
  userinfo and email addresses are masked in cells and error messages. Masking is best-effort (a
  value transformed in SQL passes unmasked), so a secret-bearing column is redacted, not left to
  the mask.

A refusal or an SQL error is an `error` result whose `detail` names what to change. Every table
must be classified: readable in `QUERY_READABLE_TABLES` or hidden in `QUERY_HIDDEN_TABLES` with a
reason, and `TestEveryTableIsClassifiedForOperatorQueries` fails until a new table is. A migration
that adds a column able to carry a secret to a readable table adds it to that table's redacted list
in the same change.

The console draws a query as a normal capability-call row, without a raw-result preview. Agent
session reads, final snapshots and streamed query receipts omit raw query data; the session DTO
also omits the private model history and queued model calls. This applies to restored sessions as
well as new runs. Query status, diagnostics, SQL arguments and timing remain inspectable. The
full result stays in the server-side conversation for subsequent model rounds and explicit
`render_ui` calls. This is a browser-preview boundary, not an upstream-data
restriction: the selected model still receives query rows and may quote them in its answer or
choose them for a final display.

### Secrets and OAuth

- Secrets never enter tool arguments. A capability that needs one declares
  `HumanInput: "secret"`: the agent prepares the non-secret parameters, and the
  console's private form posts the value directly to the server while the operation is
  approved.
- OAuth flows use `HumanInput: "oauth"`. The capability server starts the flow, keeps
  the authorization URL, state, and device code on the server, and exposes them only to
  the signed-in browser. The model sees the operation id and its status.
- Tool results must not contain credentials, tokens, raw upstream error bodies, email
  addresses, or file paths. Reuse `safety` projections from `internal/operations`
  rather than inventing a new shape.

## What is not exposed

Capabilities are opt-in. The following are deliberately not reachable by an agent, and
a new capability must not add them:

- provider runtime-policy and advanced model edits through the console's whole-entry
  provider-edit facade, which also handles credential material. A dedicated
  secret-free shared operation is required before those edits become Agent tools;
- request log and error log file contents: they quote request bodies, so the Logs
  page's viewer stays a console-only read;
- raw credential reads, auth-file downloads, or key reveals; Vertex service-account
  import remains a console-only file handoff, not raw key material supplied through
  model-visible capability arguments or previews;
- arbitrary URL, CPA, or HTTP proxying; SQL beyond the read-only query surface above
  (no writes, no hidden table, no redacted column); filesystem; shell; or host maintenance;
- applying an update to Oh My CPA or CLIProxyAPI. Replacing a container is host
  maintenance: it stays a console action with its own confirmation (ADR 0099), and the
  system information capability only reports that a newer release exists;
- plugin installation, execution, or configuration, including the plugin system settings;
- raw configuration YAML writes - only the named scalar allowlist
  (`request_retry`, `max_retry_interval`, `max_retry_credentials`,
  `routing_strategy`, `force_model_prefix`), each written as a one-path change set on the
  v8 configuration API. Like any configuration save, each one keeps a backup of the
  stored file first, and the first one to a pre-v8 file makes CPA convert it (ADR 0051);
- configuration backup restore, deletion and retention: a restore is a whole-document
  write, and the other two discard the copies a restore depends on (ADR 0051);
- the credential fields that can transport secrets: `oauth_set_credential_fields`
  accepts `prefix`, `priority`, `weight`, `note`, `excluded_models`, `expired`,
  `disable_cooling`, `websockets`, `using_api`, `request_retry`,
  `request_scoped_errors` and `model_aliases`, while `headers` and `proxy_url`
  stay console-only because either can carry credential material into a preview;
- approval, secret submission, or OAuth completion for an operation an external agent
  prepared.
- which entries this console's rail shows: it is console state stored in the preferences
  document rather than a deployment capability, no capability reads or writes it, and the pages
  it hides stay reachable by URL.

A capability that cannot be implemented safely must be omitted here and recorded in
the pull request with the reason, rather than added with a weaker check.

## Adding an agent-capable feature

When a new OMC feature suits an agent:

1. Put the business operation in `internal/operations` (or extract the existing
   handler body into it) so the console and the registry share one implementation -
   including the write gate, revision checks, and readback verification.
2. Register the capability with its permission, risk, adapters, `Invalidates`, and
   confirmation preview. Do not add per-feature code to `internal/agent` or
   `internal/mcpbridge`.
3. Add tests for the declaration (schema and permission), the permission or confirmation
   path, and the operation itself, including a failure path.
4. Give the console's capability directory its copy: `agent.capability.<name>` (a short title)
   and `agent.capability.<name>.description` (what the call does, for the operator) in
   `web/src/i18n/index.tsx` and every catalog under `web/src/i18n/locales/`. The registry's
   `Description` stays English and is written for the model; the console shows it only in an
   expanded row. `scripts/capability-copy.test.mjs` fails when a registered capability has no copy
   or copy names a capability that is gone, reading the registry from the regenerated demonstration
   dataset.
5. If the capability is intentionally not exposed, say so in the pull request.

## External agents (MCP)

The registry is served to MCP clients in two ways, from one tool definition
(`mcpbridge.NewServer`), so the two cannot differ in tools, schemas or behaviour:

**Remote, by URL.** The console serves Streamable HTTP at `<console URL>/api/mcp`:

```bash
claude mcp add --transport http oh-my-cpa https://omc.example.com/omc/api/mcp \
  --header "Authorization: Bearer $OMCPA_CPA_MANAGEMENT_KEY"
```

The endpoint accepts the management key as a bearer token only - a console session
cookie is refused - and counts wrong keys against the login throttle. It is stateless
and answers in plain JSON, so it works behind any reverse proxy that forwards the base
path, and it is refused in demo mode. The Agent page's **Connect** action opens a drawer with the
deployment's own address with configuration for Claude Code, Codex and JSON-configured
clients.

A deployment that has set `OMCPA_MCP_READONLY_KEY` may present it the same way
(`--header "Authorization: Bearer $OMCPA_MCP_READONLY_KEY"`). It reaches a second server that
lists and runs only `read` capabilities, masks email-shaped account labels in its results, and
cannot sign in to the console, approve an operation, or run a write tool. See
[Read-only MCP key](#read-only-mcp-key).

**Local, over stdio.** For clients that only speak stdio, the binary bridges to the
capability HTTP endpoints:

```bash
OMCPA_SERVER_URL=https://omc.example.com/omc \
OMCPA_CPA_MANAGEMENT_KEY=<your CPA management key> \
oh-my-cpa mcp
```

The subcommand runs before configuration, database, and CPA client initialization, so
the bridge process opens no data directory and holds no CPA credential of its own.
Non-loopback URLs must be HTTPS, redirects are refused, and responses are bounded. A
refusal from OMC reaches the agent as OMC's own code (`authentication_required`,
`capability_unavailable`, ...); any other error body is replaced by `omc_request_refused`.

### What an MCP client sees

- One tool per capability declared with the `mcp` adapter, with its input schema, the
  result envelope as output schema, and annotations derived from the declaration:
  `readOnlyHint` for `read`, `destructiveHint` for `destructive`, `openWorldHint: false`.
  `ask_question` (an MCP client has its own way to ask its user) and the read-only
  database queries are offered to the built-in Agent only.
- Server instructions (`mcpbridge.SERVER_INSTRUCTIONS`) that explain the result statuses
  and the approval flow, and the OMC build as the server version.
- `omc_operation_status` (`operation_id`, optional `wait_seconds` up to 30): the state of
  a prepared operation. With `wait_seconds` it re-reads until the operator decides or
  the wait ends, so an agent does not poll in a loop. The name is reserved in the
  registry.

### Approving what an external agent prepared

A high-risk call returns `status: "pending"`, the `operation_id`, and a second text item
`Operator approval: <console URL>/authorize/<id>`. The link only names the operation.
It opens the authorization screen (`web/src/pages/agent/AuthorizePage.tsx`), a consent
screen outside the console's shell and behind its sign-in gate: who is asking, on which
deployment, the capability and its permission, the prepared change, and Deny and Allow,
with the same private
secret and OAuth handoffs, and posts the one allow-or-deny decision to the same
endpoint (ADR 0035). The decision endpoint requires a browser origin and refuses any
request carrying an `Authorization` header, so the key that prepared an operation can
never approve it.

Because the management key is administrator-equivalent, an external process holding it
can also log in to the console. Use MCP only with agents and hosts you would trust with
the console itself, serve the console over HTTPS before connecting one remotely, and
rotate the key if that trust changes.

`OMCPA_MCP_READONLY_KEY` covers the opposite case: an agent that should read fleet
analytics but not administer. It is a separate bearer credential for the same endpoint,
holds no session and approves nothing, lists and runs only `read` capabilities, and its
results mask the local part of email-shaped account labels. See
[Read-only MCP key](#read-only-mcp-key) (ADR 0103).

Tests: `internal/mcpbridge/bridge_test.go` (stdio round trip, transport refusals,
approval link, status wait, refusal codes, the complete tool schema),
`internal/api/agent_mcp_test.go` (the endpoint end to end: credentials, catalogue, read,
prepare, status), `internal/api/agent_readonly_test.go` (the read-only key's read-only
catalogue and write refusal), `internal/quota/passive_test.go` and
`internal/repository/quota_observations_test.go` (the parser and the observation store) and
the `agent-external` browser scenario (authorization screen and guide).

### Pricing provider membership

`pricing_list` includes `providers` for the models in its bounded current page. Each membership carries the configured provider id, family, snapshot name, channel, priority, OAuth marker, a hostname-only endpoint hint and exact model identities. This read uses the stored complete CPA catalog and never retrieves credential secrets. Provider-specific presentation does not create separate prices: `pricing_set` still edits the global model identity, with its existing revision and confirmation policy.

`pricing_list` also returns `candidates`, keyed by model, for the custom and linked prices on its page that OpenRouter now offers a price for and the operator has not answered: `kind` `automatic` (auto mode would match it) or `suggested` (a resemblance), with the OpenRouter model and its rates; a failed lookup leaves it empty rather than failing the list. An agent acts on one through `pricing_set` (mode `auto`, or `linked` to the candidate's id), which records the answer like a console save; ignoring a candidate is left to the console, because it only silences a prompt and changes no price.

### Pricing an agent can carry out end to end

Five reads surround the pricing writes so an agent can work a price out, check it and
only then ask for approval. All are low-risk reads on both adapters, bounded to 20 rows
a page, and none returns a secret.

- `pricing_overview` (`offset`): `sync` (`is_known`, `is_running`, `has_failed`, last
  success, next run, interval, `upstream_models`), `coverage` counts by mode, and the
  `unpriced` models busiest first with 30-day traffic and up to two OpenRouter
  suggestions. The stored sync failure text is withheld because it can quote an
  upstream response; `has_failed` says that it exists.
- `pricing_get` (`model`): `price` (absent when unpriced), `automatic` (what auto mode
  would match), `suggestions`, `candidate`, `channels` serving the model, `usage_30d`
  and `profile_7d` (median request size and the largest prompt). Advisory fields are
  left out when their lookup fails.
- `pricing_catalog_search` (`query`, `offset`): the stored OpenRouter snapshot matched
  on id, name and canonical slug, exact ids first, with rates and tiers.
- `pricing_quote`: one hypothetical request (`input_tokens`, optional output and cache
  tokens, `timestamp_ms`, `channel`, `service_tier`) priced against `model`'s stored price or a
  `proposed` object shaped like `pricing_set`'s input. It returns the resolved `price`,
  the `breakdown` with the governing `tier_index`, and `cost_usd`. Nothing is written.
- `requests_cost_breakdown` (`id`): a recorded request's pricing status, stored nanos,
  locked price and channel versions, tier, the billed `service_tier` with its
  `service_tier_source` (`response` or `request`) and per-bucket rates; `resource_missing` for
  an unknown id.

A time-of-day tier's `utc_start`/`utc_end` are HHMM on the clock named by `time_zone`,
UTC when omitted (ADR 0063). An agent given a vendor's price page enters the published
hours with the vendor's zone; converting them to UTC is wrong for any zone with
daylight saving and is never needed. `pricing_set` resolves a link or an automatic
match while preparing, so a model outside the CPA catalog, an unknown OpenRouter id
or an automatic switch with no match arrives as `invalid_parameters` with a detail
rather than as a failed approval. Tests in
`internal/operations/pricing_test.go` cover both adapters, the refusals, the dry run
and the breakdown's schema.

### Usage and request filters

`usage_aggregate`, `usage_compare` and `requests_list` share one input, so a question
narrowed for a list is narrowed the same way for a total. Beyond the exact dimensions
(`providers`, `models`, `call_points`, `client_keys`, `credentials`, `status`) it takes
`search` (a literal substring, at most 256 characters), `min_latency_ms`, and a pattern:
`regex_field` (one of `model`, `model_alias`, `response_model`, `provider`, `endpoint`,
`ua`, `request_id`) with `regex`, an RE2 pattern of at most 256 characters. A pattern
without a known field, or one RE2 refuses, fails with `invalid_regex: <reason>` so the
model can correct the call; other malformed input stays `invalid_parameters`. All three
are low-risk reads with no secret or OAuth handoff.

A request item carries `request_id`, `ttft_ms`, `user_agent` (the product label already
redacted at ingestion) and, only when the upstream served a different model,
`served_model`, alongside provider, model, call point, tokens, latency and cost. Raw
logs, client addresses, key masks and credential names stay out. The field descriptions
are kept short on purpose: the input is repeated in three tools and the catalogue has a
size budget (`TestAgentCatalogueFitsTheSchemaBudget`). Tests cover the filter mapping and
each refusal (`internal/operations/validation_test.go`), the matching itself
(`internal/repository/usage_filter_test.go`) and the HTTP parameter
(`internal/api/usage_events_test.go`).

### Timezone capabilities

`timezone_get` is a low-risk read returning the optional manual override, deployment timezone and effective IANA timezone. `timezone_set` is a low-risk write accepting `{ "timezone": "Asia/Kuala_Lumpur" }`; an empty string restores the deployment timezone. Both are available to Agent and MCP administrators under the existing capability policy. The write shares the preference repository's validation and commit-before-publication rule, returns `invalid_timezone` for an invalid name, and invalidates `preferences` and `timezone` readers. No secret or OAuth handoff is involved. Tests cover validated writes, reads through both adapters and refusal without changing the runtime calendar.

### TPS calculation capabilities

`tps_calculation_get` is a low-risk read returning `{ "mode": "exclude_ttft" }` or
`{ "mode": "include_ttft" }`. Missing or unsupported stored preferences resolve to
`exclude_ttft`. `tps_calculation_set` is a low-risk write accepting the same mode
shape, rejects unsupported or absent mode values with `invalid_parameters`, and
returns the saved mode. Both are available to Agent and MCP administrators under
the existing policy. The write shares the console's `omc_tps_calculation_mode`
preference and invalidates `preferences`; no secret or OAuth handoff is involved.
It changes request-record and Playground TPS readouts, not original timings or usage.
Exclusion subtracts measurable first-token latency and otherwise uses total latency,
as it also does for records that report reasoning tokens (hidden reasoning precedes
the first token, so the residual window never contained it);
inclusion always uses total latency. Tests cover both adapters, shared preference
reads/writes, defaults, invalid values and administrator refusal.

### Automatic quota refresh capabilities

`quota_auto_refresh_get` is a low-risk read returning
`{ "interval_minutes": 360, "refresh_on_reset": true }` by default; an absent or
unreadable stored field resolves to its default. `quota_auto_refresh_set` is a low-risk write that
requires both fields, accepts `interval_minutes` of 0 (schedule off) or 5-1440, returns
`invalid_parameters` otherwise and returns the saved policy. Both are available to Agent
and MCP administrators. The write shares the console's `omc_quota_auto_refresh`
preference, which the server's background loop reads every minute (ADR 0094), and
invalidates `preferences`; no secret or OAuth handoff is involved. Tests cover both
adapters, defaults, invalid values and administrator refusal.

### Browser connection recovery

Managed Agent execution belongs to the server, not a socket. Console refresh or network recovery
replays its projected journal and does not invoke capabilities again. Stop calls the authenticated
run cancellation endpoint; a disconnected subscriber does not signal operator intent. Pending
approvals, secrets and OAuth continue through their existing server-side handoffs and are never
automatically decided during recovery. Process restart does not resume uncertain external writes.
See [ADR 0044](adr/0044-browser-connections-subscribe-to-server-owned-runs.md).

### Custom icon library

`custom_icons_list` is a low-risk read for Agent/MCP, accepting an optional `offset` and returning at most twenty icon metadata records plus `has_more`. It includes names, stable IDs, revisions and stored reference counts, never image bytes. `custom_icon_create` accepts `{name,data}` and `custom_icon_update` accepts `{id,name?,data?}` as low-risk presentation writes. Both reuse the browser import validation and return metadata only. The existing 32 KiB capability input cap still applies; larger artwork must be imported through the browser picker rather than widening the tool envelope.

`custom_icon_delete` accepts `{id}` and is destructive/high-risk on both adapters. Its preview contains the icon ID/name, artwork revision, reference count and `provider_icons: restore_defaults` impact; approval rechecks the artwork revision, removes all current matching overrides and deletes the asset in one transaction. Every successful write invalidates `custom-icons`; deletion additionally invalidates `preferences`. Stable refusals are `custom_icon_invalid_image`, `custom_icon_too_large`, `custom_icon_invalid_name`, `custom_icon_limit`, `custom_icon_not_found`; stale deletion approval returns `resource_conflict`. No artwork is returned in capability output, preview or audit details. Tests exercise both adapters, administrator permissions, refusal, reference-reset confirmation and confirmed deletion.


## Quota capacity output

### Quota prediction

`quota_prediction` is a low-risk `read` on Agent and MCP. Its optional filters are
`provider`, `window_kind`, `auth_index` and `limit`; the limit defaults to and may not
exceed 100. A read-only MCP principal must omit `auth_index`, receives no auth index in
the result and sees `credential_label` through `accountLabelForCaller`. Agent and console
callers retain both fields. The result is also bounded to 32 KiB and reports every removed
tail row through `omitted`. The internal Feishu findings projection filters verdicts before
these public result bounds, prioritises `will_exhaust`, and retains at most 178 findings to
fit the card envelope while also applying the 32 KiB result bound. Its `omitted` count is
disclosed in the prediction card footer, so a finding cannot disappear silently behind
either a non-finding row, the byte bound or renderer truncation.

Each item includes the source window identity (`window_id`, `window_scope` and optional
`window_model`), stable within its credential and independent of forecast values. These
additive fields distinguish model/group windows without changing the tool name or filters.
Each item states the current used percentage, reset and observation times, data age,
burn rate, projected used percentage at reset, optional projected tokens and cost,
optional exhaustion ETA, verdict, evidence tier, reason codes and sample quality.
Verdicts are `will_exhaust`, `under_used`, `on_track` and `unknown`; evidence is `fact`,
`projection` or `unknown`. Boundary, stale/error reading, incomplete history, mid-cycle
reset, `fewer_than_three_buckets`, `insufficient_coverage`, `observation_gap`, early-cycle
non-positive trend, invalid percentages, unsafe arithmetic and unavailable usage are
explicit reasons. `sample_count` counts the deduplicated five-minute buckets that
participate in the fit, including the current reading; a zero means the sample was not
evaluated because a fact or an earlier validation return settled the result, not that no
history exists. `coverage_seconds` and `max_gap_seconds` stay null below two points. A
fresh, valid 100 percent reading returns
`will_exhaust`/`fact` even when history is incomplete or a reset was detected; stale or
error status, a missing or invalid percentage, and an unknown or expired boundary still
prevent that fact. `fewer_than_two_observations` remains a legacy reason code but the
current path emits the bucket-specific reasons. A missing number is never serialized as
a fabricated zero; model/group usage that cannot be assigned safely reports
`scope_unknown`. Freshness is window-aware: at least the two-hour idle
refresh cadence, one quarter of the window when longer, and no more than twenty-four hours.

The dashboard reads the same computation through
`GET /api/v1/management/dashboard/predictions`. This console projection retains the full
stored roster and operator-visible credential labels and auth indexes, groups items by
provider, and adds prediction-call duration `duration_ms` plus nullable cycle accounting
(`window_start_ms`, `remaining_percent`, `uniform_projected_percent_at_reset`,
`remaining_window_hours`, `window_error_hours`, `remaining_tokens`,
`capacity_error_percent` and optional `capacity_unavailable`). These
console fields are excluded from capability JSON and schemas. Dashboard-only unknown
placeholders do not enter quota_prediction or digest results and cannot displace their
actual windows. Providers without refresh support or stored window evidence are omitted.
Matching passive observations can establish missing standard windows without a snapshot;
ordinary and read-only callers use the same restored evidence before masking. Safe stored Ollama
account snapshots join the same roster without reading cookies. Step and Stepfun share
one dashboard group while retaining their stored provider identities in items. The existing
`quota_prediction` tool keeps its name, filters and prediction semantics, with the additive
window identity fields described above. The five-minute foreground refresh and the manual dashboard refresh
therefore recompute only from stored evidence and never probe CPA.

The tool reads current-cycle history as fixed five-minute buckets filtered in SQL by the
complete window identity (`auth_index`, `provider`, `window_kind`, `scope` and `model`).
Each bucket contributes its newest real reading; empty buckets are not filled. The 5,000
cap counts buckets rather than raw rows, so a seven-day window (about 2,016 buckets) is not
treated as incomplete because it stored many readings. Overflow still marks
`incomplete_history`. Reset detection compares ordered readings inside a bucket, adjacent
bucket minima and last readings, and the current snapshot against the bucket it replaces;
a drop or moved reset makes the window unknown rather than fitting across it. The pure
calculation layer also keeps its cross-point reset check. A projection requires at least
three buckets, coverage of at least `max(30 minutes, 5% of the cycle)`, and no adjacent gap
larger than `max(30 minutes, coverage / 2)`.

The tool assembles its roster and current readings only from persisted safe credential
metadata and quota snapshots, then reads passive and active observations without refreshing
quota or calling the CPA management gateway. Standard windows
join `QueryCredentialWindowUsage`; model and group windows join
`QueryCredentialModelWindowUsage` through the existing scope resolver. Reset-time token
and cost projections require at least five percent used, recorded traffic and a priced
request share of at least 95 percent. Otherwise both fields are omitted with a reason such
as `used_percent_below_five`, `no_traffic`, `no_price` or
`insufficient_priced_usage`. This is not the capacity API and returns no capacity field.
See ADR 0098 for the capability boundary and ADR 0113 for bucketed evidence.

### Reset advice

`reset_advice` is a low-risk `read` shared by Agent and MCP. Optional inputs are
`provider`, `window_kind`, `auth_index` and `limit` (default and maximum 100).
Read-only MCP callers must omit `auth_index`; their results omit that field and mask
email-shaped credential labels with the existing account-label rule. The result contains
`generated_at_ms`, `items` and `omitted`, with a 32 KiB ceiling and deterministic ordering.
Each credential has one row including `credential_label`, `provider`, `window_id`, `window_kind`,
`remaining_resets`, `earliest_expiry_ms`, `used_percent`, `reset_observed_at_ms`,
`quota_observed_at_ms`, `cooldown_active`, `announcements`, `verdict`, `evidence` and
bounded reason codes. Missing numeric readings are null. `remaining_resets` counts
each applicable entitlement once, including grants that clear several windows.
The window fields and `used_percent` identify the highest valid standard reading
(window ID breaks ties); without a selected reading, the ID is empty and kind is `unknown`.
`cooldown_active` is true/false from an existing correlated local cooldown record,
or null when none is available. `announcements` contains active matching evidence with
ID, source, scope, optional model, confidence, expected reset and registration timestamps,
influences_advice and has_conflict. It omits credential targets and original free text.
local_evidence retains source=local_observation, the original verdict/evidence/reasons and
quota_reset_at_ms. The existing top-level counts and observation times remain local readings.
Timed confirmed/probable notices within the configured horizon override a known local
verdict to use_before_expiry when positive eligible entitlements and an enabled, available
credential are observed, including a local keep_for_emergency verdict. Unknown/zero capacity
keeps its verdict and gains notice_without_local_capacity. Unverified, untimed or distant
notices remain evidence only. A differing reset time or overridden conclusion carries
has_conflict; neither source is erased or treated as an observed reset. Exact provider and
credential scope matches are required; model scope requires an exact persisted model window.
See ADR 0111.

The four verdicts are `use_before_expiry`, `keep_for_emergency`, `no_action` and
`unknown`. Fresh remaining entitlements expiring within the configured horizon
(default 72 hours) yield `use_before_expiry` below 90% used; a reading at or above
90% in any standard window takes precedence and yields `keep_for_emergency`.
Without a known emergency, every standard window must be readable and below the
threshold before recommending spending; missing/stale quota evidence then yields
`unknown`. A known zero remaining count
or already expired grants yields `no_action`; a positive count outside the horizon
also yields `no_action`. Missing grants, count/detail disagreement, missing expiry,
unknown grant scope, absent quota boundaries and missing or stale readings yield
`unknown` with a reason. Claude grants apply only to their explicit `clears` windows;
Codex credits apply to standard windows. A stored credential without a snapshot is
still returned as unknown. A requested kind selects credentials with that window
kind, but every standard window still participates in the credential's decision.
Credentials without any windows remain visible as unknown for a requested kind.

`fact` identifies a known zero remaining balance; `projection` identifies advice from
current readings, including a positive balance outside the expiry horizon; `unknown`
identifies insufficient evidence. Entitlement snapshots must be at most two hours old.
Quota freshness follows the existing window-aware two-to-twenty-four-hour bound; newer
passive observations update only quota, never the entitlement timestamp. All inputs
come from local metadata, snapshots and observations. The capability never contacts CPA,
refreshes upstream quota or claims a reset. This advice does not assert that a provider
will allow redemption at the current instant. Announcement evidence is a future
extension described in ADR 0109, not a data source in this capability version.

### Model advice

`model_advice` is a low-risk `read` on Agent and MCP, including the read-only MCP
key. Optional `models` names at most twenty client-requested call points; without
it, the tool ranks all names in the configured directory and returns the first
fifty, with `omitted` counting the remainder. `recommended` is the leading
`ok` or `caution` model, or null when neither exists even if `avoid` models have
ranks. Each item reports `ok`, `caution`, `avoid`, or `unknown`,
its continuous rank (null for unknown), bounded reason codes, the tightest
relevant window's label/kind/used share/reset (with a missing kind inferred from
the window ID first, then its period hours), eligible and usable credential
counts, and the selected reading's age. The result also carries
`generated_at_ms` and fits the capability's 32 KiB result budget.

The route projection used by `models_list` supplies provider and upstream
identity. Exact model-scope and reviewed family-scope windows are applied only
to their target; standard windows apply to the credential. The existing quota
recommendation computes healthy, warning and exhaustion on those relevant
windows, with a parked or cooling credential blocked. The best measured,
unblocked credential decides the model verdict. Stale, unpublished and idle
readings are not zeros. A newer classified collection failure makes the retained
successful snapshot unmeasured. Missing credential model membership is counted as
unmeasured, not silently dropped. Missing or ambiguous route provenance and absent
credential membership remain unknown, rather than being assigned a provider.
The tool reads the CPA directory and credential list but never probes an
upstream quota endpoint. It returns no credential identity, account label,
address, key or path, regardless of caller authority. See ADR 0097.

`quota_list` and `quota_refresh` expose the same safe window results as the console.
Their `plan` projection contains only `plan_type`, `plan_label`, `tier` and optional
`subscription_active`; false denotes an explicit inactive subscription, while an
absent field means unknown. The projection includes live xAI/Antigravity tiers and
Meta plan readings, not credential metadata, raw responses or minted keys. Meta
refresh uses its key-exchange endpoint and can mint a key upstream; the refresh
tool description states this side effect, and returned keys are discarded (ADR 0065).
No new capability or permission is needed: listing remains a read and refreshing
remains a low-risk write. Their tool descriptions identify these as estimates rather
than balances or routing instructions.

A standard five-hour or weekly window whose request headers published a reading newer than the
stored snapshot is presented from that reading, marked with `observed_at_ms` and
`source='passive'`; the `capacity` estimate still comes from the snapshot. An agent must
describe such a window's `used_percent` as a header reading at that instant rather than as a
refreshed balance.

A credential's `status` is `unpublished` when the provider answered without
publishing a window. That is a reading, not a failure and not an unread credential:
it says the read happened and the provider reported nothing to meter, so an agent
must neither retry it on the assumption that nothing was asked nor report it as
`idle` (ADR 0071).

Each supported window can carry `usage` (current cycle's half-open range, requests,
priced requests, tokens and locked cost), `capacity` (tokens, optional locked-cost
estimate and rounding allowance), and `capacity_unavailable` (why the current cycle
cannot be estimated). A capacity's `basis` is `current_cycle` or `previous_cycle`, and
`observed_at_ms`, `from_ms` and `reset_at_ms` identify the reading it derives from.
A previous-cycle reference must be described as historical, never as the current
remaining balance; its current unavailable reason and current recorded usage remain
in the result. Neither value is an actual provider-denominated quota or a spend forecast.

`model_families` identifies reviewed scope metadata. `has_mid_cycle_reset` is durable
reset evidence; `has_incomplete_history` preserves a failed evidence read for the cycle.
Neither flag is an estimate. Unknown scope/alias resolution, unavailable
history and unavailable usage return `scope_unknown`, `history_unavailable` and
`usage_unavailable`; these never mean zero. Only a fresh valid current cycle with
`low_usage`, `no_traffic` or `no_reading` may carry a previous-cycle reference. Missing
history, changed scopes/periods and early-reset cycles do not produce one. Costs use
request-time prices and require 95% priced-request coverage; error percentages describe
rounding only and must not be presented as statistical confidence. See ADR 0059.

### Passive quota observations

`quota_observations_list` is a low-risk read exposed to the Agent and MCP. It returns the
quota windows an upstream published on request response headers (Codex `x-codex-*`, Anthropic
`anthropic-ratelimit-unified-*`) and the windows an active refresh this deployment performed,
oldest first, each row one reading at one instant rather than a current balance. It accepts
optional `auth_index`, `provider`, `window_kind`, `source` (`passive` or `active`), an inclusive
`from_ms`/`to_ms` window (default the last seven days) and a `limit` (default 200, maximum 1000).
A limit above 1000 is `invalid_parameters`; a matching history larger than the limit is refused
with `tool_result_too_large` and a detail telling the caller to narrow the window, never
truncated. Each row names its `source`, its `scope` (`standard`, `model` or `group`) and its
`model` (empty for a standard window). `event_key` is not projected.

The rows come from `quota_observations` (ADR 0103, ADR 0104), an append-only history. A passive
row is written in the same transaction as the request record: a window is recorded only when its
percentage or reset changed, or every fifteen minutes while steady, under `source='passive'` and
`scope='standard'`. An active row is the result of a completed refresh, recorded for each window
that states a period and a used share under `source='active'`; the source, scope and model
distinguish the two. The table rolls at a fixed 365-day horizon independent of request-record
retention. A malformed or missing upstream reading produces no row: an absent window is unknown,
never zero.

### Quota refresh scheduling and backoff

The deployment reads each refreshable credential's quota on its own cadence (ADR 0104); an agent
does not need to drive it. A refresh that reaches an upstream which answered 429 is refused with
`rate_limited_backoff` and a detail naming the retry time, and the credential and its provider are
parked until then; the manual endpoint and the `quota_refresh` tool share that state, so a
rate-limited credential is not probed again from any path until its park expires. An agent should
report the wait rather than retry immediately.

The browser's multi-credential refresh uses the same backoff and per-credential lock but returns
one allowlisted result per credential. Different credentials of the same provider keep the normal
four-way concurrency; a rate-limited target carries its retry time beside successful siblings and
does not make the batch HTTP request fail. The Agent/MCP `quota_refresh` capability remains a
single-credential call and keeps its typed `rate_limited_backoff` refusal.

### Read-only MCP key

A deployment may set `OMCPA_MCP_READONLY_KEY` as a second bearer credential for the remote MCP
endpoint. Holding it reaches only capabilities declared `read`; write and destructive tools are
not listed and are refused with `capability_forbidden`, and the holder can neither sign in to
the console nor approve an operation. Its principal is distinct from the operator's, so it
cannot read an operation the management key prepared. For such callers the tool output masks the
local part of an email-shaped account label in credential and provider display names (for
example `vinsprite78@gmail.com` becomes `vin***@gmail.com`); `auth_index` is unchanged. The
console and the management key keep seeing the real labels. The key must be at least sixteen
characters and differ from the CPA management key, or start-up fails.

### Available model directory

`models_list` is a low-risk read exposed to the Agent and MCP. It takes no parameters,
requires the existing read permission and performs no inference or mutation. Its safe
`models`, `providers`, `routes`, `partial`, `model_info` and `metadata_updated_at` result
is shared with `GET /management/model-square`. CPA's live `/v1/models` is authoritative;
the first nonempty configured client key is used server-side and never disclosed.
Without one, the operation returns `client_key_required`.

Exact call points may retain several upstream targets. Empty `upstream_model` or
`provider_id` denotes unresolved provenance; unreadable enrichment does not suppress
advertised models. Bundled models.dev facts are matched only by exact canonical identity
or an unambiguous source alias. They do not establish operational availability or an
open-source license. Names/icons use current deployment overlays; endpoints are reduced
to hostnames. Secrets and account fields are excluded. Results exceeding 2,000 routes
are refused with `tool_result_too_large`.

### OAuth credential refresh

`oauth_refresh_credential` is a high-risk write accepting `name` and `auth_index`. It
asks CPA to renew that credential's tokens now instead of at its scheduled refresh and
returns only whether it succeeded: CPA's own answer carries the renewed tokens, so it
is discarded inside the management client and never reaches a preview, a result or the
audit log. It invalidates `management-auth-files` and `management-quota`.

### OAuth model exclusions

`oauth_excluded_models_list` reads the provider-wide, normalized exclusion map and
its revision. `oauth_set_excluded_models` is a high-risk write accepting `provider`
and `models`. Its preview names the provider and shows before/after rules; execution
requires the operator's confirmation and rechecks the map revision under the shared
whole-config write gate. It reuses the console facade's rule validation and verified
CPA readback, then invalidates the exclusion map, credential model lists, Model Square and pricing.
An empty list removes only that provider's defaults, leaving non-empty credential
`excluded_models` overrides unchanged. A non-empty credential list replaces, rather
than extends, the provider defaults; a missing, empty or null list inherits the
current provider rules. This precedence requires CPA v8.0.33 or later; upgrade
older gateways before relying on credential overrides. The wildcard `*` hides every model only on inheriting
credentials, so it must remain visible in the preview without implying a universal
block. For example, provider `["*"]` with credential `["model-b"]` does not block
`model-a` on that credential through the provider rule. Clearing the credential
list restores inheritance of `*`, not unrestricted availability. CPA remains
authoritative for availability; use its per-credential model list to check it.
These capabilities are available through the built-in Agent and stdio MCP registry,
without a new adapter or any secret-bearing response.

### Account auto-management status

`account_automation_status` is a low-risk read exposed to the Agent and MCP. It returns
the resolved master switch (whether the environment variable or the console's web
switch is in effect), the current gateway credential inventory (whether it could be read,
and the total, managed, unmanaged, gateway-disabled and pending-absence counts - all counts
omitted when the read failed, so zero only means a complete empty gateway), and, for each
adopted credential, its plan: the state, why it has
no automatic control, the burn_credit exception, removed=false for active rows, whether OMC disabled it, an active recovery instant with
`recovery_status=scheduled|overdue`, and whether a standard weekly reading exists with
its used share. `scheduled` means the still-disabled credential has a future recovery;
`overdue` means that instant passed and retry work remains. Completed or abandoned plans
omit both recovery fields, including historical rows written before the consumed instant
was cleared. It returns no credential material, and it is read-only by construction: the
console's `/api/v1/account-automation` write routes are session-and-same-origin authenticated and
the read-only MCP key cannot reach them, so the agent cannot adopt, unadopt, set Credit, switch, cancel or
set a recovery time. Controllable credentials may be adopted even before their provider
capability is verified; the read then reports `state=monitoring` with
`monitor_reason=unverified_account_weekly_capability`, and no automatic disable or
recovery action is permitted. Paused plans retain their recovery projection, with is_enabled=false preventing execution. Tombstones are excluded from this summary. Missing gateway credentials and missing CPA names have separate reasons, credential_not_found and credential_missing_name; historical no_controllable_credential values remain readable. The summary is read-only: it never confirms an absence, removes a plan or writes the gateway. See ADR 0105, ADR 0108 and ADR 0116.

### Ollama and Feishu status

`ollama_accounts_status` and `feishu_notifications_status` are low-risk reads exposed
to the Agent and MCP. The first reports the Ollama collector switch and, per stored
account, its label, collection status, newest safe quota reading and retry state; the
session cookie is never returned. The second reports the Feishu module's enabled,
ready, degraded, forced-off and connected state, where its switch comes from, open
problems, pending and queued sends, and its last error; the app secret is never
returned. Neither has a write path: the console's `/api/v1/ollama` and
`/api/v1/feishu` routes are session-and-same-origin authenticated and the read-only
MCP key cannot reach them. See `docs/ollama-monitoring.md` and
`docs/feishu-notifications.md`.

### Plugin-page native boundary

The browser's trusted Plugin Host (ADR 0067) is not an Agent/MCP capability. It exposes native
secret-bearing configuration only through an authenticated console session; models and
external agents continue to use the declared sanitized capability registry and its
confirmation/revision rules. No arbitrary native path or URL becomes a tool argument.

## Credential warmup capabilities

`quota_warmup_settings_get` and `quota_warmup_credential_get` are bounded read projections
of provider defaults, effective policy, model, next check, activation observations and pause reason.
The optional `state.protocol_pause` carries bounded model/reason evidence independently of the
latest receipt; a rate-limited recheck does not restore automatic compatibility.
`quota_warmup_settings_set`, `quota_warmup_credential_set` and `quota_warmup_run` are
high-risk writes exposed to both Agent and MCP. Their prepare result binds confirmation
to the provider-policy or credential revision; provider-default approval states its
impact on current and future inheriting credentials. Saving an enabled policy authorizes
its first automatic consuming attempt through the same billing and window checks, without
requiring prior manual activation. Manual approval authorizes one optional tiny consuming
request and post-request activation observation, not token access or reset-credit redemption.
The shared implementation enforces unknown/paid billing protection and
uncertainty-aware idempotency; uncertain results must never be automatically replayed.
