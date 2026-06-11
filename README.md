# CCC FUSE Sidecar

Docker-oriented FUSE fd broker for CCC containers. A privileged sidecar owns
`/dev/fuse` and `CAP_SYS_ADMIN`; normal app containers run standard
libfuse-based programs through replacement `fusermount*` shims, without
receiving mount authority themselves.

The implementation follows the fd-passing idea used by PFN's
`meta-fuse-csi-plugin`, but removes Kubernetes/CSI dependencies.

## Components

This repository intentionally ships only two functional components:

- `ccc-fuse-sidecar`: privileged daemon. It listens on a shared Unix socket,
  validates mountpoints against allowed prefixes, opens `/dev/fuse`, performs
  `mount(2)` with `fstype=fuse` and `fd=<fusefd>`, then sends the fd to the app
  over `SCM_RIGHTS`.
- `fusermount3`: transparent libfuse helper shim. The client image also exposes
  the same binary as `fusermount`, `fusemount3`, and `fusemount` for compatibility
  with helper lookup paths used by libfuse and CCC images.

There is no separate starter command and no `/dev/fuse` placeholder helper. CCC's
supported runtime path is: map `/dev/fuse` into the app container for libfuse
compatibility, withhold `CAP_SYS_ADMIN`, and let libfuse fall back to the
`fusermount3` shim.

## Static Build Requirement

All published binaries must be built with `CGO_ENABLED=0`. CCC copies the
`fusermount3` client binary from the `client` image into Ubuntu-based CCC images;
it must not depend on Alpine `musl`, glibc, a dynamic loader, or any other shared
objects from the Go builder image.

Use this pattern for local checks and release builds:

```bash
CGO_ENABLED=0 GOOS=linux go test ./...
CGO_ENABLED=0 GOOS=linux go build ./cmd/...
```

A valid client binary should report as static, for example:

```bash
ldd ./fusermount3
# not a dynamic executable
```

## Build

Requirements:

- Go 1.22 or newer
- Docker or another OCI image builder, if you want to build container images

Run the local checks and build the binaries:

```bash
CGO_ENABLED=0 GOOS=linux go test ./...
CGO_ENABLED=0 GOOS=linux go build ./cmd/...
```

Build the sidecar and client images:

```bash
docker build --target sidecar -t vicoslab/ccc-fuse-sidecar:latest .
docker build --target client -t vicoslab/ccc-fuse-sidecar:client-latest .
```

Both image targets are `scratch` images containing only static Go binaries.

The release workflow publishes two images from this repository:

```text
# privileged sidecar runtime image
vicoslab/ccc-fuse-sidecar:<release-tag>
vicoslab/ccc-fuse-sidecar:latest

# client-only shim image consumed by CCC image builds
vicoslab/ccc-fuse-sidecar:client-<release-tag>
vicoslab/ccc-fuse-sidecar:client-latest
```

There are no separate `sidecar-*` tags; the unprefixed tags are the sidecar
runtime image. The `client-*` prefix is only needed to distinguish the shim-only
image from the runtime image in the same Docker Hub repository.

The `client` Docker target contains one static helper binary plus portable helper
aliases under `/usr/local/bin` so downstream images can copy them into their own
layout:

```text
/usr/local/bin/fusermount3
/usr/local/bin/fusermount  -> fusermount3
/usr/local/bin/fusemount3  -> fusermount3
/usr/local/bin/fusemount   -> fusermount3
```

CCC base images copy only `fusermount3` into `/opt/ccc-fuse-sidecar/bin`, replace
`fusermount*`/`fusemount*` in `/usr/local/bin`, `/usr/bin`, and `/bin`, and
reassert those links during CCC startup because later image layers or packages
can overwrite them. This covers libfuse builds that find helpers through `PATH`
and builds that exec a compiled-in path such as `/bin/fusermount3`.

## Daemon

### Legacy Direct-Prefix Mode

This is the default mode and preserves the original behavior. The app-visible
mountpoint path is validated and used directly inside the sidecar mount
namespace, so the sidecar and app containers must see the target tree at the
same path.

