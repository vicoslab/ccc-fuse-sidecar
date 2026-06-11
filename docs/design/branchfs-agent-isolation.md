# BranchFS Local Agent Isolation Design

## Summary

CCC does **not** need distributed FUSE mounts for the BranchFS agent-isolation
use case. The live FUSE mount only needs to exist on the node where the agent is
running. The global CCC storage contract is restored when reviewed BranchFS
changes are committed back to the real NFS-backed `/home` / `/storage` tree.

The design goal is:

> Run an agent against a local BranchFS branch view, prevent unreviewed writes to
> shared CCC storage, then review and commit accepted changes back to the real
> NFS-backed project path.

This avoids sidecar-to-sidecar replication, cross-node FUSE forwarding, and any
attempt to make temporary agent branches globally visible.

## Goals

1. **Protect shared CCC storage from unreviewed agent writes.**
   Agents should modify a BranchFS delta/branch first, not the real shared
   project directory.
2. **Keep FUSE local to the agent node.**
   BranchFS mounts are node-local runtime state. Accepted changes become global
   only after commit to the NFS-backed underlay.
3. **Use the existing CCC container security model.**
   The agent runs inside a CCC container as a non-root user. Root/sudo is not
   available to the agent, the container is not privileged, and non-data paths are
   read-only.
4. **Use chroot as the agent view boundary.**
   A trusted launcher prepares a chroot rooted at the BranchFS view, drops to the
   non-root agent user, and execs the agent inside that root.
5. **Keep `ccc-fuse-sidecar` simple.**
   The sidecar remains a local privileged FUSE mount broker. It should not become
   a distributed filesystem or a cluster-wide mount coordinator.
6. **Make review/commit explicit and hookable.**
   Completion hooks freeze the branch, compute status/diffs, run policy checks,
   and either auto-commit safe changes or keep the branch for human review.

## Non-goals

1. No sidecar-to-sidecar FUSE replication.
2. No global live BranchFS mount visible on every CCC node.
3. No attempt to mirror arbitrary interactive `fusermount3` calls to other nodes.
4. No network FUSE relay/proxy between nodes.
5. No guarantee that a malicious root process inside the container is contained by
   chroot alone. This design assumes the agent remains non-root and without
   dangerous capabilities.
6. No direct writes by the agent to the real NFS-backed project while the branch
   session is active.

## Assumptions

The design relies on these CCC assumptions:

- the agent process runs inside a CCC compute container;
- the agent runs as a non-root user;
- root/sudo is disabled for the agent;
- the app/agent container does not receive `--privileged` or `CAP_SYS_ADMIN`;
- the Docker socket is not exposed to the agent;
- paths outside the intended data areas such as `/home`, `/storage`, and `/tmp`
  are read-only or absent from the agent view;
- `/tmp` used by the agent is private to the branch session;
- `/home/<user>` and `/storage/user` aliases are handled consistently and do not
  expose the real underlay alongside the BranchFS view;
- `ccc-fuse-sidecar` is available locally on the node to perform privileged FUSE
  mount operations for BranchFS.

A trusted launcher may need enough privilege during setup to create mounts and
call `chroot(2)`. That privilege must not be inherited by the agent process. The
launcher must drop to the final non-root UID/GID before executing the agent.

## High-level architecture

```text
real shared project on NFS
  /storage/user/Projects/project-a
        |
        | BranchFS base/underlay, read by BranchFS
        v
node-local BranchFS mount for one agent session
  /run/ccc-agent-sessions/<session-id>/branch
        |
        | visible inside chroot as the project workspace
        v
agent chroot
  /
  /workspace/project-a        -> BranchFS branch view
  /home/<user>/Projects/...   -> BranchFS branch view, if needed
  /storage/user/Projects/...  -> BranchFS branch view, if needed
  /tmp                        -> private session tmp

agent writes
  -> BranchFS delta/store
  -> review hooks
  -> commit accepted changes to /storage/user/Projects/project-a
  -> NFS makes committed result visible on all nodes
```

## Required components

