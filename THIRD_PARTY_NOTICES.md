# Third-party notices

This file records the third-party components and reference material the first OMC
batch (account auto-management, Ollama monitoring, Feishu notifications) brings in,
with the obligation each actually carries. It does not change the project's own
`LICENSE` (MIT).

The listing is written from the licenses shipped in the resolved modules, not from
a package's own summary: where a summary disagreed with the shipped license text,
the shipped text is what is recorded.

## Linked Go modules

### `github.com/larksuite/oapi-sdk-go/v3 v3.12.0`

- License: **MIT** (`Copyright (c) 2020 Lark Technologies Pte. Ltd.`), as shipped in
  the module's `LICENSE`.
- Use: the Feishu bot's long connection and message/card sender in `internal/feishu`.
- Obligation: the MIT license text and copyright notice must be retained when the
  module is redistributed. This project links it into a binary and does not vendor
  its source; the notice is retained here.

### `github.com/gogo/protobuf v1.3.2` (indirect)

- License: **BSD-3-Clause** (`Copyright (c) 2013, The GoGo Authors`).
- Use: pulled in transitively by the Feishu SDK.
- Obligation: retain the copyright notice and the three-clause disclaimer.

### `github.com/gorilla/websocket v1.5.0` (indirect)

- License: **BSD-2-Clause** (`Copyright (c) 2013 The Gorilla WebSocket Authors`).
- Use: pulled in transitively by the Feishu SDK for the websocket protocol.
- Obligation: retain the copyright notice and the two-clause disclaimer.

Every other Go module this project builds on keeps its own license, as recorded by
`go.sum` and each module's `LICENSE`; the three above are the ones this batch adds.

## Reference material, no source redistributed

### Ollama quota parsing

The Ollama collector in `internal/ollama` is a fresh implementation: it requests the
fixed settings page and parses numeric windows with its own code. Its **approach** is
informed by the retired CPA-buddy (cpab) Ollama collector, which credits
`jacklee-code/ollama-cloud-quota-monitor` and `Wei-Shaw/sub2api`. That lineage is
reported as **LGPL-3.0-only**.

No source text from those projects is copied into this repository, and none of them
is vendored or distributed here. Whether, or how, an LGPL obligation attaches to a
distribution of this project is **not settled by this notice** and must be reviewed
before the feature is merged for distribution. A rewrite that reuses only the
approach is not, by itself, asserted here to waive any obligation.

### Feishu card and notification structure

The Feishu card and denoise structure in `internal/feishu` is likewise a fresh
implementation informed by the same cpab lineage, whose notify tree is reported as
**LGPL-3.0-only**. The same caveat applies: no source text was copied, nothing is
vendored, and the obligation question is recorded for review rather than resolved.

## Review status

These obligations were reviewed against the modules actually resolved in this
worktree. They have **not** been reviewed by legal counsel, and the project has not
been published with these components.