```bash
ccc-fuse-sidecar \
  --socket /run/ccc-fuse-sidecar/fuse.sock \
  --allow-prefix /mnt/ccc-fuse \
  --socket-mode 0666
```

Environment and flags:

- `CCC_FUSE_ALLOWED_PREFIXES`: colon- or comma-separated fallback for repeated
  `--allow-prefix` flags.
- `--allow-prefix`: absolute mountpoint prefix. `/` is rejected.
- `--socket`: Unix socket path. Default `/run/ccc-fuse-sidecar/fuse.sock`.
- `--socket-mode`: socket permissions, default `0666`.
- `--dev-fuse`: FUSE device path, default `/dev/fuse`.

The daemon rejects relative paths, socket paths too long for Linux `sockaddr_un`,
mountpoints outside allowed prefixes, non-directory mountpoints, and symlink
escapes out of the allowed tree.

### Docker-Inspect Translation Mode

Set `--docker-socket` to opt in. In this mode the client shim sends
`CONTAINER_NAME` and `HOSTNAME` hints with each request. The sidecar inspects the
claimed Docker container, finds the longest bind mount whose `Destination`
contains the requested client path, translates that path through the bind
mount's `Source`, maps the host path under `--host-root`, and then runs the same
mountpoint validation on the translated sidecar path.

```bash
ccc-fuse-sidecar \
  --socket /run/ccc-fuse-sidecar/fuse.sock \
  --docker-socket /var/run/docker.sock \
  --host-root /host \
  --allow-client-prefix /storage/user \
  --allow-host-prefix /storage \
  --require-container-label ccc.fuse=enabled
```

Additional flags and environment:

- `--docker-socket`: Docker Engine Unix socket. Accepts `/var/run/docker.sock`
  or `unix:///var/run/docker.sock`. Translation mode is enabled when this is
  set.
- `--host-root`: sidecar-visible root where host paths are mounted, for example
  `/host`.
- `--allow-client-prefix`: client-visible path prefix accepted from app
  containers. May be repeated. `/` is rejected.
- `--allow-host-prefix`: host path prefix accepted after Docker bind-mount
  translation. May be repeated. `/` is rejected.
- `CCC_FUSE_ALLOWED_HOST_PREFIXES`: colon- or comma-separated fallback for
  `--allow-host-prefix`.
- `--require-container-label key=value`: optional label requirement on the
  inspected container. May be repeated.

Translation mode supports only Docker bind mounts in this pass. Docker volumes
and tmpfs mounts are rejected so the sidecar does not guess at host paths.

Security model: `CONTAINER_NAME` is an identity hint, not strong peer
authentication. Label checks are useful authorization hardening but do not prove
that the socket peer is the claimed container. The sidecar is a trusted
component: Docker socket access plus host-root visibility is effectively host
powerful. Future work can add socket peer PID and cgroup verification.

## Shim

`fusermount3` supports the common libfuse helper interface:

```bash
fusermount3 -o fsname=demo,allow_other /mnt/ccc-fuse/demo
fusermount3 -u -z /mnt/ccc-fuse/demo
```

Supported options:

- `-o`, `--options`
- `-u`, `--unmount`
- `-z`, `--lazy`
- `-q`, `--quiet`
- `-V`, `--version`
- `-h`, `--help`

Unknown command-line options fail clearly. The shim reads `_FUSE_COMMFD`,
connects to `CCC_FUSE_SIDECAR_SOCKET` or the default socket, asks the sidecar to
mount, receives the FUSE fd over `SCM_RIGHTS`, and forwards it to libfuse over
`_FUSE_COMMFD`.

For Docker-inspect translation mode, set `CONTAINER_NAME` in the app container
to the Docker container name that the sidecar is allowed to inspect. The shim
also sends `HOSTNAME` as an optional container id hint for diagnostics.

## App-Side `/dev/fuse`

Some libfuse versions invoke `fusermount3` only after `open("/dev/fuse")`
succeeds and a direct `mount(2)` attempt fails with `EPERM`. The preferred Docker
runtime contract is therefore to pass `/dev/fuse` into the app container as a
plain device mapping, while still withholding mount authority:

