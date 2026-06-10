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

When relying on propagation, use an `rshared` bind mount for the target tree in
both the sidecar and app containers.

Pass `/dev/fuse` into the app container as a device mapping when libfuse may
probe/open it before invoking `fusermount3`, but do not pass `CAP_SYS_ADMIN` or
privileged mode to the app container. The privileged mount authority remains in
the sidecar.

See:

- [examples/docker-run.sh](examples/docker-run.sh)
- [examples/docker-compose.yml](examples/docker-compose.yml)

## CCC Integration

CCC containers need two pieces to let normal libfuse tools use the sidecar
without giving app containers mount authority:

1. **Image piece:** CCC base images include the app-side `fusermount3` shim from
   the published `ccc-fuse-sidecar` client image.
2. **Runtime piece:** `ccc-inventory` maps `/dev/fuse` into compute containers
   as a plain Docker device, while keeping the app container without
   `CAP_SYS_ADMIN` and without privileged mode.

The privileged FUSE mount broker remains in the separate sidecar container.

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

`ccc-inventory` maps `/dev/fuse` into compute containers by default:

```yaml
compute_container_fuse_enabled: True
compute_container_fuse_device: /dev/fuse
compute_container_fuse_device_permissions: rw
compute_container_fuse_capabilities: []
```

A container can opt out when needed:

```yaml
my-container:
  DISABLE_FUSE: true
```

If a site disables the global default, a specific container can opt back in with:

```yaml
my-container:
  ENABLE_FUSE: true
```

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
bash -n examples/*.sh
```

Static binary sanity check for the client shim:

```bash
CGO_ENABLED=0 GOOS=linux go build -o /tmp/fusermount3 ./cmd/fusermount3
ldd /tmp/fusermount3
# not a dynamic executable
```

Runtime FUSE validation still requires a Docker host configured with `/dev/fuse`,
`CAP_SYS_ADMIN`, and suitable mount propagation.
