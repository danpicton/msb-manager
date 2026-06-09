# 0007 — msbctl: an in-repo, opaque, HTTP-only client

The remote CLI client, **`msbctl`**, lives in this repository as a second binary
(`cmd/msbctl`), talks to msb-manager over **HTTP only**, and is **opaque**: it
does not own or import the spec schema or the response DTOs. For create it
streams a spec file (after textual interpolation, ADR-0008) to `POST /sandboxes`
and lets the server validate; for reads it pretty-prints the JSON responses
generically.

## Why

ADR-0005 put the schema and authoritative validation on the server, so the
client needs no shared Go types — it is a thin transport (the kubectl model).
With nothing to share, "monorepo for shared types" is moot; in-repo wins purely
on convenience for a single-operator project (one module, one CI, contract and
client move together). A separate repo would force a published contract module
for no v1 benefit.

## Considered options

- **Separate repository** — rejected for v1: independent release cadence is a
  future problem, and extraction later is a clean `git filter-repo`.
- **Typed client sharing the response DTOs** — rejected: couples the client to
  the contract package and buys little over generic pretty-printing in v1.

## Consequences

- **Boundary rule (do not violate):** `cmd/msbctl` imports nothing under
  `internal/` — it speaks HTTP and JSON only. Holding this line keeps later
  extraction trivial and is the test that the client stayed opaque.
- Target/auth resolution is a precedence chain: **flags > env >
  config-file profiles > defaults**. The bearer token is preferred via env or a
  `0600` config file, **not** `--token` (argv leak, cf. issue #7).
