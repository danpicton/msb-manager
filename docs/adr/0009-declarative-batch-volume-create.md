# 0009 — declarative batch volume create

`POST /volumes` accepts, in addition to the single `{name, size}` body, a
declarative **batch manifest** listing one or more volumes:

```yaml
volumes:
  - name: alpine-data
    size: 1G
  - name: pg-data
    size: 10G
```

`msbctl volume create -f <file|->` submits it. This stands up a project's volumes
from a checked-in manifest in one call — the PVC-apply model — keeping size on
the durable volume rather than the ephemeral sandbox spec (ADR-0004).

## The three settled rules

1. **Best-effort, not transactional.** Execution is split into two phases. A
   *pre-flight* validates every item (name via `msb.ValidName`, size via
   `msb.ValidSize`); if any item is malformed the whole request is rejected
   `400` and **nothing** is created — a typo never half-applies, and the
   pre-flight makes **zero** msb calls. The *execution* phase is then per-item
   and independent: one item failing does not roll back or abort the others.

2. **Create-if-absent; size-match = `exists`, mismatch = `error`.** Existing
   volumes are looked up once via `msb volume ls` (the source of truth —
   ADR-0003; there is no server-side size store). Each item is then decided
   against that map: absent → `created`; present at the same size → `exists`
   (no-op); present at a different size → `error`. "Same size" is a
   **unit-normalised** comparison — the requested size is parsed into msb's
   reported `quota_mib` (`1G` → 1024 MiB) and compared numerically, never as a
   string. msb cannot shrink or grow a volume, so a mismatch is a hard `error`,
   never a silent re-mount. This is additive create-if-absent only — not a full
   reconcile (no deletion of volumes absent from the manifest).

3. **`207` for partial failure.** The response body is always the full per-item
   result list (`{results:[...]}`, a public DTO — ADR-0006). The status code is
   `201 Created` when every item is `created`/`exists`, and `207 Multi-Status`
   when the pre-flight passed but **any** item is an `error`. `msbctl` renders
   the results in both cases and exits non-zero on `207` so unattended
   automation detects partial failure; the `207`-handling is generic, so msbctl
   gains no volume-specific knowledge and stays opaque (ADR-0007).

## Why

A declarative batch is the natural way to provision a project's volumes, and
splitting pre-flight from execution gives the strongest guarantee that is cheap
to keep: structural typos fail atomically, while genuine per-item outcomes
(already-exists, size clash, msb failure) are reported individually rather than
aborting the whole apply. The unit-normalised size comparison is the one detail
that must be exact — a string match would call `1G` and `1024M` different and
wrongly report a mismatch.

## Considered options

- **Transactional batch (all-or-nothing execution)** — rejected: msb has no
  multi-volume transaction, so "roll back on partial failure" would mean
  best-effort deletes that can themselves fail. Reporting per-item results is
  honest and simpler.
- **`volume apply` / full reconcile (delete volumes absent from the manifest)**
  — out of scope: destructive and a separate concern. This is create-if-absent
  only.
- **Resize on size mismatch** — out of scope: msb cannot shrink, and growing is
  a distinct operation. A mismatch is reported, not acted on.
- **A separate batch endpoint (e.g. `POST /volumes:batch`)** — rejected:
  branching `POST /volumes` on the presence of a `volumes` key keeps one URL for
  one resource and leaves the single-create contract untouched.

## Consequences

- The pre-existing single `{name, size}` body and its `201 {name, size}`
  response are unchanged (guarded by a regression test).
- The decision is a pure function — `planVolumeBatch(requested, existing
  name→MiB) → []result` — with no subprocess, so the create/exists/error rules
  are table-tested in isolation. The handler then executes only the `created`
  entries via `msb volume create`.
- Size normalisation lives in one place (`msb.ParseSizeMiB`), the single home
  for turning the human size grammar into the `quota_mib` unit.
