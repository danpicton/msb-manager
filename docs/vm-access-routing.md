# Accessing VM services by URL (non-goal of msb-manager)

**Status:** intended design, not built. **Owner:** out of scope for msb-manager — this is a *companion* system.

## Why this is here

msb-manager is a **control plane only**: it manages sandbox lifecycle and *reports* each VM's connection info (address + published ports), but it never proxies traffic into a VM (see ADR-0001). Reaching a VM's own service over a friendly URL is a **data-plane** concern, handled by a separate routing layer. This document records the intended pattern so it can be picked up later without re-deriving it.

## Goal

Reach each VM's service at a stable URL, e.g. `https://<vmname>.msb.home.picton.uk` (and the `.meshnet` equivalent).

## Decided shape

- **Subdomain-per-VM, not path-based.** `<vmname>.msb.home.picton.uk` gives each VM a clean, isolated origin. Path-based routing (`…/msb/<vmname>`) breaks real web apps — root-relative links, path-scoped cookies, redirects, websockets all assume the app lives at `/`.
- **Caveat:** the existing `*.home.picton.uk` wildcard does **not** cover the two-label `*.msb.home.picton.uk` (DNS/TLS wildcards match a single label). This needs its **own** wildcard DNS record and cert.

## Topology

```
client ──https──▶ Caddy (on Lenovo box) ──http──▶ 127.0.0.1:<hostport> (VM's -p published port)
                     ▲
                     │ rewrites routes on change
              route controller ──polls──▶ msb-manager  GET /sandboxes  (name + published ports)
```

1. **DNS (pihole/unbound):** `*.msb.home.picton.uk → <Lenovo LAN IP>` and `*.msb.meshnet.picton.uk → <Lenovo meshnet IP>` (the meshnet record assumes the Lenovo box has joined the NordVPN meshnet — see ADR-0001 / deployment).
2. **Reverse proxy on the Lenovo box (Caddy):** listens on 443, routes by Host header `<vmname>.msb.home.picton.uk → 127.0.0.1:<hostport>`, wildcard cert for `*.msb.home.picton.uk` via Let's Encrypt **DNS-01**. Handles websockets transparently. **This is the same Caddy that fronts the control plane** — it also routes the apex `msb.home/.meshnet.picton.uk → 127.0.0.1:<msb-manager-port>`, so request the apex name alongside the wildcard in the cert block (a single-label wildcard does not cover the apex).
3. **Route controller (the only new code, ~100 lines or a `jq`+`curl` script):** polls msb-manager's `GET /sandboxes`, and on any change rewrites Caddy's config via its admin API (or config file + reload). A *consumer* of msb-manager, kept separate to preserve the boundary.

## Why not route via bubacano's Traefik

Same single-point-of-failure as routing the control plane through bubacano, plus a worse dynamic problem: per-VM backends on a *different box* would mean syncing a k8s `Endpoints`/`IngressRoute` per VM into bubacano's cluster, churning as VMs come and go. Keep the VM proxy **local to the Lenovo box**, next to the host ports it targets.

## Open details to settle when building

- **Host-port allocation:** prefer `-p 0:<guest>` (auto-assign) and read the real host port back from `msb inspect`, so collisions are never hand-managed. The route controller reads the actual mapping regardless.
- Whether to expose over `.home` (LAN) as well as `.meshnet`, and the cert story for the LAN variant.

## The one hook this depends on

This works **only because** msb-manager exposes clean connection info (`name` + published ports) on its API. Keep that in scope — it is the contract the route controller consumes.
