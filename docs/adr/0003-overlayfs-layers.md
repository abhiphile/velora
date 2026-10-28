# ADR 0003: Use OverlayFS for private container writes

## Status

Accepted for the filesystem layering milestone.

## Context

Containers need to share immutable base filesystem content while isolating writes. Vellora already mounts a supplied rootfs inside the helper’s private mount namespace and enters it with `pivot_root`.

## Decision

Use Linux OverlayFS with one existing lower directory and a unique per-container upper, work, and merged directory. Mount OverlayFS in the child mount namespace through `internal/storage/overlay`; then hand the merged path to the existing rootfs manager. Preserve upper on exit. Remove the merged mountpoint after the child namespace ends. Keep runtime orchestration independent of mount syscalls.

## Consequences

- Base filesystem content is shared; first writes are copied up by the kernel.
- Upper/work must be unique per active mount and on the same filesystem.
- The kernel tears down namespace-local mounts at process exit.
- The initial reference count is process-local. It is sufficient while layer deletion is unsupported; persistent leases are required before deletion or multi-process container lifecycle management.
- OverlayFS kernel/backing filesystem compatibility is discovered at mount time.

## Alternatives considered

- Copying a complete root filesystem per container wastes storage and delays startup.
- AUFS is not a safe portability assumption because it is not generally an in-tree kernel filesystem.
- Btrfs snapshots require a Btrfs-backed store and do not union arbitrary directories.
