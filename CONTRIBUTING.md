# Contributing

Thanks for helping build Vellora. Read [coding-guidelines.md](coding-guidelines.md) before making changes.

## Development workflow

1. Keep each change focused and explain why the design was chosen.
2. Use conventional commit subjects such as `feat(runtime): define lifecycle interface`.
3. Run `make fmt`, `make test`, `make build`, and `make lint` before opening a pull request.
4. Add or update documentation and tests with behavior changes. Linux kernel work should include a short explanation of the relevant primitive.

Pull requests should describe the problem, approach, validation, and any limitations. Do not claim container isolation until the corresponding kernel features are implemented and tested.
