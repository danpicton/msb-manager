# 0003 — Stateless server; the client is the source of truth

**Status:** accepted

msb-manager holds **no database and no project registry**. `msb ls --format json` is the source of truth for *what exists*; the **client** holds the durable project definitions — declarative **specs** (YAML/JSON, compose-style), kept e.g. in a git repo and submitted to the create endpoint. Lineage is read from `msb.parent` **snapshot labels**, not a stored graph.

## Why

A second store is the classic source of drift, and the project's strongest pull (single-user home server, "everything driven remotely, never touch the box") is satisfied without one: persistence lives in named volumes (ADR-0004), config and secrets are supplied per-create by the caller, and the lineage *view* is reconstructable from labels. Keeping the server stateless makes it restart-safe and trivial to reason about, and means it never stores secrets at rest.

## Considered and rejected (for now)

A **stateful project registry** — the server stores specs + secrets so any client can fork/recreate without holding the spec. Rejected for v1 because it forces secret-at-rest encryption and a real store. It remains a clean additive layer if the *server* ever needs to be the durable source of truth, independent of any client.

## Consequences

- "Forking a project" is the client replaying a parent spec it holds; the server only records the lineage edge (label).
- Anything the server can't derive from `msb` (e.g. one-VM-per-volume locking) uses the smallest possible server-owned filesystem state, reconciled against `msb ls` — not a database.
