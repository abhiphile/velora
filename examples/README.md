# Examples

Vellora can run a prepared Linux root filesystem with `vellora run --rootfs /path/to/rootfs -- /bin/sh`. The root directory must contain the requested executable, its dynamic loader and libraries, and mountpoint directories `/proc`, `/tmp`, and `/dev`.

This repository does not download, unpack, or build a distribution rootfs. The supplied rootfs is bind-mounted read-only; `/tmp`, `/dev`, and `/dev/shm` are private mounts. See [rootfs isolation](../docs/rootfs.md) before running it.
