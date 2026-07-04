# Deploying msb-manager

The end-to-end server setup. The shape is fixed by the ADRs: msb-manager
binds **loopback only** and runs under **systemd as a non-root user in the
`kvm` group**; a single local **Caddy terminates TLS** (ACME DNS-01) and is
the only externally reachable listener. Clients talk HTTPS to Caddy with the
bearer token; see the README's *Client setup* section for their side.

All the referenced files live in [`deploy/`](../deploy/).

## 1. Prerequisites

- A Linux host with KVM (`/dev/kvm` present) and [microsandbox](https://github.com/superradcompany/microsandbox)
  installed and working: `msb ls` returns cleanly for the user that will run
  the service. The parsers in this repo are pinned against **msb v0.5.2**
  fixtures — after any msb upgrade, re-run the smoke test (step 7).
- Caddy built with your DNS provider module (DNS-01 needs one), e.g.
  `xcaddy build --with github.com/caddy-dns/cloudflare`.
- DNS records for your chosen hostname(s) pointing at the host (e.g. in
  pihole/unbound for the LAN, meshnet names for off-LAN reachability).

## 2. Install the binary

From a GitHub Release (built by `.github/workflows/release.yml`):

```bash
tar -xzf msb-manager_v*_linux_amd64.tar.gz
sudo install -m 0755 msb-manager /usr/local/bin/msb-manager
```

Or build from a clone (any machine — it cross-compiles):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o msb-manager ./cmd/msb-manager
```

## 3. Create the service user

The msb CLI keeps its state under the invoking user's home, so give the
service user a real home directory (outside `/home`, so systemd's
`ProtectHome` hardening can stay on):

```bash
sudo useradd --system --create-home --home-dir /var/lib/msb-manager \
  --shell /usr/sbin/nologin --groups kvm msb-manager
```

microsandbox must be usable *by this user* — install `msb` somewhere on its
PATH (or set `MSB_MANAGER_MSB_PATH`) and verify:

```bash
sudo -u msb-manager msb ls
```

## 4. Configure

```bash
sudo mkdir -p /etc/msb-manager
sudo cp deploy/msb-manager.env.example /etc/msb-manager/env
sudo chmod 0600 /etc/msb-manager/env
```

Edit `/etc/msb-manager/env` and set `MSB_MANAGER_TOKEN` (generate with
`openssl rand -hex 32`). This one token guards the whole control plane —
treat it like a root password for your sandboxes.

## 5. Install and start the service

```bash
sudo cp deploy/msb-manager.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now msb-manager
```

Verify on the host (the server won't listen until its startup volume-lock
reconcile against msb succeeds — it fails closed, so a hung msb shows up
here as a not-yet-listening service, retrying with backoff):

```bash
systemctl status msb-manager
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/sandboxes
```

Logs are structured JSON on stderr: `journalctl -u msb-manager -f`.

## 6. Caddy front door

Merge [`deploy/Caddyfile.example`](../deploy/Caddyfile.example) into your
Caddyfile, replacing the hostnames and DNS provider, then reload Caddy.
From a client machine:

```bash
curl -fsS https://msb.your.domain/readyz
```

Keep msb-manager on loopback; Caddy is the only listener that should ever
be reachable from the network.

## 7. First-boot smoke test

Everything in the test suite mocks the `msb` subprocess boundary; this is
the step that proves the deployed pair (msb-manager + your actual msb build)
agree. From any machine with `msbctl` configured (README, *Client setup*):

```bash
deploy/smoke-test.sh
```

It walks volume → create → inspect/metrics/logs → snapshot → derived
sandbox → teardown, and asserts the two server-enforced invariants (volume
rm refused while held; one running sandbox per volume). Everything it
creates is prefixed `smoke-` and cleaned up on exit.

## 8. Upgrades

```bash
sudo install -m 0755 msb-manager /usr/local/bin/msb-manager
sudo systemctl restart msb-manager
```

The server is stateless (ADR-0003): restarts re-derive everything from
`msb ls`, so an upgrade is just a binary swap. After upgrading **msb**
(rather than msb-manager), re-run the smoke test — the CLI's JSON output is
the wire format between the two.

## Out of scope here

Reaching *into* VMs (`*.msb.…` hostnames → VM host ports) is the separate
data-plane companion — see [vm-access-routing.md](vm-access-routing.md). It
shares the Caddy instance but not this service.
