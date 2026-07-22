# Contributing to jmp

Thanks for your interest in improving jmp! This guide covers the basics.

## Prerequisites

- Go 1.21 or newer
- `make` (optional, but convenient)

## Getting started

```bash
git clone https://github.com/biyan113/jmp.git
cd jmp
make build        # produces ./jmp
./jmp version
```

Run the CLI directly during development:

```bash
go run . --help
go run . list
```

## Development workflow

| Task | Command |
|------|---------|
| Build the binary | `make build` |
| Run tests | `make test` |
| Static checks | `make lint` (runs `go vet`) |
| Cross-compile all platforms | `make build-all` |
| Clean build artifacts | `make clean` |

Always run `make test` and `make lint` before opening a pull request.

## Project layout

- `main.go`, `cmd_sync.go`, `config.go` — CLI entry point and command wiring
- `internal/store` — persisted jump database (frecency, aliases, merge)
- `internal/matcher` — ranking and suggestions
- `internal/shell` — generated shell-integration scripts (bash/zsh/fish/pwsh)
- `internal/tui` — Bubble Tea interactive manager

Keep CLI concerns in the root command layer; put reusable logic in `internal/`
packages so it stays testable.

## Coding style

- Run `gofmt` / `goimports` on edited Go files.
- Follow standard Go naming: exported identifiers use `PascalCase`,
  unexported use `camelCase`, tests use `TestName`.
- Prefer small functions with clear responsibilities.

## Testing

Use the standard `testing` package. Colocate `*_test.go` files with the code
they cover. Prefer table-driven tests for matching/scoring cases and
`t.TempDir()` for anything that touches the filesystem. See
`internal/store/store_test.go` for examples.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/) style:

```
feat: add alias ranking boost
fix: handle missing store file on first run
docs: clarify sync interval units
test: cover fuzzy match edge cases
refactor: simplify merge loop
```

## Pull requests

- Keep PRs focused — one feature or fix per PR.
- Include the problem statement and approach in the description.
- Show `make test` output.
- Add screenshots/terminal output when CLI or TUI behavior changes visibly.

## Reporting bugs

Open an issue with:

1. jmp version (`jmp version`)
2. OS and shell
3. Exact steps to reproduce
4. Expected vs actual behavior
5. Any relevant output / screenshots
