# 0004 — Persistence via named volumes, not host-side scaffolding

**Status:** accepted

A project's persistent workspace is a **named microsandbox volume** (mounted at `/workspace`, referenced by name, surviving `rm`/recreate), not a host-side directory tree (`<project>/workspace`, `secrets`, `project.toml`) that an operator edits. Everything is driven over the API; the operator never touches the host filesystem.

## Why

An earlier design scaffolded a host folder per project, justified by "edit the config file on the box and the VM re-reads it." Once the requirement became **fully remote, never touch the box**, that justification collapsed: named volumes give persistence-across-recreate *and* are managed entirely through the API (`msb volume`), with no host path to maintain. Credentials are applied per-create and kept out of snapshots rather than living in host files.

## Consequences

- One running sandbox per volume, enforced by a small server-owned lock keyed by volume name, reconciled against `msb ls`.
- Volume `--size` is a quota/cap (thin), not a reservation — verify the sparse behaviour empirically before relying on over-commit.
- An **optional host-folder bind mount** is explicitly left as a future addition (operator may want direct host access to a workspace) — see deferred list in `CONTEXT.md`.
