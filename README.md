# msb-manager

A network-facing HTTP control plane for [microsandbox](https://github.com/superradcompany/microsandbox): authenticated lifecycle management for microVMs across a network, running co-located with the `msb` runtime on a single host.

It shells out to the `msb` CLI (ADR-0002) — never embeds the SDK. It's stateless: the source of truth is whatever `msb ls` and `msb inspect` say.

> v1 control plane is feature-complete. See [`CONTEXT.md`](CONTEXT.md) for design intent, [`docs/adr/`](docs/adr/) for architecture decisions, [`docs/BUILD-PLAN.md`](docs/BUILD-PLAN.md) for the build order, and [`docs/upstream-issues.md`](docs/upstream-issues.md) for microsandbox bugs/gaps we work around.

## What it does

- Sandbox lifecycle (create / start / stop / rm / inspect / list)
- Snapshot management (list / create / rm) and **derived sandboxes** (boot a fresh sandbox from a snapshot)
- Named volume management (list / create / rm) with a one-running-sandbox-per-volume invariant
- Fetch-only **logs** (NDJSON) and point-in-time **metrics**
- Credentials at create-time: env vars, **egress secrets** (`--secret KEY=VAL@HOST`), **SSH pubkey install** via a bootstrap script
- Single bearer token; binds **loopback only** (Caddy is the TLS front door)

## What it does NOT do

- Proxy traffic into a running VM. The data plane is separate — see [`docs/vm-access-routing.md`](docs/vm-access-routing.md).
- Per-sandbox auth. v1 is a single trust domain.
- Stream or aggregate logs. `msb` writes JSONL to disk; ship to Loki via a host-side collector (Grafana Alloy / OTel) if you want.
- Store a project registry. Clients hold their own specs; restarts re-derive state from `msb`.

## Build & run

```bash
go build -o ./msb-manager ./cmd/msb-manager

MSB_MANAGER_TOKEN=$(openssl rand -hex 32) ./msb-manager
```

Static binary, stdlib-first plus `gopkg.in/yaml.v3`. Go 1.26.

## Configuration

All via env vars. Only `MSB_MANAGER_TOKEN` is required.

| Var | Default | What |
|---|---|---|
| `MSB_MANAGER_TOKEN` | (required) | Bearer token guarding every protected endpoint |
| `MSB_MANAGER_LISTEN_ADDR` | `127.0.0.1:8080` | Loopback by default; Caddy is the only external listener |
| `MSB_MANAGER_MSB_PATH` | `msb` | Path to the `msb` binary |
| `MSB_MANAGER_DATA_DIR` | `/var/lib/msb-manager` | Filesystem state root (reserved for future use) |

The server refuses to start without a token.

## API surface

Every route except `/healthz` and `/readyz` requires `Authorization: Bearer <token>`. Request bodies are YAML (default) or JSON; `Content-Type: application/yaml` and `application/json` are both honored, and `yaml.v3` parses JSON as a subset so the same body works either way.

### Health

| Method | Path | What |
|---|---|---|
| `GET` | `/healthz` | Liveness — the HTTP goroutine is alive. Cheap and shallow. |
| `GET` | `/readyz` | Readiness — `msb ls` succeeded. 503 when the supervisor is unreachable. Suitable for Caddy active probes. |

### Sandboxes

| Method | Path | Maps to |
|---|---|---|
| `GET`    | `/sandboxes` | `msb ls --format json` |
| `GET`    | `/sandboxes/{name}` | `msb inspect --format json` |
| `POST`   | `/sandboxes` | `msb create` (image) or `msb run -d --snapshot` (snapshot-derived) |
| `POST`   | `/sandboxes/{name}/start` | `msb start` |
| `POST`   | `/sandboxes/{name}/stop` | `msb stop` |
| `DELETE` | `/sandboxes/{name}` | `msb rm` |
| `GET`    | `/sandboxes/{name}/logs` | `msb logs <name> --json` |
| `GET`    | `/sandboxes/{name}/metrics` | `msb metrics <name> --format json` |

`GET /sandboxes/{name}/logs` accepts `tail`, `since`, `until`, `source`, `grep` query params, each mapping 1:1 to the `msb` flag of the same name. Response is `application/x-ndjson` — one JSON object per line, pass-through from `msb`.

### Snapshots

| Method | Path | Maps to |
|---|---|---|
| `GET`    | `/snapshots` | `msb snapshot ls --format json` |
| `POST`   | `/snapshots` | `msb snapshot create --from <from> [--label k=v ...] [-f] <name>` |
| `DELETE` | `/snapshots/{name}` | `msb snapshot rm <name>` |

`GET /snapshots/{name}` is deliberately absent: `msb snapshot inspect` has no `--format json`. See [`docs/upstream-issues.md`](docs/upstream-issues.md) issue #3.

### Volumes

| Method | Path | Maps to |
|---|---|---|
| `GET`    | `/volumes` | `msb volume ls --format json` |
| `POST`   | `/volumes` | `msb volume create --size <size> <name>` |
| `DELETE` | `/volumes/{name}` | `msb volume rm <name>` — **refused with 409 if a running sandbox holds the volume.** msb itself doesn't enforce this; msb-manager does. |

## Sandbox spec

The `POST /sandboxes` body. Either `image` or `snapshot` is required; they're mutually exclusive.

```yaml
name: my-sandbox            # required, unique sandbox name
image: alpine               # required (mutually exclusive with snapshot)
# snapshot: my-snap         # alternative: derive from a snapshot

cpus: 2                     # optional
memory: 512                 # MiB, optional

volume:                     # optional, single named-volume mount
  name: my-vol
  mount: /workspace

env:                        # optional, plain env vars (not secret)
  PATH: /usr/bin

ports:                      # optional, host:guest forwards
  - host: 8080
    guest: 80

secrets:                    # optional, msb's --secret KEY=VAL@HOST
  - key: GITHUB_TOKEN       # released only to outbound traffic for the host
    value: ghp_xxxxxxxx
    host: github.com

ssh_keys:                   # optional, OpenSSH-format pubkey lines
  - ssh-ed25519 AAAA...key... user@host
```

SSH keys are installed via a `--script-raw` snippet executed after create with `msb exec`; if the install fails, msb-manager rolls back with `msb rm -f` so create has atomic semantics.

## Status codes

| Code | When |
|---|---|
| 200 / 201 / 204 | Success |
| 400 | Malformed body / query param; spec validation failure |
| 401 | Missing or invalid bearer token |
| 404 | Sandbox / snapshot / volume not found |
| 409 | Duplicate name; sandbox still running on rm; volume in use |
| 413 | Spec body over 64 KiB |
| 500 | Unrecognised `msb` error (the underlying stderr is logged server-side, not echoed to the client) |
| 503 | `/readyz` only — `msb` is unreachable |

## Deployment

msb-manager binds loopback only. **Caddy is the single external listener** — it terminates TLS (DNS-01 wildcard) and reverse-proxies to msb-manager on `127.0.0.1`. systemd runs the binary as a non-root user in the `kvm` group. See [`CONTEXT.md`](CONTEXT.md#resolved-decisions-v1) for the full deployment picture.

## Concurrency

`msb` v0.5.2 is **not concurrent-safe** for mutating commands (CONTEXT verification #3 — parallel `msb create` left the supervisor unable to service `msb ls`). msb-manager serialises `create` / `start` / `stop` / `rm` via a per-process mutex; reads (`list`, `inspect`, `logs`, `metrics`) run unguarded. The one-running-sandbox-per-volume invariant is enforced by an in-memory `VolumeLock` seeded from `msb` state at startup.

## Development

```bash
go test ./... -race
go vet ./...
```

TDD per [`CLAUDE.md`](CLAUDE.md). The high-value test seams are the pure layers: spec→msb-args translation (`internal/msb/client.go`), `msb`-JSON→domain structs (`internal/msb/parse.go`, snapshot-tested via `internal/msb/testdata/`), the volume lock state machine (`internal/lock/`).

## License

Not specified yet.
