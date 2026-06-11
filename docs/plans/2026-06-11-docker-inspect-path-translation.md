# Docker Inspect Path Translation Implementation Plan

> **For Hermes:** Use Codex CLI to implement this plan task-by-task, then independently verify the resulting diff and tests.

**Goal:** Let one shared `ccc-fuse-sidecar` socket safely serve multiple CCC client containers whose client-visible paths such as `/storage/user` may be backed by different host bind mounts.

**Architecture:** The client shim sends a client identity hint from `CONTAINER_NAME` with each mount/unmount request. The privileged sidecar treats that value as an identity hint, uses Docker inspect through `/var/run/docker.sock` to obtain the claimed container's bind mounts, translates the requested client path through Docker's `Destination -> Source` mount table, maps the host source path into the sidecar through a configured `--host-root`, validates allowlists and symlink escapes, then performs mount/unmount at the translated sidecar path. The legacy same-namespace `--allow-prefix` mode stays as the default so current deployments and tests keep working.

**Tech Stack:** Go 1.22 stdlib only, Docker Engine HTTP API over Unix socket, existing Unix socket JSON protocol and SCM_RIGHTS fd passing.

---

## Requirements

### Functional requirements

1. Preserve current behavior by default:
   - `ccc-fuse-sidecar --allow-prefix /mnt/ccc-fuse` continues to validate and mount paths directly inside the sidecar namespace.
   - Existing unit tests and Docker smoke tests continue to pass.
2. Add an opt-in Docker-inspect translation mode:
   - sidecar flag: `--docker-socket /var/run/docker.sock` or `unix:///var/run/docker.sock`;
   - sidecar flag: `--host-root /host`;
   - repeated sidecar flag: `--allow-client-prefix /storage/user`;
   - repeated sidecar flag or env: `--allow-host-prefix /storage`;
   - optional sidecar flag: `--require-container-label key=value`;
   - optional sidecar flag: `--trust-container-name-env` for development mode if implemented, default true for first pass is acceptable only if README labels it as not strong authentication.
3. Extend protocol requests with optional identity fields:
   - `container_name` from `CONTAINER_NAME`;
   - `container_id` or `container_id_hint` from `HOSTNAME` if available.
4. Extend the client shim:
   - read `CONTAINER_NAME` from environment;
   - read `HOSTNAME` as optional ID hint;
   - include those fields in both mount and unmount requests;
   - debug logs should print the container hint when present.
5. In translation mode, sidecar must:
   - require non-empty `container_name`;
   - call Docker inspect for that container;
   - optionally require configured labels on the inspected container;
   - choose the longest bind-mount `Destination` prefix that contains the requested client mountpoint;
   - initially support only Docker `Type=bind` mounts;
   - compute `hostPath = Source + relative(requestPath, Destination)`;
   - require `requestPath` under an allowed client prefix;
   - require `hostPath` under an allowed host prefix;
   - compute `sidecarPath = hostRoot + hostPath` using safe path joining;
   - validate and pin the translated sidecar path using the existing `OpenValidatedMountpoint`/symlink-escape pattern;
   - mount/unmount using the translated sidecar path, while logging both client and sidecar paths.
6. Add unit tests for translation without real Docker/FUSE:
   - fake Docker inspector returns inspect JSON/structs;
   - longest-prefix mount selection;
   - rejects missing container name in translation mode;
   - rejects requested client paths outside allowed client prefixes;
   - rejects host paths outside allowed host prefixes;
   - rejects non-bind mounts;
   - translates mount and unmount targets before invoking injected `Mount`/`Unmount` funcs.
7. Add docs:
   - README section explaining legacy direct-prefix mode vs Docker-inspect translation mode;
   - document security model: `CONTAINER_NAME` is a hint, not strong auth unless peer/container verification is added;
   - document required Docker wiring: sidecar needs Docker socket, host path visibility under `--host-root`, and mount propagation;
   - include a concise Compose example.

### Security requirements

1. Do not trust arbitrary sidecar paths from the client.
2. Do not let a client choose the translated sidecar prefix directly.
3. Keep `/` rejected for allow prefixes.
4. Require explicit allowed client prefixes and host prefixes in translation mode.
5. Only accept bind mounts for first implementation; reject Docker volumes/tmpfs until deliberately designed.
6. Use longest-prefix matching with path-boundary checks; `/storage/user2` must not match `/storage/user`.
7. Existing symlink/pinned-fd validation must still run on the translated sidecar target.
8. Docker socket access is host-root-equivalent; docs must explicitly say the sidecar is a trusted component.
9. Label checks are authorization hardening, not full proof of peer identity. Peer PID/cgroup verification can be future work.

