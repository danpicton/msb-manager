# 0005 — Single-resource spec object, server-owned schema (kubectl model)

The create endpoint accepts a sandbox **spec**, and we had to decide where the
schema lives: does the server receive a human spec and parse/validate it, or
does it receive only flat structured params with the client doing the YAML
deconstruction (the Docker-daemon model)? We chose the **kubectl model**: the
server owns the `Spec` schema and is the sole authoritative validator, and the
wire carries a single-resource spec *object* accepted as **either YAML or JSON**
through one decoder. The client is a thin transport — it streams a spec file or
builds the object from flags, but never owns the schema.

## Why

The server **must** validate regardless of any client, because it shells out to
`msb` with these values: identifier safety (issue #3), secret format (issue #7),
unknown-field rejection. Since the server already parses-to-validate, accepting
the spec object directly is nearly free, and it keeps thin clients (a bare
`curl` streaming a `spec.yaml`) first-class — the project's "client holds the
spec in a git repo" stance (ADR-0003). Moving parsing to the client would
*duplicate* validation rather than relocate it, and would force every client to
embed the schema.

## Considered options

- **Docker-daemon params** — client deconstructs YAML → flat params, server takes
  params only. Rejected: doesn't remove server-side validation (subprocess
  safety), and kills the thin client.
- **Compose-style fan-out** — a multi-resource file translated client-side into N
  calls. Not applicable in v1: a spec is single-sandbox and maps 1:1 to
  `msb.CreateOpts`. There is no fan-out to justify a client-side layer.

## Consequences

- The wire contract is one spec object per `POST /sandboxes`; YAML and JSON are a
  content-type detail, not two code paths (yaml.v3 parses both).
- A future **multi-sandbox "project file"** (several sandboxes + volumes +
  ordering in one document) is reserved as a **client-only** concern that fans
  out over N `POST /sandboxes` calls. The server stays single-resource and never
  learns that format.