```bash
docker run ... \
  --device /dev/fuse:/dev/fuse:rw \
  your-ccc-app-with-fuse-shims:latest
```

Do not add app-side `CAP_SYS_ADMIN`, and do not run the app container as
`--privileged`. The sidecar container remains the only container with mount
capability.

## Docker Runtime Notes

Plain Docker containers do not automatically share a mount namespace. A Docker
deployment must either mount the target tree through a host bind with suitable
shared propagation into both containers, or run the privileged helper in the
namespace where the resulting mount must be visible.

When relying on propagation in legacy direct-prefix mode, use an `rshared` bind
mount for the target tree in both the sidecar and app containers at the same
container path. For Docker-inspect translation mode, the textual paths may differ
(for example app `/storage/user` and sidecar `/host/storage/user`), but both must
be views of the same host-backed tree and the app storage bind must use compatible
shared propagation so the sidecar-created mount becomes visible in the app.

Docker-inspect translation mode also requires the sidecar to see the host paths
reported by Docker inspect under `--host-root`; for CCC-style storage this can be
a narrower bind such as `/storage:/host/storage:rshared` rather than a full host
root bind. Mounting `/var/run/docker.sock` gives the sidecar Docker API access and
should be treated as host-powerful even when the bind is marked read-only.

Pass `/dev/fuse` into the app container as a device mapping when libfuse may
probe/open it before invoking `fusermount3`, but do not pass `CAP_SYS_ADMIN` or
privileged mode to the app container. The privileged mount authority remains in
the sidecar.

See:

- [examples/docker-run.sh](examples/docker-run.sh)
- [examples/docker-compose.yml](examples/docker-compose.yml)
- [examples/docker-compose-docker-inspect.yml](examples/docker-compose-docker-inspect.yml)

## CCC Integration

CCC containers need two pieces to let normal libfuse tools use the sidecar
without giving app containers mount authority:

1. **Image piece:** CCC base images include the app-side `fusermount3` shim from
   the published `ccc-fuse-sidecar` client image.
2. **Runtime piece:** `ccc-inventory` maps `/dev/fuse` into compute containers
   as a plain Docker device, while keeping the app container without
   `CAP_SYS_ADMIN` and without privileged mode.

The privileged FUSE mount broker remains in the separate sidecar container.

For BranchFS-backed agent protection, this sidecar is only the FUSE/mount
plumbing layer. Branch/session creation, path-policy auto-commit, human review,
and rollback/commit decisions should live in a separate trusted BranchFS agent
supervisor. In the combined CCC/BranchFS workspace, see
`../CCC_AGENT_BRANCHFS_PROTECTION_REVIEW_DESIGN.md` for that higher-level design.

### CCC base image contract

CCC base images consume a published client image selected by:

```dockerfile
ARG CCC_FUSE_CLIENT_IMAGE="vicoslab/ccc-fuse-sidecar:client-latest"
```

Release builds should pass the matching tag:

```text
CCC_FUSE_CLIENT_IMAGE=vicoslab/ccc-fuse-sidecar:client-<release-tag>
```

The CCC image copies exactly one required client binary:

```text
/usr/local/bin/fusermount3  ->  /opt/ccc-fuse-sidecar/bin/fusermount3
```

It then links common helper names to that one binary in all locations that
libfuse or users commonly probe:

```text
/usr/local/bin/fusermount3  /usr/local/bin/fusermount  /usr/local/bin/fusemount3  /usr/local/bin/fusemount
/usr/bin/fusermount3        /usr/bin/fusermount        /usr/bin/fusemount3        /usr/bin/fusemount
/bin/fusermount3            /bin/fusermount            /bin/fusemount3            /bin/fusemount
```

The CCC image sets the default control socket path:

```dockerfile
ENV CCC_FUSE_SIDECAR_SOCKET=/run/ccc-fuse-sidecar/fuse.sock
```

and creates `/run/ccc-fuse-sidecar`.

