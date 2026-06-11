#!/usr/bin/env bash
set -Eeuo pipefail

mountpoint=${CCC_FUSE_TEST_MOUNTPOINT:-/mnt/ccc-fuse/rar}
socket=${CCC_FUSE_SIDECAR_SOCKET:-/run/ccc-fuse-sidecar/fuse.sock}
archive=${CCC_FUSE_TEST_ARCHIVE:-/tmp/test.tar}
write_payload=${CCC_FUSE_TEST_WRITE_PAYLOAD:-created through ccc-fuse-sidecar}
mount_active=0

log() {
  printf '[ccc-fuse-smoke] %s\n' "$*"
}

step() {
  log "STEP: $*"
}

pass() {
  log "PASS: $*"
}

fail() {
  local status=$?
  log "FAIL: command failed at line ${BASH_LINENO[0]}: ${BASH_COMMAND}"
  exit "$status"
}

cleanup() {
  local status=$?
  if [[ "$mount_active" == 1 ]]; then
    log "CLEANUP: lazy-unmounting ${mountpoint}"
    fusermount3 -u -z "$mountpoint" >/dev/null 2>&1 || true
  fi
  exit "$status"
}

trap fail ERR
trap cleanup EXIT

step "wait for sidecar socket ${socket}"
for _ in $(seq 1 100); do
  [[ -S "$socket" ]] && break
  sleep 0.1
done
[[ -S "$socket" ]]
pass "sidecar socket is ready"

step "verify client prerequisites"
mkdir -p "$mountpoint"
[[ -e /dev/fuse ]]
command -v archivemount >/dev/null
command -v fusermount3 >/dev/null
[[ "$(readlink -f /usr/bin/fusermount3)" == /opt/ccc-fuse-sidecar/bin/fusermount3 ]]
pass "archivemount, /dev/fuse, and fusermount3 shim are present"

step "mount archive ${archive} at ${mountpoint}"
archivemount "$archive" "$mountpoint"
mount_active=1
pass "archive mounted"

step "read existing file through FUSE"
ls -la "$mountpoint"
[[ "$(cat "${mountpoint}/hello.txt")" == hello ]]
pass "existing archive content is readable"

step "write new file through FUSE"
printf '%s\n' "$write_payload" >"${mountpoint}/created.txt"
sync "${mountpoint}/created.txt" 2>/dev/null || sync
[[ "$(cat "${mountpoint}/created.txt")" == "$write_payload" ]]
pass "new file is writable and immediately readable"

step "unmount archive to flush archivemount changes"
fusermount3 -u "$mountpoint"
mount_active=0
pass "archive unmounted"

step "verify written file persisted into archive after unmount"
rm -rf /tmp/archive-check
mkdir -p /tmp/archive-check
tar -xf "$archive" -C /tmp/archive-check
[[ "$(cat /tmp/archive-check/created.txt)" == "$write_payload" ]]
pass "written file persisted in archive"

trap - EXIT
log "PASS: ccc-fuse-sidecar docker archivemount read/write/flush test passed"
