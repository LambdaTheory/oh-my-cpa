# ADR 0117: Agent commits use the human git identity in this repository

- Status: Accepted
- Date: 2026-11-03

## Context

The team standard (`templates/project-agents.md` and dev-workflow §3) asks an agent working
on a team repository to commit, push and comment as `lambdatheory-agent[bot]`, with the
agent kind as the author name, and to act in a person's name only through a per-action
`gh-human` confirmation. That rule presumes the GitHub App is installed on the repository
and that the machine provides the `AGENT_KIND` and `gh-human` entry points.

Neither holds for this repository. The `lambdatheory-agent` App is not installed on
`LambdaTheory/oh-my-cpa` and has no near-term plan to be; the local agent environments
have no `AGENT_KIND` and no `gh-human`. The alternative the standard sanctions—report the
integration gap and wait—would block every change indefinitely. The history gives the
outcome of not waiting: all 54 pull requests merged into this repository so far were
authored and commented by the human account `Newoahil`
(88387859+Newoahil@users.noreply.github.com), and that identity is what the repository's
review trail already reads.

The repository owner recorded the exception on 2026-11-03: this repository stops waiting
for the bot identity.

## Decision

Agent work in this repository commits, pushes and comments on pull requests as the
currently logged-in human identity, `Newoahil` with the noreply address above. The author,
committer and PR text all name that human account; no bot author is written and no bot
identity is claimed. The bot requirement is void here until the App is actually installed.

Independence between agents is not carried by the account. It comes from a separate
session and a separate agent working the task, each loading this contract, rather than
from a distinguishable git author.

## Consequences

The team standard's bot rule does not apply to this repository, and an agent that reads
`AGENTS.md` needs no report-and-wait step to proceed. The repository's authorship trail
stays uniform: every commit and PR is `Newoahil`, so an agent's work is identifiable by
its branch, session and PR text rather than by its author field.

This decision is scoped to this repository and to this exception; other team repositories
follow the standard unchanged. If `lambdatheory-agent` is installed here later, a new ADR
supersedes this one and restores the bot identity for subsequent work.