### CCC startup relink hook

CCC users can install apt packages at container startup. Some packages may
install or overwrite `/bin/fusermount3`, `/usr/bin/fusermount3`, or related
helper names after the Docker image was built.

To keep the shim active, CCC includes:

```text
/etc/runit_init.d/05_ccc_fuse_client_shims.sh
```

The hook runs after `00_aptget.sh`, reasserts the helper symlinks, and recreates
`/run/ccc-fuse-sidecar` if needed. It does not create `/dev/fuse` and does not
start or embed the sidecar.

Controls:

```bash
CCC_FUSE_RELINK_HELPERS=1
CCC_FUSE_CLIENT_BIN_DIR=/opt/ccc-fuse-sidecar/bin
CCC_FUSE_HELPER_DIRS=/usr/local/bin:/usr/bin:/bin
CCC_FUSE_SOCKET_DIR=/run/ccc-fuse-sidecar
CCC_FUSE_SIDECAR_SOCKET=/run/ccc-fuse-sidecar/fuse.sock
```

### CCC runtime contract

For a FUSE-aware CCC app container, mount the shared control volume at
`/run/ccc-fuse-sidecar` in both the sidecar and app containers, and give the app
container only this FUSE device mapping:

```bash
--device /dev/fuse:/dev/fuse:rw
```

Do not provide app-side:

```bash
--cap-add SYS_ADMIN
--privileged
```

`ccc-inventory` keeps FUSE sidecar support disabled by default so existing
production containers are not recreated. A specific container opts in through its
custom settings file:

```yaml
my-container:
  ENABLE_FUSE: true
```

When enabled, `ccc-inventory` starts a matching
`vicoslab/ccc-fuse-sidecar` helper container, maps `/dev/fuse` plus
`/run/ccc-fuse-sidecar` into the app container through the existing volume-mount
list, and keeps the app container without `SYS_ADMIN` and without
`--privileged`.

### CCC request flow

1. A tool inside the CCC container uses libfuse normally.
2. libfuse may open `/dev/fuse` first; this succeeds because `ccc-inventory`
   mapped the device into the app container.
3. libfuse attempts a direct mount. Because the app container has no
   `CAP_SYS_ADMIN`, the mount fails with `EPERM`.
4. libfuse invokes `fusermount3`.
5. CCC's `fusermount3` shim contacts the `ccc-fuse-sidecar` Unix socket, asks the
   privileged sidecar to perform the mount, receives the FUSE fd by
   `SCM_RIGHTS`, and forwards that fd back to libfuse using the normal helper
   protocol.

The app container can therefore use FUSE without receiving broad mount
authority. Only the sidecar container should get `/dev/fuse` plus
`CAP_SYS_ADMIN` or an equivalent site-specific privileged policy.

## Validation

Tests do not require real FUSE or Docker. They use temporary Unix sockets, fake
files, and injected syscall functions.

Expected local checks:

```bash
CGO_ENABLED=0 GOOS=linux go test ./...
CGO_ENABLED=0 GOOS=linux go build ./cmd/...
bash -n examples/*.sh tests/*.sh
```

Static binary sanity check for the client shim:

```bash
CGO_ENABLED=0 GOOS=linux go build -o /tmp/fusermount3 ./cmd/fusermount3
ldd /tmp/fusermount3
# not a dynamic executable
```

Runtime FUSE validation still requires a Docker host configured with `/dev/fuse`,
`CAP_SYS_ADMIN`, Docker Compose v2, and suitable mount propagation. Run both
runtime smoke tests when those prerequisites are available:

```bash
# Legacy direct-prefix mode: sidecar and client both use /mnt/ccc-fuse.
tests/run-docker.sh

# Docker-inspect translation mode: client uses /storage/user, while the sidecar
# translates through Docker inspect and mounts under /host<host-source>.
tests/run-docker-inspect.sh
```

Both tests run the same archivemount read/write/flush check: mount a tar archive,
read `hello.txt`, write `created.txt`, force a flush, unmount normally, and verify
the written file persisted into the archive.
