#!/usr/bin/env bash
set -euo pipefail

sidecar_image="${SIDECAR_IMAGE:-vicoslab/ccc-fuse-sidecar:latest}"
app_image="${APP_IMAGE:-your-ccc-app-with-fuse-shims:latest}"
sidecar_name="${SIDECAR_NAME:-ccc-fuse-sidecar}"
control_volume="${CONTROL_VOLUME:-ccc-fuse-control}"
host_mount_root="${HOST_MOUNT_ROOT:-/tmp/ccc-fuse-mounts}"
host_dev_fuse="${HOST_DEV_FUSE:-/dev/fuse}"

if [[ ! -e "$host_dev_fuse" ]]; then
  echo "error: host FUSE device does not exist: $host_dev_fuse" >&2
  exit 1
fi

mkdir -p "$host_mount_root"
docker volume create "$control_volume" >/dev/null

cleanup() {
  docker rm -f "$sidecar_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

docker run -d --rm \
  --name "$sidecar_name" \
  --device "$host_dev_fuse:/dev/fuse:rw" \
  --cap-add SYS_ADMIN \
  --mount "type=volume,src=${control_volume},dst=/run/ccc-fuse-sidecar" \
  --mount "type=bind,src=${host_mount_root},dst=${host_mount_root},bind-propagation=rshared" \
  "$sidecar_image" \
  --allow-prefix "$host_mount_root"

docker run --rm -it \
  --mount "type=volume,src=${control_volume},dst=/run/ccc-fuse-sidecar" \
  --mount "type=bind,src=${host_mount_root},dst=${host_mount_root},bind-propagation=rshared" \
  --device "$host_dev_fuse:/dev/fuse:rw" \
  -e CCC_FUSE_SIDECAR_SOCKET=/run/ccc-fuse-sidecar/fuse.sock \
  "$app_image"