### Non-goals for this pass

1. No automatic peer PID/cgroup verification.
2. No support for Docker named volumes or tmpfs path translation.
3. No Kubernetes/CRI support.
4. No dynamic per-client sockets.
5. No changes that require app containers to receive `SYS_ADMIN` or `--privileged`.

---

## Task 1: Extend protocol request identity fields

**Objective:** Add optional container identity fields while preserving backward compatibility.

**Files:**
- Modify: `internal/protocol/protocol.go`
- Test: existing protocol/client tests

**Steps:**
1. Add fields to `protocol.Request`:
   ```go
   ContainerName   string `json:"container_name,omitempty"`
   ContainerIDHint string `json:"container_id_hint,omitempty"`
   ```
2. Add env consts:
   ```go
   EnvContainerName = "CONTAINER_NAME"
   EnvHostname      = "HOSTNAME"
   ```
3. Run `CGO_ENABLED=0 go test ./...` and verify existing tests pass.

---

## Task 2: Send container hints from the client shim

**Objective:** Include `CONTAINER_NAME` and `HOSTNAME` in mount and unmount requests.

**Files:**
- Modify: `internal/client/client.go`
- Modify: `internal/client/client_test.go`

**Steps:**
1. Add helper:
   ```go
   func containerIdentityFromEnv(getenv func(string) string) (name, idHint string)
   ```
   Trim whitespace. Empty is allowed for legacy mode.
2. Pass the values into `RequestMount` and `requestUnmount` or update those functions to accept them.
3. Include fields in JSON requests.
4. Extend tests to assert that when env contains:
   ```go
   CONTAINER_NAME=ccc-demo
   HOSTNAME=abc123
   ```
   the sidecar receives those fields.
5. Update debug logging to include `container=... idHint=...` when present.

---

## Task 3: Add Docker inspect client abstraction

**Objective:** Add a small stdlib Docker inspect client that can be faked in tests.

**Files:**
- Create: `internal/sidecar/docker.go`
- Create: `internal/sidecar/docker_test.go`

**Data structures:**
```go
type DockerInspector interface {
    InspectContainer(ctx context.Context, name string) (ContainerInspect, error)
}

type ContainerInspect struct {
    ID     string
    Name   string
    Config struct {
        Labels map[string]string `json:"Labels"`
    } `json:"Config"`
    Mounts []DockerMount `json:"Mounts"`
}

type DockerMount struct {
    Type        string `json:"Type"`
    Source      string `json:"Source"`
    Destination string `json:"Destination"`
    RW          bool   `json:"RW"`
    Propagation string `json:"Propagation"`
}
```

**Steps:**
1. Implement `NewDockerInspector(socket string) DockerInspector`.
2. Accept `/var/run/docker.sock` and `unix:///var/run/docker.sock`.
3. Use `http.Client{Transport: &http.Transport{DialContext: ...}}` over Unix socket.
4. Call `GET /containers/{name}/json`; URL-escape name.
5. Return useful errors on non-2xx status.
6. Unit-test parsing with an `httptest`-style fake server or a fake inspector where simpler.

---

## Task 4: Implement path translation engine

**Objective:** Convert a client-visible mountpoint into a sidecar-visible mountpoint.

**Files:**
- Create: `internal/sidecar/translate.go`
- Create: `internal/sidecar/translate_test.go`

**Config structs:**
```go
type TranslationConfig struct {
    Enabled              bool
    HostRoot             string
    AllowedClientPrefixes []string
    AllowedHostPrefixes   []string
    RequiredLabels        map[string]string
}
```

**Core function:**
```go
func TranslateMountpoint(req protocol.Request, inspect ContainerInspect, cfg TranslationConfig) (TranslatedMountpoint, error)
```

**Return struct:**
```go
type TranslatedMountpoint struct {
    ClientPath       string
    HostPath         string
    SidecarPath      string
    MatchedSource    string
    MatchedDest      string
    ContainerName    string
    ContainerID      string
    Propagation      string
}
```

**Algorithm:**
1. Clean and validate `req.Mountpoint` absolute path.
2. Require it under one allowed client prefix.
3. Pick the longest bind mount whose cleaned `Destination` contains the client path.
4. Compute relative path under the destination.
5. Join with cleaned host `Source`.
6. Require host path under allowed host prefixes.
7. Join host path under cleaned `HostRoot`; e.g. `HostRoot=/host`, `HostPath=/storage/user/x` => `/host/storage/user/x`.
8. Return translation details.

