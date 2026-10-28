# Vellora Coding Guidelines

> **Project:** Vellora  
> **Language:** Go  
> **Architecture:** Clean Architecture + Domain Driven Design (where appropriate)  
> **Goal:** Build an educational, production-inspired OCI container runtime from first principles.

---

# Philosophy

Vellora is **not** a Docker clone.

The objective is to understand and implement container runtime concepts ourselves while maintaining production-quality engineering standards.

Every feature should prioritize:

- Readability
- Simplicity
- Modularity
- Testability
- Documentation
- Correctness
- Security
- Performance (only after correctness)

---

# Core Principles

## 1. Understand Before Implementing

Never copy code without understanding it.

Before implementing any subsystem:

- Understand the Linux concept.
- Understand why it exists.
- Understand alternative implementations.
- Document important design decisions.

Every Pull Request should answer:

> Why was this approach chosen?

---

## 2. Small Commits

Each commit should represent a single logical change.

Good:

- Add PID namespace support
- Implement cgroup memory controller
- Add OverlayFS mount helper

Bad:

- Complete runtime update

---

## 3. One Responsibility Per Package

Every package should have a single responsibility.

Example:

```text
namespace/
    pid.go
    uts.go
    mount.go

network/
    bridge.go
    veth.go
    nat.go

storage/
    overlay.go
    rootfs.go

runtime/
    container.go
    lifecycle.go
```

Never mix unrelated responsibilities.

---

# Project Structure

```text
cmd/
internal/
pkg/
configs/
docs/
examples/
scripts/
test/
```

Rules:

- `cmd` only starts the application.
- Business logic never lives in `main.go`.
- `internal` contains implementation details.
- `pkg` only contains reusable packages.
- Documentation belongs in `docs`.

---

# Dependency Direction

Always follow this dependency flow:

```text
CLI

↓

Runtime

↓

Subsystems

↓

Linux Kernel
```

Subsystems must not depend on CLI.

Example:

```
runtime
 ├── namespace
 ├── cgroup
 ├── storage
 ├── network
```

NOT

```
namespace
    imports cli ❌
```

---

# Coding Style

Follow standard Go conventions.

## Variable Names

Good

```go
containerID
rootfs
bridgeName
memoryLimit
```

Bad

```go
cid
tmp1
abc
```

Avoid unnecessary abbreviations.

---

## Function Size

Maximum recommended function length:

**40–60 lines**

Split complex logic into helper functions.

---

## File Size

Recommended:

< 300 lines

If a file becomes larger, split it.

---

# Interfaces

Define interfaces where multiple implementations are expected.

Good:

```go
type Runtime interface {
    Run(ctx context.Context, cfg Config) error
    Stop(id string) error
}
```

Avoid interfaces with only one implementation unless future extensibility is expected.

---

# Error Handling

Never ignore errors.

Bad

```go
_ = os.Remove(path)
```

Good

```go
if err := os.Remove(path); err != nil {
    return fmt.Errorf("remove %s: %w", path, err)
}
```

Wrap every error with context.

---

# Logging

Use:

```go
log/slog
```

Never use:

```go
fmt.Println()
```

Good

```go
logger.Info(
    "container started",
    "id", id,
    "pid", pid,
)
```

---

# Configuration

Never hardcode:

- Paths
- Ports
- Runtime directories

Use configuration.

---

# Context

Every long-running operation should accept:

```go
context.Context
```

Never ignore context cancellation.

---

# Concurrency

Use goroutines carefully.

Rules:

- Never leak goroutines.
- Every goroutine must terminate.
- Protect shared state.
- Prefer channels over mutexes when appropriate.
- Keep synchronization simple.

---

# Testing

Every exported function should have tests.

Preferred testing pyramid:

- Unit tests
- Integration tests
- End-to-end tests

Avoid testing private implementation details.

---

# Documentation

Every exported type, function, and interface should have a GoDoc comment.

Example:

```go
// Runtime manages the lifecycle of containers.
type Runtime struct {}
```

---

# Comments

Write comments that explain **why**, not **what**.

Bad

```go
// increment i
i++
```

Good

```go
// PID 1 requires special signal handling inside a container.
```

---

# Package Design

Packages should expose minimal public APIs.

Avoid large utility packages.

Instead of:

```
utils/
```

Prefer:

```
filesystem/
network/
process/
```

---

# Security

Never assume input is trusted.

Validate:

- paths
- container names
- image names
- IDs
- configuration

Avoid shell execution whenever possible.

Prefer Go libraries over invoking external commands.

---

# Linux Philosophy

Every Linux-specific feature should be implemented using kernel primitives whenever practical.

Examples:

- namespaces
- cgroups v2
- OverlayFS
- netlink
- mount
- clone

Avoid unnecessary wrappers.

---

# Performance

Correctness first.

Measure before optimizing.

If optimization is required:

- Benchmark first.
- Document why.
- Verify improvement.

---

# Architecture Decisions

Every major subsystem should include an Architecture Decision Record (ADR).

Location:

```text
docs/adr/
```

Example:

```
0001-use-overlayfs.md

0002-cgroups-v2.md

0003-runtime-layout.md
```

---

# Git Workflow

Use conventional commits.

Examples:

```
feat(runtime): implement process lifecycle

feat(namespace): add PID namespace support

feat(storage): mount overlay filesystem

fix(network): resolve bridge cleanup issue

docs(runtime): explain namespace architecture

test(cgroup): add memory controller tests
```

---

# Pull Request Checklist

Before opening a PR, ensure:

- Code builds successfully.
- Tests pass.
- Lint passes.
- Formatting applied.
- Documentation updated.
- No unnecessary dependencies added.
- Public APIs documented.
- Errors wrapped with context.

---

# Milestones

## v0.1

- CLI
- Process execution
- PID namespace
- UTS namespace
- Mount namespace
- cgroups v2

---

## v0.2

- Root filesystem
- OverlayFS
- Container lifecycle

---

## v0.3

- Bridge networking
- veth pairs
- Port mapping

---

## v0.4

- OCI image support
- Image pulling
- Layer management

---

## v0.5

- Volumes
- Logs
- Stats
- Inspect
- Exec

---

## v1.0

Production-ready educational runtime with complete documentation, testing, observability, and stable APIs.

---

# Guiding Principle

> Every line of code in Vellora should be understandable, maintainable, and teach something about how container runtimes work. If a feature makes the code harder to understand without providing clear value, reconsider the design.
