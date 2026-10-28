# ADR 0001: Modular runtime layout

- **Status:** Accepted
- **Date:** 2026-10-29

## Context

Vellora is an educational runtime that must grow subsystem by subsystem without putting kernel details in CLI code or in `main`.

## Decision

Keep the executable in `cmd/vellora`, orchestration and implementation packages private under `internal`, and separate process, namespace, cgroup, storage, image, and network responsibilities. Define narrow interfaces at expected substitution or orchestration boundaries. Reserve `pkg` for APIs only after they are stable enough for external users.

## Consequences

The dependency flow remains CLI → runtime → subsystems → kernel. Early interfaces are intentionally small and do not imply that the corresponding Linux functionality is available yet.
