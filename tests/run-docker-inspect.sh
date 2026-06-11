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
if [[ ! -S /var/run/docker.sock ]]; then
  echo "/var/run/docker.sock is required for Docker-inspect translation test" >&2
  exit 1
fi

mkdir -p tests/run-inspect tests/inspect-storage
rm -f tests/run-inspect/fuse.sock
rm -rf tests/inspect-storage/rar

created_bind_mount=0

# Docker-inspect translation mounts the same host directory through two textual
# paths:
#   client:  /storage/user/rar
#   sidecar: /host${PWD}/tests/inspect-storage/rar
# Both binds need compatible propagation so the sidecar-created FUSE mount is
# visible inside the client container.
if command -v findmnt >/dev/null 2>&1; then
  propagation="$(findmnt -no PROPAGATION -T tests/inspect-storage 2>/dev/null || true)"
  if [[ "$propagation" != shared* && "$propagation" != rshared* ]]; then
    if [[ "$(id -u)" == 0 ]]; then
      if ! findmnt -T tests/inspect-storage -no TARGET | grep -Fqx "$(realpath tests/inspect-storage)"; then
        mount --bind tests/inspect-storage tests/inspect-storage
        created_bind_mount=1
      fi
      mount --make-rshared tests/inspect-storage
    else
      echo "warning: tests/inspect-storage is on a '$propagation' mount; bind propagation may fail unless the host path is shared/rshared" >&2
    fi
  fi
fi

export CCC_FUSE_INSPECT_CLIENT_NAME="ccc-fuse-inspect-client-$$"

docker build --target sidecar -t ccc-fuse-sidecar:test-sidecar .
docker build --target client -t ccc-fuse-sidecar:test-client .

cleanup_compose() {
  docker compose -f tests/docker-compose-docker-inspect.yaml down --volumes --remove-orphans >/dev/null 2>&1 || true
  if [[ "${created_bind_mount:-0}" == 1 ]]; then
    umount tests/inspect-storage >/dev/null 2>&1 || true
  fi
}
trap cleanup_compose EXIT

docker compose -f tests/docker-compose-docker-inspect.yaml up --build --abort-on-container-exit --exit-code-from fuse-client