**Tests:**
- `/storage/user/project/mnt` maps through `/storage/user -> /srv/users/bob` to `/host/srv/users/bob/project/mnt`.
- nested destination `/storage/user/private` wins over `/storage/user`.
- `/storage/user2/x` does not match `/storage/user`.
- bind mounts only; reject `volume`/`tmpfs`.
- reject host path outside allowed host prefixes.
- reject missing required labels.

---

## Task 5: Wire translation into daemon mount/unmount

**Objective:** Use direct validation in legacy mode and translated sidecar paths in Docker-inspect mode.

**Files:**
- Modify: `internal/sidecar/daemon.go`
- Modify: `internal/sidecar/daemon_test.go`
- Modify: `cmd/ccc-fuse-sidecar/main.go`

**Steps:**
1. Extend `sidecar.Config`:
   ```go
   DockerInspector DockerInspector
   Translation TranslationConfig
   ```
2. Add daemon helper:
   ```go
   func (d *Daemon) resolveMountpoint(ctx context.Context, req protocol.Request) (ResolvedMountpoint, error)
   ```
   Legacy mode returns client path as sidecar path.
3. In mount path, call `resolveMountpoint`, then `OpenValidatedMountpoint(resolved.SidecarPath, resolved.AllowedSidecarPrefixes)`.
4. In unmount path, translate the same way and unmount sidecar path.
5. Log client path and translated sidecar path when different.
6. Add tests with injected fake Docker inspector and injected Mount/Unmount funcs.

---

## Task 6: Add CLI/env configuration

**Objective:** Expose translation mode safely.

**Files:**
- Modify: `cmd/ccc-fuse-sidecar/main.go`
- Modify: `README.md`
- Modify tests as needed

**Flags:**
```text
--docker-socket /var/run/docker.sock
--host-root /host
--allow-client-prefix /storage/user   (repeatable)
--allow-host-prefix /storage          (repeatable)
--require-container-label key=value   (repeatable)
```

**Rules:**
1. Translation mode is enabled if `--docker-socket` is non-empty.
2. In translation mode, require `--host-root`, at least one client prefix, and at least one host prefix.
3. Legacy `--allow-prefix` remains required only when translation mode is disabled.
4. Reuse the existing prefix parsing behavior, including rejecting `/`.
5. Parse required labels with exactly one `=` and non-empty key.

---

## Task 7: Documentation and examples

**Objective:** Make deployment requirements explicit.

**Files:**
- Modify: `README.md`
- Create or modify: `examples/docker-compose-docker-inspect.yml`

**Docs must state:**
1. Legacy direct-prefix mode requires sidecar/client same target paths.
2. Docker-inspect mode supports different client paths/backing mounts by translating through Docker inspect.
3. Sidecar needs:
   - `/var/run/docker.sock` mounted read/write or read-only if Docker permits inspect;
   - host storage paths visible under `--host-root`, e.g. `/host`;
   - `CAP_SYS_ADMIN` and `/dev/fuse`;
   - mount propagation compatible with the client storage bind.
4. Client needs:
   - `CONTAINER_NAME` env set correctly;
   - `/dev/fuse` device mapping but no `SYS_ADMIN`;
   - shim socket path configured.
5. Security warning: Docker socket + host root visibility makes the sidecar trusted/host-powerful.

---

## Task 8: Validation

**Objective:** Prove static behavior and preserve smoke tests.

**Commands:**
```bash
export PATH=/home/domen/conda/envs/codex/bin:$PATH
gofmt -w $(find cmd internal -name '*.go' -type f | sort)
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build ./cmd/...
bash -n examples/*.sh tests/*.sh
docker compose -f tests/docker-compose.yaml config >/tmp/ccc-fuse-sidecar-tests-compose.yml
```

**Runtime FUSE test:** only run when `/dev/fuse` is available:
```bash
tests/run-docker.sh
```

Expected: existing archivemount read/write/flush smoke test passes.

---

## Future work after this plan

1. Verify socket peer PID/cgroup belongs to the claimed Docker container.
2. Support CRI/containerd/Kubernetes runtimes.
3. Support named volumes if needed.
4. Add structured JSON logs for translation decisions.
5. Cache Docker inspect results briefly and invalidate on failures.
