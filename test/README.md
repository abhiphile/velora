# Integration tests

Linux namespace and cgroups v2 integration tests live here behind the `integration` build tag because they require privileged kernel operations, a delegated cgroup hierarchy, and host utilities. Run them on Linux as root with `sudo env PATH="$PATH" go test -tags=integration ./test/integration -v`. Unit tests sit beside their Go packages. On macOS, Linux builds and integration test sources can be cross-compiled but the kernel behavior cannot be exercised.
