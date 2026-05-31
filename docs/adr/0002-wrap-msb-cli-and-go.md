# 0002 — Wrap the `msb` CLI (not the SDK), in Go

**Status:** accepted

msb-manager drives microsandbox by shelling out to the `msb` **CLI** (with `--format json`), not by embedding a language **SDK**, and is written in **Go**.

## Why the CLI, not the SDK

The decisive factor is **VM lifetime**. The SDK "embeds the runtime" and "spawns the VM as a child process," and the docs won't commit to the VM surviving the parent exiting — fatal for a fleet of persistent, always-on VMs that must outlive msb-manager restarts and deploys. The CLI talks to microsandbox's own supervisor, so VMs are decoupled from msb-manager's process lifecycle. The CLI is also the only **complete** management surface (`ls`, `start` a stopped sandbox, snapshots, metrics, volumes); the SDK is oriented to create/exec/stop within one process.

## Why Go

Wrapping the CLI makes the choice language-agnostic, so it comes down to fit: this is **I/O-bound subprocess orchestration**, not compute. Go's stdlib is the whole job (`os/exec`, `encoding/json`, `net/http`), goroutines map onto concurrent sandbox ops, and a single static binary deploys trivially under systemd. Rust's safety/perf advantages buy nothing here.

## Consequences

- We are coupled to `msb`'s `--format json` output schemas — pin the `msb` version, isolate parsing in one adapter module, snapshot-test it.
- Subprocess spawn cost per operation is irrelevant for a lifecycle control plane (no hot path).
