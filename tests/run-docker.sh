#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for this runtime test" >&2
  exit 127
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "docker compose v2 is required for this runtime test" >&2
  exit 127
fi
if [[ ! -e /dev/fuse ]]; then
  echo "/dev/fuse is required for this runtime test" >&2
  exit 1
fi

mkdir -p tests/run tests/mnt
rm -f tests/run/fuse.sock
created_bind_mount=0

# FUSE mounts created by the sidecar must propagate through the host bind mount
# into the client container. Docker can request rshared propagation on the bind,
# but the source mount on the host also has to be shared. In many CI hosts this
# is already true; otherwise run this script as root or pre-mark tests/mnt's
# parent mount as shared.
if command -v findmnt >/dev/null 2>&1; then
  propagation="$(findmnt -no PROPAGATION -T tests/mnt 2>/dev/null || true)"
  if [[ "$propagation" != shared* && "$propagation" != rshared* ]]; then
    if [[ "$(id -u)" == 0 ]]; then
      if ! findmnt -T tests/mnt -no TARGET | grep -Fqx "$(realpath tests/mnt)"; then
        mount --bind tests/mnt tests/mnt
        created_bind_mount=1
      fi
      mount --make-rshared tests/mnt
    else
      echo "warning: tests/mnt is on a '$propagation' mount; bind propagation may fail unless the host path is shared/rshared" >&2
    fi
  fi
fi

docker build --target sidecar -t ccc-fuse-sidecar:test-sidecar .
docker build --target client -t ccc-fuse-sidecar:test-client .

cleanup_compose() {
  docker compose -f tests/docker-compose.yaml down --volumes --remove-orphans >/dev/null 2>&1 || true
  if [[ "${created_bind_mount:-0}" == 1 ]]; then
    umount tests/mnt >/dev/null 2>&1 || true
  fi
}
trap cleanup_compose EXIT

docker compose -f tests/docker-compose.yaml up --build --abort-on-container-exit --exit-code-from fuse-client
