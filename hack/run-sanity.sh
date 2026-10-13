#!/usr/bin/env bash
# Runs csi-sanity against a locally started driver.
#
# Node tests mount real filesystems, so this needs root:
#     sudo ./hack/run-sanity.sh
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
SOCKET="$WORK/csi.sock"

# sudo resets PATH (and this sudo ignores -E), so a csi-sanity installed by the
# invoking user's `go install` would be invisible below.
if [[ -n "${SUDO_USER:-}" ]]; then
  PATH="$PATH:$(getent passwd "$SUDO_USER" | cut -d: -f6)/go/bin"
fi
command -v csi-sanity >/dev/null || {
  echo "csi-sanity not found on PATH; install it with:" >&2
  echo "  go install github.com/kubernetes-csi/csi-test/v5/cmd/csi-sanity@latest" >&2
  exit 1
}

# In-cluster the node server takes its ID from the downward API; a local run has
# no kubelet, so supply a placeholder.
export NODE_NAME=${NODE_NAME:-sanity-node}

cleanup() {
  [[ -n "${DRIVER_PID:-}" ]] && kill "$DRIVER_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

"$ROOT/bin/csi-demo" \
  --mode=all \
  --endpoint="unix://$SOCKET" \
  --volume-root="$WORK/volumes" \
  --backend=loop &
DRIVER_PID=$!

# Wait for the socket rather than sleeping a fixed amount. A driver that died at
# startup must surface here, or the wait just expires and csi-sanity reports the
# wrong problem.
for _ in $(seq 1 50); do
  [[ -S "$SOCKET" ]] && break
  kill -0 "$DRIVER_PID" 2>/dev/null || { echo "driver exited before creating $SOCKET" >&2; exit 1; }
  sleep 0.1
done

# Do not pre-create the mount and staging paths: csi-sanity makes them itself
# with os.Mkdir and treats an existing directory as a setup failure.
csi-sanity \
  --csi.endpoint="$SOCKET" \
  --csi.mountdir="$WORK/mount" \
  --csi.stagingdir="$WORK/staging" \
  "$@"
