# Repository Guidelines

## Project Structure & Module Organization

This is a Go CLI project for the `jmp` binary. Entry-point and command wiring live in `main.go`, with supporting root-level files such as `cli.go` (exit codes, output and message helpers), `config.go` and `cmd_sync.go`. Reusable implementation code belongs under `internal/`: `internal/store` handles persisted jump data, `internal/matcher` ranks matches, `internal/shell` handles shell integration, and `internal/tui` contains Bubble Tea UI code. Tests are colocated with packages (`main_test.go` drives the CLI end to end; each `internal/` package has its own `*_test.go`). Build outputs are `jmp`, `dist/` and `man/`; do not treat them as source.

## Build, Test, and Development Commands

- `make build`: builds the local `jmp` binary with version metadata.
- `make test`: runs `go test ./...` across all packages.
- `make lint`: runs `go vet ./...` for static checks.
- `make man`: generates man pages into `man/`.
- `make build-all`: cross-compiles release binaries into `dist/`.
- `make clean`: removes `jmp` and `dist/` build artifacts.
- `go run .`: runs the CLI directly during development.

Use `make install` only when intentionally installing to `/usr/local/bin/jmp`.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on edited Go files before review. Keep package names short and lowercase (`store`, `matcher`, `shell`, `tui`). Prefer small functions with clear responsibilities, and keep CLI concerns in the root command layer rather than mixing them into internal packages. Every database mutation goes through `store.Update` (the locked read-modify-write cycle), never a bare `Load` + `Save`. User-facing strings use `T(en, zh)`; failures return an error carrying one of the documented exit codes instead of calling `os.Exit`. Follow Go naming conventions: exported identifiers use `PascalCase`, unexported identifiers use `camelCase`, and tests use `TestName`.

## Testing Guidelines

Use the standard Go `testing` package. Add or update colocated `*_test.go` files for behavior changes, especially scoring, persistence, alias handling, and command behavior. Prefer table-driven tests for match cases and isolated temporary directories via `t.TempDir()` for file persistence. Run `make test` before opening a pull request.

## Commit & Pull Request Guidelines

This repository currently has no commit history to infer conventions from. Use concise Conventional Commit-style messages, for example `feat: add alias ranking` or `fix: handle missing store file`. Pull requests should include a short problem statement, the implementation approach, test results such as `make test`, and screenshots or terminal output when TUI or CLI behavior changes.

## Security & Configuration Tips

Avoid committing local databases, generated binaries, or machine-specific shell configuration. Keep path handling portable by using `filepath` utilities in Go instead of hard-coded separators.