### 1. `ccc-fuse-sidecar`

Local privileged FUSE broker only.

Responsibilities:

- own `/dev/fuse` and `CAP_SYS_ADMIN`;
- perform local `mount(2)` / `unmount(2)` for BranchFS;
- pass FUSE file descriptors to the unprivileged BranchFS process;
- validate allowed mount paths;
- stay local to the node.

Out of scope:

- cluster-wide mount replication;
- knowing BranchFS review policy;
- knowing agent lifecycle;
- committing changes to NFS.

### 2. BranchFS daemon/process

Unprivileged userspace filesystem for one branch session.

Responsibilities:

- expose the project branch view;
- store writes/deletes in a session delta;
- provide status/diff information;
- freeze the branch for review;
- commit accepted changes to the real underlay;
- abort/discard unaccepted changes.

### 3. Trusted agent supervisor, e.g. `ccc-agent-run`

The lifecycle owner for an agent session.

Responsibilities:

1. allocate a session ID;
2. create session directories;
3. start/mount BranchFS through `ccc-fuse-sidecar`;
4. assemble the chroot root;
5. remove or hide real writable underlays;
6. drop privileges and exec the agent;
7. detect agent completion;
8. freeze BranchFS;
9. run review/policy hooks;
10. commit, abort, or keep for review;
11. unmount and clean up.

### 4. Chroot root

A minimal filesystem view for the agent.

Requirements:

- project workspace paths resolve to the BranchFS view;
- real `/storage/user/<project>` and `/home/<user>/<project>` underlays are not
  reachable as writable paths;
- `/tmp` is private per session;
- system/runtime paths are read-only where possible;
- no Docker socket;
- no privileged sidecar control paths unless explicitly required;
- no inherited file descriptors to the real underlay.

### 5. Review and commit hooks

Hookable policy layer that runs after the agent finishes.

Required hook stages:

1. `pre-freeze` or `on-agent-exit`;
2. `freeze` BranchFS;
3. `status` / `diff` generation;
4. `policy-check`;
5. `pre-commit`;
6. `commit` or `mark-pending-review`;
7. `cleanup`.

## Session layout

Suggested runtime layout:

```text
/run/ccc-agent-sessions/<session-id>/
  root/                 # chroot root
  branch/               # BranchFS mountpoint
  tmp/                  # private /tmp
  logs/
  metadata.json
```

Suggested durable review layout on shared storage:

```text
/storage/user/.ccc/agent-sessions/<session-id>/
  session.yaml
  status.json
  diff.patch
  policy.json
  review.md
```

The BranchFS delta/store may be local or shared:

- local SSD/HDD: faster, simpler runtime cleanup, but weaker recovery if the node
  dies;
- shared storage: slower, but easier to inspect/recover/review from another node.

For a first implementation, shared metadata/review artifacts plus either local or
shared delta storage are acceptable. The live FUSE mount remains local either way.

## Chroot and privilege requirements

`chroot` is acceptable for this CCC threat model because the agent is non-root and
runs inside an already constrained container. The supervisor must still enforce
these requirements:

1. Perform setup as a trusted process, then execute:
   - close unrelated inherited file descriptors;
   - `chroot(newroot)`;
   - `chdir("/")`;
   - drop supplementary groups;
   - `setgid(agent_gid)`;
   - `setuid(agent_uid)`;
   - exec the agent.
2. The agent must not have:
   - root/sudo;
   - `CAP_SYS_ADMIN`;
   - `CAP_SYS_CHROOT`;
   - `CAP_DAC_OVERRIDE`;
   - Docker socket access;
   - writable access to the real NFS underlay.
3. If `/proc` is mounted inside the chroot, it should be the container-limited
   `/proc` and should not expose host namespaces or privileged process file
   descriptors.
4. Setuid helpers should be removed or made unavailable where practical.
5. The BranchFS mount and the real underlay must never both be exposed as writable
   aliases inside the chroot.

## Path model

The safest first implementation is project-scoped:

```text
base project:
  /storage/user/Projects/project-a

agent-visible workspace:
  /workspace/project-a
```

