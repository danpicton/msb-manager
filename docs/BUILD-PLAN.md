# Build plan — v1

The design is in [CONTEXT.md](../CONTEXT.md) and [docs/adr/](adr/). This is the *build order* and the v1 **API surface**. Work TDD (the `tdd` skill); see [CLAUDE.md](../CLAUDE.md) for the test seams.

## v1 API surface

Single bearer token on everything except `GET /healthz`. Create accepts a declarative **spec** as YAML or JSON (content-type), compose-style.

### Sandboxes
| Method | Path | Wraps | Notes |
|---|---|---|---|
| `GET` | `/sandboxes` | `msb ls --format json` (+ inspect) | name, state, base, connection info (address + published ports), volume |
| `GET` | `/sandboxes/{name}` | `msb inspect --format json` | |
| `POST` | `/sandboxes` | `msb create`/`run` | from spec: image **or** snapshot, memory, cpus, volume, env, secrets, ssh pubkeys, ports, network policy, setup script. Acquires volume lock; applies credentials; stamps `msb.parent` if derived |
| `POST` | `/sandboxes/{name}/start` | `msb start` | |
| `POST` | `/sandboxes/{name}/stop` | `msb stop` | optional timeout/force |
| `DELETE` | `/sandboxes/{name}` | `msb rm` | optional force; releases volume lock |
| `POST` | `/sandboxes/{name}/exec` | `msb exec` | optional per-call env; returns stdout/stderr/exit |
| `GET` | `/sandboxes/{name}/logs` | `msb logs --json` | `tail`, `since`, `source` query params; fetch-only (no streaming) |
| `GET` | `/sandboxes/{name}/metrics` | `msb metrics` | |

### Snapshots
| `GET` | `/snapshots` | `msb snapshot ls` | includes labels (incl. `msb.parent`) |
| `POST` | `/snapshots` | `msb snapshot create --from <stopped sandbox>` | stamps `msb.parent` label |
| `GET` | `/snapshots/{name}` | `msb snapshot inspect` | |
| `DELETE` | `/snapshots/{name}` | `msb snapshot rm` | |

### Volumes
| `GET` | `/volumes` · `POST` `/volumes` (name, size) · `DELETE` `/volumes/{name}` | `msb volume …` | |

### Ops
`GET /healthz` (no auth).

## Build order (first slice → full v1)

0. **Open verifications** (see CONTEXT.md) — does `msb inspect --format json` show mounts/env; is `--size` sparse; is `msb` concurrency-safe. Results shape steps 5 and the lock design.
1. **Skeleton:** Go module, config (token, data dir, `msb` path, listen addr — loopback), structured JSON logging, bearer middleware, `/healthz`, graceful shutdown.
2. **`msb` adapter module:** the single seam that shells out with `--format json`; typed wrappers; injected exec-runner for tests; snapshot-tested parsers.
3. **Core lifecycle:** list, inspect, create (image-only first — no volumes/creds yet), start, stop, rm. Establish error mapping (exit code/stderr → HTTP status).
4. **Spec parsing:** YAML/JSON spec → `msb` create args (pure function — TDD heavily).
5. **Volumes + one-VM-per-volume lock:** lockfile via `O_EXCL`, reconciled against `msb ls` to reclaim stale locks (TDD the state machine).
6. **Credentials:** env (`-e`), egress secrets (`--secret KEY=VAL@host`), ssh pubkey install, bootstrap script on create — kept out of snapshots.
7. **Snapshots + derived sandboxes:** list/create (stamp `msb.parent`)/inspect/rm; create from snapshot with the volume flag (new volume vs reuse ancestor's).
8. **Logs + metrics** fetch endpoints.

## Out of scope for v1 (see CONTEXT.md "Deferred")

Lineage *view* endpoint, per-sandbox auth scopes, stateful project registry, log streaming/aggregation, optional host-folder mount. None require rework to add later.
