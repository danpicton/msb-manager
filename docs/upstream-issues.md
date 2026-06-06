# Upstream issues — microsandbox

Findings against **`msb` v0.5.2** while building msb-manager. Each is a candidate
to file against [superradcompany/microsandbox](https://github.com/superradcompany/microsandbox).

---

## 1. `msb inspect` echoes secret values in plaintext

`msb inspect --format json` includes the raw secret value under
`config.network.secrets.secrets[].value`. Anyone who can call `inspect` can
read every secret of every sandbox — including from a non-running supervisor
process by reading the JSON output.

### Repro

```bash
msb create -n leak --secret CANARY=ohno@example.com alpine
msb inspect leak --format json | jq '.config.network.secrets.secrets[].value'
# "ohno"
msb rm -f leak
```

### Expected

The secret value field is redacted (empty string, `null`, or the field
omitted). The surrounding structure — `placeholder`, `env_var`,
`allowed_hosts`, `injection`, `require_tls_identity` — stays, because it's
useful for debugging without exposing the secret.

### Actual

Plaintext value present in inspect output.

### msb-manager workaround

`internal/msb/parse.go` deliberately does not extract the `config.network`
subtree, so `GET /sandboxes/{name}` never surfaces it.
`TestParseInspect_DoesNotLeakSecretValue` is the regression guard.

---

## 2. `msb volume rm` succeeds while volume is mounted by a running sandbox

`msb volume rm <name>` returns exit 0 even when a running sandbox is currently
mounting the volume. The volume disappears from `msb volume ls` but the
sandbox's mount config still references it — a self-inconsistent state.

### Repro

```bash
msb volume create --size 1G inuse
msb create -n holder -v inuse:/workspace alpine
msb volume rm inuse              # succeeds (exit 0)
msb volume ls                    # 'inuse' gone
msb inspect holder --format json | jq '.config.mounts'
# 'inuse' still referenced in holder's mount config
```

### Expected

`msb volume rm` returns a non-zero exit with an `error: volume in use: ...`
message (same shape as `error: sandbox still running: ...` on `msb rm`),
analogous to Docker / Podman / libvirt / k8s PVC semantics.

### Actual

Silent success; volume registry and running-sandbox mount config diverge.

### Open questions for the fix

- After `volume rm` with the sandbox still running, can the sandbox stop and
  restart? Does the mount fail, recreate empty, or silently break?
- If a new volume is created with the same name, is it a fresh volume or does
  it somehow rejoin the orphaned data?

### msb-manager workaround

`DELETE /volumes/{name}` consults an in-memory VolumeLock and returns
**409 Conflict** when a running sandbox holds the claim, naming the holder.
See `internal/server/volumes.go`.

---

## 3. `msb snapshot inspect` has no `--format json`

`msb snapshot ls`, `msb ls`, `msb volume ls`, and `msb inspect` all accept
`--format json`. `msb snapshot inspect` does not — and labels (used for the
`msb.parent` lineage relation, among others) appear to be only surfaced
through `inspect`, not through `ls`.

### Repro

```bash
msb snapshot inspect --help 2>&1 | grep format
# no --format option
msb snapshot inspect probe-snap --format json
# error: unexpected argument '--format' found
```

### Expected

`msb snapshot inspect --format json <snap>` returns a structured JSON
document including the snapshot's labels — at minimum a `labels` map
keyed by name.

### Actual

No JSON output; labels are not present in `msb snapshot ls --format json`
either.

### msb-manager workaround

`GET /snapshots/{name}` is deliberately **not implemented** until a JSON
inspect surface lands; parsing the text output would be brittle and a
violation of ADR-0002's "isolate parsing in one adapter module" guidance.
`GET /snapshots` returns the populated `ls --format json` rows minus labels.
