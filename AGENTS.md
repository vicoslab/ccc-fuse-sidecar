# AGENTS.md — CCC FUSE Sidecar

## Mission

Implement a Docker-oriented equivalent of PFN's `meta-fuse-csi-plugin` for CCC containers. The goal is to let normal CCC application containers run libfuse-based programs without `CAP_SYS_ADMIN` or privileged mode, while a trusted privileged sidecar/container performs the privileged `open("/dev/fuse")` and `mount(2)` operations and passes the FUSE fd back to the unprivileged process. CCC app containers may still receive `/dev/fuse:/dev/fuse:rw` as a plain device mapping so libfuse reaches the `fusermount3` fallback path.

Reference material:

- Upstream: https://github.com/pfnet-research/meta-fuse-csi-plugin
- Blog: https://tech.preferred.jp/en/blog/meta-fuse-csi-plugin/

## Required architecture

1. **Privileged Docker sidecar daemon**
   - Runs with `/dev/fuse` and `CAP_SYS_ADMIN` (or privileged) only in the sidecar, not in CCC app containers.
   - Listens on a Unix-domain socket in a shared control directory.
   - For mount requests: validates the requested mountpoint is under an allowed prefix, opens `/dev/fuse`, calls `mount(2)` with `fstype=fuse` and `fd=<fusefd>` plus safe options, then passes the fd to the client over SCM_RIGHTS.
   - For unmount requests: validates path and performs `unmount(2)`/lazy unmount as requested.
   - Emits clear logs and error responses. Do not require Kubernetes/CSI packages.

2. **Transparent client shims for CCC app images**
   - Provide binaries/symlinks named exactly like standard FUSE helpers so apps do not know about the sidecar:
     - `fusermount3`
     - `fusermount`
     - `fusemount3`
     - `fusemount`
   - `fusermount3` behavior must support the libfuse protocol: read `_FUSE_COMMFD`, connect to sidecar UDS, receive `/dev/fuse` fd over SCM_RIGHTS, and send it back to libfuse over `_FUSE_COMMFD`.
   - `fusermount`/`fusemount*` should use the same implementation or symlink to it.
   - Keep CLI compatible for common options: `-o/--options`, `-u/--unmount`, `-z/--lazy`, `-q/--quiet`, `-V/--version`, `-h/--help`. Unknown options should fail clearly rather than silently doing the wrong thing.
   - The socket path is configured by `CCC_FUSE_SIDECAR_SOCKET`; default `/run/ccc-fuse-sidecar/fuse.sock`.
   - This must be transparent for any libfuse-based app that shells out to `fusermount`/`fusermount3`.

3. **Docker artifacts**
   - Multi-stage Dockerfile(s) to build fully static Go binaries with `CGO_ENABLED=0` and produce:
     - sidecar image containing the privileged daemon;
     - client image/stage containing only the `fusermount3` shim aliases for copying into CCC app images.
   - Provide compose/run examples showing required Docker flags: shared control volume, shared/bind mount root with propagation where needed, sidecar has `/dev/fuse` + `CAP_SYS_ADMIN`, app has the shim, socket volume, and `/dev/fuse:/dev/fuse:rw` but no app-side mount authority.

4. **CCC integration**
   - Document the detailed CCC integration contract in this repository.
   - The CCC repository should carry only a short user-facing README note and consume the published client image.
   - The CCC image-side tool must have the transparent standard names above.
   - Keep existing image behavior unchanged unless the user explicitly asked for a default-breaking deployment change.

## Docker/mount namespace notes

Unlike CSI/Kubernetes, plain Docker containers do not automatically share a mount namespace. A Docker deployment must either:

- mount the target tree through a host bind with suitable shared/rshared propagation into both the sidecar and app containers; or
- run the privileged helper in the namespace where the resulting mount must be visible.

Document this honestly and make examples use explicit `bind-propagation=rshared` when they rely on propagation.

## Implementation constraints

- Prefer Go. Keep dependencies small. Do not vendor the whole CSI plugin.
- Preserve Apache-2.0 attribution for any code derived from PFN/meta-fuse-csi-plugin.
- Tests must not require real FUSE or Docker; include unit/protocol tests using Unix sockets and temporary files where possible.
- Use strict validation around mountpoint paths and socket paths. Never accept arbitrary host paths without an allowed prefix.

## Validation

Run and record:

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build ./cmd/...
bash -n examples/*.sh
```

If Docker/FUSE runtime validation is unavailable, state that clearly and rely on static/protocol tests.
