# msb-manager

A network-facing **control plane** for [microsandbox](https://github.com/superradcompany/microsandbox). It runs co-located with the microsandbox runtime on a single host and exposes an authenticated HTTP API for managing the lifecycle and configuration of microVMs across the network. It never proxies traffic into a running VM — clients reach VM contents directly.

## Language

**Control plane**:
The responsibility msb-manager owns: managing the lifecycle and configuration of sandboxes (create, start, stop, remove, inspect) and surfacing the information needed to reach them. Stops at the VM boundary.
_Avoid_: "management API" (too vague), "orchestrator" (implies scheduling we don't do yet).

**Data plane**:
Interacting with the contents of a running VM — a shell/PTY, the VM's own bearer-authenticated API, a matrix client, a web app on a published port. Explicitly **out of scope** for msb-manager; handled by other tooling talking to the VM directly.

**Sandbox**:
A microsandbox microVM — a hardware-isolated VM with its own Linux kernel, not a container. **The only noun msb-manager models.** (microsandbox's own term; we adopt it.)
_Avoid_: "container", "VM" in API surface (use "sandbox" for consistency with microsandbox), "instance".

**Agent**:
Informal slang for whatever workload runs *inside* a sandbox (e.g. an AI agent, a service). **Not a concept msb-manager models** — it never appears in the API or state. The control plane knows only sandboxes.
_Note_: microsandbox's own docs use "agent" to mean the *external* AI system (Claude Code, Cursor) that *drives* sandboxes — the opposite sense. Avoid the word in code and API to dodge the clash.

**Snapshot**:
A captured writable-layer artifact of a stopped sandbox, from which new sandboxes can be booted (`msb run --snapshot`). Created `--from` a stopped sandbox. msb-manager exposes snapshot operations as pass-throughs.

**Lineage**:
The ancestry tree of snapshots (image → snapshot → derived snapshot → …). microsandbox does **not** track this transitively — the parent edge lives only in the intermediate sandbox, which gets removed. msb-manager preserves it by stamping a `msb.parent` **label** at snapshot-create time. A *read-side lineage view* (graph walk over labels) is **deferred past v1**; v1 only stamps the label so the door stays open.

**Project**:
The logical unit of persistent work, **not** a host folder (we dropped host-side scaffolding). In the stateless model a project is: a **named microsandbox volume** (mounted at `/workspace`, survives `rm`/recreate, referenced by name) + a client-authored **spec** (see *Spec*) + its place in the snapshot **lineage**. The server stores no project record; the client is the source of truth.
_Concurrency_: exactly one *running* sandbox may mount a given volume at a time, enforced by msb-manager (a server-owned lock keyed by volume name, reconciled against `msb ls`).

**Spec**:
A declarative description of **a single sandbox** to create — image-or-snapshot, memory, CPUs, volume, env, secrets, SSH public keys, setup script, network policy. Authored client-side as YAML (or JSON) and submitted to the create endpoint; the server owns the schema and validates it (kubectl model, ADR-0005), so the client is a thin transport. One spec maps 1:1 to one sandbox — it is **not** a multi-resource orchestration file (no service graph, no fan-out). **The spec is the project's durable definition and lives with the client** (e.g. a specs repo), since the server is stateless.
_Avoid_: "compose-style" (implies multi-resource orchestration we don't do — a spec is single-sandbox, kubectl-style).

**Derived sandbox**:
A sandbox booted from a snapshot. On creation a flag chooses its volume: **new volume** (a fresh project off a base) or **reuse the ancestor's volume** (the snapshot→recreate-in-place case).

**Connection info**:
The address and published guest ports of a running sandbox, surfaced by the control plane so external tooling can reach the data plane directly. msb-manager reports it; it does not route it.

**msbctl**:
The remote command-line client for msb-manager — a thin, **opaque** HTTP client that owns no schema (the server validates). It streams a spec to create and pretty-prints JSON responses for reads. Lives in-repo (`cmd/msbctl`) but imports nothing under `internal/` (ADR-0007).
_Avoid_: "agent", "SDK" (it is neither — it wraps the HTTP API, not the `msb` CLI).

**Trust domain**:
v1 is a **single trust domain** — any valid bearer token has full access to every sandbox. Per-sandbox authorization is deferred past v1.

## Resolved decisions (v1)

- **Language: Go.** I/O-bound subprocess orchestration; stdlib does the whole job.
- **Stateless server, client is source of truth.** No database. `msb ls --format json` is the source of truth for "what exists"; the client holds project specs/secrets. msb-manager shells out to the `msb` CLI (not the SDK).
- **Ops-endpoint auth boundary.** `/healthz` (shallow, cheap) and `/readyz` are the only unauthenticated endpoints; everything else is behind the bearer token. `/readyz` runs `msb ls` to prove the supervisor is up, so its result is cached for a short TTL (`readinessTTL`, 2s) to stop an unauthenticated caller driving unbounded — and potentially hanging — subprocesses (issue #6). Caching was chosen over requiring auth so the probe contract stays simple and Caddy/systemd need no token plumbing.
- **Persistence via named microsandbox volumes**, not host-side folders. Everything is driven remotely over the API — the operator never touches the host filesystem.
- **Create accepts a declarative spec** (YAML or JSON), single-sandbox, **server-owned schema** (kubectl model, ADR-0005): the server parses and authoritatively validates because it shells out to `msb`. A thin client CLI streams a spec file or builds the object from flags, but never owns the schema. A future multi-sandbox "project file" is a client-only fan-out, never server-side.
- **Remote client `msbctl` is in-repo, opaque, HTTP-only** (ADR-0007): a thin transport with no shared schema and no `internal/` imports. Target/auth via a precedence chain (flags > env > config-file profiles > defaults); the bearer token is preferred via env or a `0600` config file, not argv.
- **Response DTOs are a deliberate contract** (ADR-0006): handlers map `internal/msb` adapter types onto an `internal/api` package rather than serialising adapter structs directly, so an adapter refactor can't silently reshape the public API. Symmetric to the inbound `spec.Spec → msb.CreateOpts` seam.
- **Client-held secrets via interpolation** (ADR-0008): specs carry `${VAR}` placeholders; `msbctl` does value-safe textual `envsubst` over the raw bytes (from environment or per-run flags) before POSTing, so secret values never sit in the committed spec and the client stays opaque.
- **Credentials applied per-create, kept out of snapshots.** Egress tokens via `--secret KEY=VAL@host`; SSH public keys installed into the guest at create; private/file secrets mounted, not baked.
- **Concurrency:** one running sandbox per volume, enforced by a server-owned lock reconciled against `msb ls`.
- **Startup fails closed on the volume-lock reconcile** (issue #20): the server retries the startup reconcile with backoff (1s doubling to a 30s cap) and does not start the listener until one succeeds, so lock-guarded mutations can never be served with an unseeded lock. Chosen over fail-startup (systemd restart churn for a transient msb hang — verification #3) and over serve-but-503 (more moving parts for the same gate). A shutdown signal during the loop aborts startup.
- **Core surface:** `list / inspect / create / start / stop / rm / exec / logs / metrics` for sandboxes; `snapshot ls / create / inspect / rm` as pass-throughs; `volume` create/ls/rm. **Volume create is dual-shape** (ADR-0009): the single `{name, size}` body (`201 {name, size}`), or a declarative batch manifest `{volumes:[...]}` — atomic pre-flight validation (`400`, zero msb calls) then best-effort per-item execution returning `{results:[...]}` with `201` (all created/exists) or `207` (any error). `msbctl volume create -f <file|->` submits a batch and exits non-zero on `207`.
- **Logging: fetch-only, no streaming.** `GET /logs` wraps `msb logs --json` (`--tail`/`--since`/`--source`). No SSE, no log forwarding — microsandbox already writes per-sandbox JSON Lines to disk. msb-manager emits its own logs as structured JSON to stdout.
- **Deployment: one local Caddy as the TLS front door.** A single Caddy on the Lenovo box terminates TLS (DNS-01 wildcard certs for `picton.uk`) and fronts both planes: `msb.home/.meshnet.picton.uk` → msb-manager; `*.msb.home/.meshnet.picton.uk` → VM host ports (data-plane routing, see [`docs/vm-access-routing.md`](docs/vm-access-routing.md)). **msb-manager binds loopback only**; Caddy is the sole exposed listener. DNS records in pihole/unbound. Runs as a systemd service, non-root user in the `kvm` group. The meshnet is for off-LAN *reachability*, not transport security (Caddy gives TLS on the LAN too). Local Caddy avoids the SPOF of routing through bubacano's Traefik.

## Deferred (no rework to add later)

- **Optional host-folder bind mount** as an alternative/addition to named volumes (operator-requested; may want it later for direct host access to a workspace).
- Read-side **lineage view** (graph walk over `msb.parent` snapshot labels).
- **Stateful project registry** (server remembers specs/secrets so any client can fork without holding the spec) — would add secret-at-rest encryption.
- **Per-sandbox auth scopes** (currently single trust domain).
- **Log aggregation** (Grafana/**Loki**): a host-side collector (Grafana Alloy / OTel Collector) tails microsandbox's JSONL log files and pushes to Loki — *not* msb-manager's job. Optionally enriched with control-plane labels (`project`, `lineage_parent`, `base_snapshot`).
- **Metrics scraping** (**Prometheus**): an optional `/metrics` exposition endpoint (msb-manager + `msb metrics` data). Distinct from logs — Prometheus pulls metrics, Loki ingests logs.
- **Log streaming** (SSE `?follow`): explicitly **not wanted** — no human live-watching. Recorded only so it's clear it was considered and declined.

## Open verifications (first build session)

The stateless model rests on three unverified microsandbox behaviours. None block the design; each is a one-command check to do before building on it:

1. **Does `msb inspect --format json` echo volume mounts and env?** ✅ *Resolved (msb v0.5.2, 2026-06-03).* Both surfaced. `config.env` as `[key, value]` tuples. `config.mounts` distinguishes the auto `Tmpfs` (`{type:"Tmpfs", guest:"/tmp", size_mib}`) from a **named volume** (`{type:"Named", name:"myvol", guest:"/workspace", readonly, host_permissions, stat_virtualization}`). The `name` field carries the volume source, so **project membership and the one-VM-per-volume check are derivable from msb state alone — the lock stays stateless** (no server-owned volume map). Fixtures: `internal/msb/testdata/{inspect,inspect_named_volume}.json`; helper `SandboxDetail.VolumeNames()`.
2. **Is volume `--size` sparse/thin?** ✅ *Resolved (msb v0.5.2, 2026-06-04).* Strongly sparse: a 10 GiB volume occupied 4 KiB on disk. Quotas can be over-committed freely (ADR-0004).
3. **Is `msb` safe under concurrent invocation?** ❌ *Resolved (msb v0.5.2, 2026-06-04) — NO.* Two parallel `msb create` got stuck and left subsequent `msb ls` hanging (lock contention against the supervisor). msb-manager must **serialise mutating commands** (create/start/stop/rm) — step 5 design now includes a per-process global mutex in addition to the per-volume `O_EXCL` lock. (The TTY-suspend in interactive shells is a separate artifact and doesn't affect msb-manager, which uses `exec.Cmd` pipes.)
   - *Re-checked 2026-06-08 (msb v0.5.2): could **not** reproduce the supervisor hang when the two `msb create`s were run with stdout/stderr redirected to pipes/files (msb-manager's actual mode) — both sandboxes came up `Running` and `msb ls` returned cleanly. An interactive backgrounded run (`&` writing the progress spinner to the TTY) only hit the known TTY-suspend artifact, not the supervisor lock. The hang is evidently load/timing-sensitive rather than deterministic. Decision unchanged: keep the serialising mutex plus the per-invocation timeout (issue #4) as cheap insurance — msb-manager never issues concurrent mutating calls anyway, and the timeout is the real backstop against any future wedge.*

## msb CLI surface (verified, v0.5.2, 2026-06-04)

- **`--secret ENV=VALUE@HOST`** is present on `create`/`run`. Egress creds (step 6) are unblocked. Companion flag `--on-secret-violation` controls policy (`block`, `block-and-log`, `block-and-terminate`, `passthrough`).
- **No SSH-pubkey install flag.** `msb ssh` exists for *connecting* but nothing on `create`/`run` for installing a key into the guest. Step 6 needs another path — a bootstrap script writing to `authorized_keys` is the obvious one.
- **Error shape** (all exit 1, stderr `error: <category>: <details>`):
  - `sandbox not found: <name>` → mapped to HTTP 404
  - `sandbox already exists: <details>` → 409
  - `sandbox still running: <details>` → 409 (rm of running sandbox)
  - `volume already exists: <name>` → 409
  - `volume not found: <name>` → 404
  - Anything else stays 500. Fixtures in `internal/msb/errors_test.go`; classifier in `internal/msb/errors.go`.
- **`msb volume rm` does NOT block on in-use volumes.** Verified: removing a volume currently mounted by a running sandbox returns exit 0 and the volume is gone (the running sandbox keeps the mount until it stops, but the volume is no longer in `msb volume ls`). msb-manager's `DELETE /volumes/{name}` consults the in-memory VolumeLock and returns 409 when a running sandbox holds the claim — a safer invariant than msb itself enforces.
- **`msb inspect --format json` echoes secret values in plaintext** under `config.network.secrets.secrets[].value`. Likely an upstream bug (the placeholder/host-allowlist structure is useful, the raw value is not). msb-manager's parser deliberately omits the `network` subtree from `SandboxDetail`, so `GET /sandboxes/{name}` doesn't surface it; `TestParseInspect_DoesNotLeakSecretValue` is the regression guard.
- **`--secret` split direction for a VALUE containing `@` — UNVERIFIED; rejected fail-closed (issue #22, 2026-06-12).** Whether msb v0.5.2 splits `VALUE@HOST` at the first or the last `@` has not been checked (no msb available in the fixing session). Because `@HOST` is the egress allow-list, a first-`@` split would silently release the secret to a host derived from the value, so `spec.Validate()` rejects `@` in `secrets[i].value`, mirroring the key/host checks. To relax: verify with `msb create --secret 'K=a@b@allowed.host'` and inspect the parsed allow-list; if it proves a last-`@` split, drop the value check and pin the accepted shape with a `buildCreateArgs` test plus a comment at the assembly site.
- **Egress secret values are inline in `msb create` argv — ACCEPTED V1 RISK (issue #7).** `buildCreateArgs` assembles `--secret KEY=VALUE@HOST`, so the value is world-readable via `/proc/<pid>/cmdline` for the lifetime of the `msb` child. Accepted for v1: single host, msb-manager runs non-root, single trust domain. The create/log side of the leak is closed — `redactSecrets` scrubs secret values out of `msb` stderr before it's folded into an error and logged, guarded by `TestClientCreate_DoesNotLeakSecretInError` (mirrors the inspect-side guard). **Verified (msb v0.5.2, 2026-06-08):** `msb create --help` exposes only `--secret ENV=VALUE@HOST` (value inline) — there is no `--secret-file`, env-ref, or stdin path to migrate to, so the argv exposure cannot be closed in v1 and stays an accepted risk until upstream adds an off-argv form.
