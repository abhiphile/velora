# ADR 0002: Private read-only root filesystem

## Status

Accepted

## Context

Vellora must give a process a supplied filesystem tree as `/` without exposing the host root mount tree. The runtime already starts a new mount namespace, so mount operations can be scoped to the container. The milestone intentionally excludes image layers and OverlayFS.

## Decision

- Keep rootfs orchestration in `internal/storage` behind `RootFSManager` and `MountManager` interfaces.
- Recursively mark inherited mounts private before changing the root.
- Bind-mount the caller's rootfs onto itself, then use the documented `pivot_root(".", ".")` sequence to avoid creating a persistent staging directory in the source tree.
- Detach the old root with `MNT_DETACH`.
- Make the supplied rootfs read-only. Use separate tmpfs mounts for writable temporary/device paths.
- Mount procfs only after the child has entered its PID namespace.
- Let mount namespace teardown reclaim successful mounts; explicitly roll back partial mounts on setup errors.

## Consequences

Writes under the supplied rootfs fail by default, protecting its host-side backing tree. Workloads can write to `/tmp` and `/dev/shm`, but those files do not persist. The approach does not provide a writable image layer, a full device-management policy, capability reduction, or a complete security boundary. OverlayFS and stronger privilege isolation require later milestones.
