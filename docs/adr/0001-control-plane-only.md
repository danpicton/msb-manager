# 0001 — msb-manager is a control plane only

**Status:** accepted

msb-manager manages the **lifecycle and configuration** of microsandbox VMs (create, start, stop, remove, inspect, snapshot) and *reports* each VM's connection info, but it **never proxies traffic into a running VM**. Reaching VM contents — a shell, the VM's own API, a matrix client, a web app on a published port — is a data-plane concern handled by other tooling talking to the VM directly.

## Why

The two responsibilities have radically different engineering shapes: the control plane is request/response lifecycle management; the data plane is long-lived, stateful, streaming connections and reverse-proxying. Bundling them would drag websockets, PTY handling, and a dynamic proxy into a daemon whose job is orchestration. Keeping the boundary at the VM edge keeps msb-manager small, stateless, and restart-safe.

## Consequences

- msb-manager must still *surface* connection info (address + published ports) so external tooling can reach the data plane. This is the one hook the routing companion depends on.
- URL-based access to VM services is a separate, out-of-scope system; the intended pattern is recorded in [`docs/vm-access-routing.md`](../vm-access-routing.md).
