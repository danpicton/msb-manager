# 0008 — Client-held secrets via textual interpolation

Egress secret *values* never live in a committed spec. A spec carries only the
secret's structure with a `${VAR}` placeholder for the value; `msbctl` performs
a textual `${VAR}` substitution over the raw spec bytes at send time (envsubst-
style, the docker-compose model) before POSTing. Values come from the operator's
environment or from per-run flags that populate the interpolation table — never
by the client parsing and rewriting the spec.

## Why

It keeps secrets out of version control (the spec lives in a git repo, ADR-0003)
while leaving the client **opaque** (ADR-0007): textual interpolation needs no
schema knowledge, whereas structurally merging a `--secret` flag into the spec
would force the client to parse it. One mechanism (interpolation), two value
sources (environment, per-run flag).

## Consequences

- The server still re-validates the interpolated spec, so a malformed value is
  caught server-side (`spec.Validate` rejects `=`/`@`/newlines in secret fields).
- **Sharp edge:** textual substitution into YAML can let a value containing a
  newline or YAML metacharacter alter document structure. Interpolation must be
  **value-safe** (JSON-encode the substituted value, or interpolate post-parse);
  the server validation is defence-in-depth, not the only guard.
- This does not revisit the deferred server-side secret store; secrets remain
  client-held.
