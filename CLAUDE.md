# msb-manager

A network-facing **control plane** for [microsandbox](https://github.com/superradcompany/microsandbox): an authenticated HTTP API to manage microVM lifecycle across the network, running on a single host (a Lenovo tiny desktop).

## Read first

Before writing any code, read these — the design is settled and these records are authoritative:

- **[CONTEXT.md](CONTEXT.md)** — glossary, resolved decisions, deferred items, open verifications.
- **[docs/adr/](docs/adr/)** — the four architecture decisions and *why*.
- **[docs/BUILD-PLAN.md](docs/BUILD-PLAN.md)** — v1 API surface and the ordered first slice.
- **[docs/vm-access-routing.md](docs/vm-access-routing.md)** — VM-by-URL access (a *non-goal*; separate companion system).

## Invariants — do not violate without a new ADR

- **Control plane only.** Manage lifecycle; never proxy traffic into a VM. (ADR-0001)
- **Wrap the `msb` CLI, never the SDK.** Shell out with `--format json`. (ADR-0002)
- **Language is Go.** Stdlib-first; minimal dependencies. (ADR-0002)
- **Stateless. No database, no project registry.** `msb ls` is the source of truth for what exists; the client holds project specs. Any unavoidable server state is the smallest possible filesystem state, reconciled against `msb`. (ADR-0003)
- **Persistence via named microsandbox volumes, not host-side folders.** (ADR-0004)
- **Single bearer token, single trust domain.** Token from config/env, constant-time compare.
- **Binds loopback only.** Caddy (separate) terminates TLS and fronts it. Runs under systemd as a non-root user in the `kvm` group.

## How we work

- **TDD.** Use the `tdd` skill (red-green-refactor). The highest-value test seams are the pure layers: spec→`msb`-args translation, `msb`-JSON→domain structs, the one-VM-per-volume lock state machine, and lineage-label stamping. Mock the subprocess boundary via an injected exec-runner interface; reserve integration tests for a real `msb`.
- Keep the `msb` CLI interaction in **one adapter module** with snapshot-tested parsers, so a CLI-output change is a one-place fix.
- **Issues and PRs** follow [docs/issue-pr-guidelines.md](docs/issue-pr-guidelines.md) and the templates in `.github/` — location-prefixed titles, checkable acceptance criteria, explicit out-of-scope, named test seam.
