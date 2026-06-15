# Issue and PR guidelines

Issues and PRs here are read by humans **and** by coding agents picking work up cold. The templates in [`.github/`](../.github/) enforce the structure; this page records the why. The rules are short because most of "agent-readable" is just good practice for humans too — the delta is explicitness.

## Issues

Write every issue as though briefing someone brand new to the codebase. Concretely:

- **Title says where, not just what.** Prefix with the area in brackets — `[api]`, `[msb adapter]`, `[volume lock]`, `[docs]`. A panel of issues should be navigable by title alone.
- **Context is mandatory.** One or two sentences of *why*. A human teammate can fill in unstated rationale from hallway context; an agent cannot.
- **Acceptance criteria are checkable bullets.** "Done" must be decidable without asking the author. If a criterion can't be phrased as a tickable box, the issue isn't ready.
- **State what's out of scope.** Agents (and enthusiastic humans) wander; an explicit fence is cheaper than review comments.
- **Give pointers.** Relevant files, ADRs, fixtures, or an example of the pattern to follow. Embedding a short code sample of the shape you want is high-leverage.
- **Name the test seam.** This repo works TDD-first; say which seam the work lands in — spec→`msb`-args translation, `msb`-JSON→struct parsing, the volume-lock state machine, or lineage-label stamping — and what proves it works.
- **Scope narrowly.** "Update the whole error path" is a bad issue; "map `volume not found` stderr to HTTP 404 in `internal/msb/errors.go`" is a good one. Split rather than broaden.

## PRs

A reviewer should find any answer in under a minute. Five short sections, optional ones deleted rather than left as "N/A":

- **What & why** — one or two sentences; link the issue (`Closes #N`) instead of repeating it.
- **How** — the approach, plus alternatives rejected and why. This is the part the diff can't show.
- **Test plan** — what was run and what proves it works, not just "tests pass".
- **Risk & rollback** — only when the change can break something at runtime.
- **Invariants** — if the PR touches an invariant from [CLAUDE.md](../CLAUDE.md), it must link the new ADR that authorises the change. No ADR, no merge.

Keep the diff focused. Related issues may be grouped into one PR when they share a seam and review reads more naturally together — link each (`Closes #N, Closes #M`) and keep them as separate, self-contained commits. If the work sprawled beyond what a reviewer can follow in one sitting, split the PR rather than grow the description.

## Sources

Distilled from GitHub's coding-agent guidance ([assigning issues to coding agents](https://github.blog/ai-and-ml/github-copilot/assigning-and-completing-issues-with-coding-agent-in-github-copilot/), [WRAP up your backlog](https://github.blog/ai-and-ml/github-copilot/wrap-up-your-backlog-with-github-copilot-coding-agent/)) and standard PR-template practice ([Graphite](https://graphite.com/guides/github-pr-description-best-practices), [minware](https://www.minware.com/blog/effective-pr-template)).