If CCC compatibility requires standard aliases, both aliases must point to the
same BranchFS branch view:

```text
/home/<user>/Projects/project-a      -> BranchFS view
/storage/user/Projects/project-a     -> BranchFS view
```

Never expose this combination:

```text
/workspace/project-a                 -> BranchFS view
/storage/user/Projects/project-a     -> real NFS underlay
```

That would let the agent bypass BranchFS.

## Lifecycle

Example lifecycle for `ccc-agent-run`:

```text
1. Resolve target project path on real NFS.
2. Allocate session ID.
3. Create session runtime and review directories.
4. Start BranchFS against the real project underlay.
5. Mount BranchFS locally through ccc-fuse-sidecar.
6. Assemble chroot root and bind/read-only runtime paths.
7. Exec agent as non-root inside chroot.
8. Wait for process exit or explicit finish signal.
9. Freeze BranchFS.
10. Generate status/diff/review artifacts.
11. Run policy hooks.
12. If policy passes, commit delta to the real NFS project path.
13. If policy fails, keep session pending review.
14. Unmount BranchFS and clean up runtime state when safe.
```

## Completion model

For the first implementation, prefer one-shot agent runs:

```bash
ccc-agent-run --project /storage/user/Projects/project-a -- codex ...
```

Process exit is the completion signal.

For server-style agents, completion must be explicit or adapter-specific. Idle
heuristics can create review reminders, but they should not auto-commit.

## Review and policy requirements

Policy should be based on actual BranchFS status after freeze, not on the agent's
self-report.

Minimum policy:

```text
if no changes:
  close as no-op
elif all changes are under allowed project scope and no deny rule matches:
  auto-commit, if configured
else:
  mark pending-review
```

Recommended deny/review triggers:

- writes outside the requested project scope;
- secret/key material;
- `.ssh` changes;
- `.git/config` or `.git/hooks` changes;
- global shell startup files;
- Conda/base environment mutations outside allowed envs;
- large unexpected deletes;
- symlink escapes;
- writes to shared group storage unless explicitly allowed.

## Commit requirements

Committing BranchFS changes to the real NFS underlay must be deliberate and
observable.

Requirements:

- commit only after freeze;
- record exactly what was committed;
- preserve file modes and symlinks intentionally;
- handle deletes/tombstones explicitly;
- fail safely on conflicts or changed underlay state;
- write durable review artifacts before or during commit;
- make committed results visible through normal NFS paths.

## Failure handling

Expected failure modes:

- agent crashes;
- BranchFS daemon crashes;
- node dies;
- commit conflicts with underlay changes;
- policy hook fails;
- unmount fails because processes still hold files open.

Required behavior:

- do not auto-commit on uncertain state;
- preserve review artifacts and deltas when possible;
- mark session as `pending-review` or `failed`;
- allow manual abort/commit/retry;
- cleanup local mounts only after data is safe.

## Minimal first milestone

A useful first implementation should support:

1. one project path;
2. one local BranchFS mount;
3. one one-shot agent process;
4. chroot into the BranchFS view;
5. private `/tmp`;
6. no real writable `/home` or `/storage` underlay inside chroot;
7. process-exit completion;
8. BranchFS status/diff artifact generation;
9. manual commit or abort.

Auto-commit, server-agent adapters, multi-root sessions, and advanced recovery can
come later.

## Open questions

1. Where should BranchFS delta data live by default: local SSD or shared storage?
2. Should the first chroot expose only `/workspace`, or also CCC-compatible
   `/home/<user>` and `/storage/user` aliases?
3. Which runtime/tool paths must be writable for Codex, Claude, Hermes, and
   OpenClaw?
4. Which policy hooks are required before any auto-commit is allowed?
5. How should the supervisor represent sessions for user commands such as
   `list`, `show`, `diff`, `commit`, and `abort`?
6. What is the minimum BranchFS API needed by the supervisor: `mount`, `freeze`,
   `status`, `diff`, `commit`, `abort`, `unmount`?
