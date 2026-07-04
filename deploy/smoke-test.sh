#!/usr/bin/env bash
# First-boot smoke test: drives the full control-plane surface against a REAL
# msb through the HTTP API. Every unit test mocks the subprocess boundary by
# design, so this is the check that the parsers and error mapping match the
# msb actually installed on the host. Run it once after deploying, and after
# upgrading msb.
#
# Run from any machine with msbctl on PATH and a resolved target:
#   MSB_MANAGER_URL=https://msb.example TOKEN... deploy/smoke-test.sh
# or with a msbctl config profile already set up. Override the boot image
# with SMOKE_IMAGE (default: alpine).
#
# Everything it creates is prefixed smoke- and removed on exit, pass or fail.

set -euo pipefail

MSBCTL=${MSBCTL:-msbctl}
SMOKE_IMAGE=${SMOKE_IMAGE:-alpine}
SB=smoke-sandbox
DERIVED=smoke-derived
VOL=smoke-volume
SNAP=smoke-snapshot

step() { printf '\n== %s\n' "$*"; }

# expect_refused runs a command that MUST fail (the server enforcing an
# invariant); a success is a test failure.
expect_refused() {
  local msg=$1
  shift
  if "$@" >/dev/null 2>&1; then
    printf 'FAIL: expected the server to refuse: %s\n' "$msg" >&2
    exit 1
  fi
  printf '   refused as expected: %s\n' "$msg"
}

# ensure_running starts a sandbox unless inspect already reports it Running
# (msb may or may not auto-start on create depending on the path taken).
ensure_running() {
  local name=$1
  if ! "$MSBCTL" inspect "$name" -o json | grep -q '"status"[[:space:]]*:[[:space:]]*"Running"'; then
    "$MSBCTL" start "$name"
  fi
}

cleanup() {
  set +e
  step "cleanup (best-effort)"
  "$MSBCTL" stop "$DERIVED" >/dev/null 2>&1
  "$MSBCTL" rm "$DERIVED" >/dev/null 2>&1
  "$MSBCTL" stop "$SB" >/dev/null 2>&1
  "$MSBCTL" rm "$SB" >/dev/null 2>&1
  "$MSBCTL" snapshot rm "$SNAP" >/dev/null 2>&1
  "$MSBCTL" volume rm "$VOL" >/dev/null 2>&1
}
trap cleanup EXIT

step "unauthenticated ops probes"
if [ -n "${MSB_MANAGER_URL:-}" ]; then
  curl -fsS "${MSB_MANAGER_URL%/}/healthz" >/dev/null && echo "   /healthz ok"
  curl -fsS "${MSB_MANAGER_URL%/}/readyz" >/dev/null && echo "   /readyz ok"
else
  echo "   MSB_MANAGER_URL not set (profile-based target?) — skipping curl probes"
fi

step "list sandboxes"
"$MSBCTL" ls

step "create volume $VOL"
"$MSBCTL" volume create "$VOL" --size 1G
"$MSBCTL" volume ls

step "create sandbox $SB from image $SMOKE_IMAGE with $VOL at /workspace"
"$MSBCTL" create -f - <<EOF
name: $SB
image: $SMOKE_IMAGE
memory: 256
volume:
  name: $VOL
  mount: /workspace
EOF
ensure_running "$SB"

step "inspect / metrics / logs"
"$MSBCTL" inspect "$SB"
"$MSBCTL" metrics "$SB"
"$MSBCTL" logs "$SB" --tail 5

step "exec: run a command inside the sandbox"
"$MSBCTL" exec "$SB" -- sh -c 'echo smoke-exec-ok; ls /workspace' | grep -q smoke-exec-ok
echo "   exec output round-tripped"

step "exec: remote exit code is mirrored (CONTEXT open verification #4)"
if "$MSBCTL" exec "$SB" -- sh -c 'exit 7'; then
  echo "FAIL: expected exit code 7 from the remote command" >&2
  exit 1
else
  rc=$?
  if [ "$rc" -eq 7 ]; then
    echo "   exit code 7 mirrored — msb exec propagates exit codes"
  else
    echo "   got exit $rc, not 7 — msb exec does NOT propagate exit codes verbatim; update CONTEXT #4" >&2
  fi
fi

step "invariant: volume rm is refused while a running sandbox holds it (409)"
expect_refused "volume rm $VOL while $SB is running" "$MSBCTL" volume rm "$VOL"

step "stop $SB and snapshot it"
"$MSBCTL" stop "$SB"
"$MSBCTL" snapshot create "$SNAP" --from "$SB"
"$MSBCTL" snapshot ls

step "derive $DERIVED from $SNAP, reusing $VOL"
"$MSBCTL" create -f - <<EOF
name: $DERIVED
snapshot: $SNAP
volume:
  name: $VOL
  mount: /workspace
EOF
ensure_running "$DERIVED"

step "invariant: one running sandbox per volume (409)"
expect_refused "start $SB while $DERIVED holds $VOL" "$MSBCTL" start "$SB"

step "tear down"
"$MSBCTL" stop "$DERIVED"
"$MSBCTL" rm "$DERIVED"
"$MSBCTL" rm "$SB"
"$MSBCTL" snapshot rm "$SNAP"
"$MSBCTL" volume rm "$VOL"

printf '\nPASS: control plane verified against the live msb.\n'
