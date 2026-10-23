# ADR 0115: A dedicated deployment branch carries a verified prebuilt SPA

- Status: Accepted
- Date: 2026-10-23

## Context

The fork's Dokploy host can compile Go but should not pay the frontend build's
memory cost. A Mac can prepare that frontend without introducing an image registry.
The generated entry document, lazy chunks, workers, fonts and provider artwork must
travel together; a partial or stale distribution cannot silently become an image.
ADR 0033 remains the development-branch rule: generated assets are not committed
there, and a Go-only development checkout can serve the existing placeholder.

## Decision

Only `deploy/prebuilt` carries generated assets. `scripts/prebuilt-web.mjs prepare`
requires a full source commit SHA and a new destination, clones into that isolated
directory, installs the locked dependencies, builds and syncs the complete SPA,
then writes `deploy/prebuilt-manifest.json`. It never switches, resets or stages
the caller's checkout. Failed preparation leaves its directory available for diagnosis.

The versioned manifest records the source SHA, previous deployment tip, optional
fork tag, SHA-256 per source file and embedded asset, and sorted aggregate hashes
over the source inventory, frontend inputs and outputs. Hash records use UTF-8
`path + NUL + lowercase SHA-256 + newline`, sorted by path. No clock, absolute
directory or generated commit ID enters those hashes. The generated deployment
commit is distinct from its source; the binary embeds the source revision and
`fork` comparison version, and leaves the source branch unknown for the detached
input. These hashes detect inconsistency, not authenticity against a malicious
committer who controls both the source and manifest. Repository review remains
the trust boundary.

Publication is a separate explicit command. It checks both origin URLs against
the canonical `LambdaTheory/oh-my-cpa` fork, reconstructs the allowed source
inventory from Git blobs and uses a private index to publish only approved source
paths, the manifest and verified distribution. Arbitrary untracked files, local
state, dependencies, environment files and credentials are excluded. The first
deployment has the source as parent; subsequent deployments have the previous
deployment as first parent and the new source as second parent. The next source
must descend from the previous source. The prior tip must have the generated
shape and unchanged source bytes; unknown changes require operator investigation.
The remote tip must still match preparation, and an ordinary fast-forward push
rejects a concurrent advance. There is no force push, branch deletion or automatic
retry. The caller's index and HEAD stay unchanged.

`Dockerfile.prebuilt` is an independent build graph containing Go and Alpine only.
A standard-library Go verifier checks source freshness, complete asset inventory,
regular files, local public artwork and relative static resource references before
Go compilation. Its Docker-specific context allowlist admits the embedded dist
but excludes native dependencies and Git state. The existing target-platform Go
compilation, unprivileged runtime, volume and health probe are retained.
`deploy/dokploy/compose.prebuilt.yml` inherits runtime configuration from the source
Compose service and selects that Dockerfile. The default Dockerfile and source
Compose path retain their ordinary build behavior.

## Consequences

Deployments require deliberate preparation and publication for every source update.
Git stores the complete generated SPA on the deployment branch, increasing fork
storage; development branches keep reviewable source diffs. Mac preparation needs
Node, pnpm, Go and build-time dependency downloads; the resulting browser assets
have no CDN dependency, and Dokploy requires no frontend toolchain. Go modules and
base images still need build-time availability. Static reference checks cover
Rollup module edges and literal CSS/HTML references and public resources; built-browser acceptance owns
dynamic runtime loading and `/omc` behavior.

Script tests use isolated repositories and a bare local remote for publication
races and history checks. Go tests own manifest rejection and resource closure.
Deployment contracts protect the separate graph and inherited hardening. Real Mac
preparation, Linux image build and `/omc` smoke acceptance are required before the
first publication; implementation alone does not certify a production deployment.
