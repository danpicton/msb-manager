# 0006 — Public response DTOs; adapter types are not the wire contract

The HTTP handlers currently serialise `internal/msb` adapter structs
(`msb.Sandbox`, `msb.SandboxDetail`, `msb.Metrics`, `msb.Snapshot`,
`msb.Volume`) straight to the wire, so the public API response shape is an
*accident* of how an internal scratch type happens to be JSON-tagged. We will
introduce an `internal/api` package of response DTOs and map adapter types onto
them in the handlers — the symmetric counterpart to the existing inbound seam
(`spec.Spec` → `msb.CreateOpts` via `ToCreateOpts`).

## Why

The request side already has a deliberate translation seam, but the response
side is wired straight through. That means an internal adapter refactor —
renaming a field, scraping an extra value out of `msb`, adapting to a new `msb`
output format — would *silently change the public API* and break clients with no
visible cause. A named DTO layer makes the wire contract a thing we own and
change deliberately.

## Consequences

- The duplication (`api.SandboxSummary` mirroring `msb.Sandbox`, etc.) is
  intentional: it is the contract boundary, not redundancy to "simplify" away.
- `msbctl` stays opaque (ADR-0007) and does **not** import these DTOs, but they
  pin the JSON shape the client pretty-prints, so the contract is stable.
- The mapping is the place to drop fields the public API should never expose
  (cf. the deliberately-omitted `network` subtree that carries plaintext
  secrets, CONTEXT.md "msb CLI surface").
