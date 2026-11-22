# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-11-22

A hardening pass that holds jmp to the standard of a mature Unix CLI: no
silent data loss, a sync that cannot damage the remote, and predictable exit
codes and output.

### Added
- TUI: press `+` to manually boost the score of the selected row (or every
  marked row) by adding weight, without faking a visit.
- Documented exit codes: `0` success, `1` runtime error, `2` usage error,
  `3` no match / not found, `4` sync could not reach or update the remote.
- `--version`, `-v/--verbose` and `--quiet` global flags.
- `JMP_DB` environment variable (also honoured by the shell integration) and
  `JMP_LANG` (`en` / `zh`). Help and messages now follow the locale instead of
  mixing Chinese and English.
- `jmp sync --dry-run` (`-n`) and itemised changes with `-v`; `jmp sync unset`;
  `jmp sync set --interval`; a local path (e.g. a mounted share) is accepted as
  the remote. `jmp sync status` shows the last attempt and the last error.
- `jmp list [keyword...]`, `--paths` and `--no-header` for scripting.
- `jmp add` / `jmp remove` accept several paths; `jmp import -` reads stdin.
- Man pages: `make man`; shipped in release archives and the Homebrew formula.
- Daily database snapshot (`db.json.bak`) and automatic recovery from a
  corrupt database, with a warning.
- `j -- back` (bash / zsh / fish) queries a directory literally named like a
  navigation keyword.

### Fixed
- **Data loss on jump**: `jmp jump` / `j` no longer delete records of
  directories that are missing at that moment. An unmounted external or
  network volume used to lose all its records and weights. Pruning is now done
  only by `jmp clean`. (Supersedes the pruning half of the 0.2.1 fix; missing
  directories are still never chosen as a jump target.)
- **Data loss on `alias set` and `import`**: both used to overwrite the weight
  of an already recorded directory (with 10 and 1 respectively).
- **Lost updates**: every write is now a cross-process locked
  read-modify-write, in the CLI and the TUI. Concurrent shells used to
  overwrite each other.
- **Sync could overwrite the remote after a failed pull.** Any pull failure
  was treated as "first sync" and followed by a push. Now only a remote file
  that verifiably does not exist is seeded; every other failure aborts.
- **Sync resurrected deletions.** Removed paths and aliases now propagate
  through tombstones; alias conflicts resolve to the most recently set one.
- Remote uploads are atomic (temporary file + rename).
- Background sync can no longer prompt for a password or hang (`BatchMode`,
  connect and overall timeouts), retries a failing remote once per interval
  instead of on every prompt, and never runs twice at once.
- PowerShell no longer blocks the prompt while syncing: `jmp sync auto`
  detaches on every shell.
- bash and PowerShell recorded a visit on every prompt; they now record one
  per directory change, like zsh and fish.
- The shell integration quotes the binary path (spaces are safe), prefers the
  stable `PATH` location over a versioned package-manager path, completes
  names containing spaces in bash, and shows real errors instead of reporting
  them as "no match".
- Runtime errors no longer dump the full usage text.
- `jmp add` rejects paths that are not existing directories; alias names are
  validated; unexpected positional arguments are rejected.
- `--json` now produces JSON for every command, including errors.
- `jmp clean --dry-run` lists what it would remove instead of only counting.
- `sync.json` and the sync state follow `--db` / `JMP_DB` like `config.json`.
- The Go module path is now `github.com/biyan113/jmp`, matching the
  repository, so `go install github.com/biyan113/jmp@latest` works.

### Changed
- Exit codes: `jmp remove` on an unknown path now exits `3` (was `2`);
  `jmp alias remove` on an unknown alias exits `3` (was `1`); usage errors
  exit `2` (was `1`).
- `jmp add --json` reports `{"added": [...], "rejected": [...]}` (was a single
  `"added"` string); `jmp remove --json` reports `removed` / `not_found` lists.
- `jmp add -w` is documented as what it does: it sets the weight.
- `jmp sync push` is documented as an overwrite without merge.
- `jmp list` prints full dates and no separator line.
- The sync timestamp file `.last_sync` is replaced by `sync_state.json`.
- Removed the unreachable "jump" branch of the bare `jmp <query>` form; use
  `j <query>` or `jmp jump <query>`.

## [0.2.1] - 2026-08-21

### Fixed
- Jumping no longer lands on a deleted or moved directory. Missing paths are
  skipped in ranking and pruned from the database on `jmp jump` / `j`, so the
  next live match (for example the folder after a rename/move) is used instead
  of erroring on the stale record.

## [0.2.0] - 2026-07-25

### Added
- Homebrew installation: `brew install biyan113/tap/jmp` (macOS / Linux).
  GoReleaser now publishes a Formula to
  [biyan113/homebrew-tap](https://github.com/biyan113/homebrew-tap).
- `j` now acts as `cd` on a database miss: it first queries the jmp DB, and on
  no match falls back to treating the argument as a directory path (`~`
  expansion, absolute paths, and relative names all work). Works across bash,
  zsh, fish, and PowerShell.
- `jmp version` subcommand and `--version` injection at build time
  (`-ldflags "-X main.version=..."`).
- Shell-integration tests for all four supported shells.

### Fixed
- **Sync data safety**: `jmp sync` now aborts the push when the pull or merge
  step fails, instead of overwriting the remote with a stale/empty local copy.
- **Cross-platform import**: `jmp import` now recognizes Windows drive paths
  (`C:\...`) in addition to unix `~` and `/`.
- Sync temp files now use `os.CreateTemp` to avoid name collisions under
  concurrency.

### Changed
- TUI version label reads the build-injected version instead of a hardcoded
  string.
- Removed dead code (`tui.truncate`) and redundant local `min`/`max` helpers
  (Go 1.21+ builtins are now used).

## [0.1.0] - 2026-07-23

### Added
- Initial release of `jmp`, a smart directory jumper and autojump enhancement.
- Frecency-based ranking with substring > fuzzy matching, multi-keyword queries.
- `@alias` quick-jump system.
- Interactive TUI manager (Bubble Tea): search, star, categorize, edit weight,
  delete, preview, filter tabs.
- Shell integration for bash, zsh, fish, and PowerShell with tab completion
  and `back` / `fwd` / `-` navigation history.
- Multi-device sync over SSH/SCP (pull → merge → push).
- Maintenance commands: `doctor`, `clean`, `stats`, `import`.
- JSON output mode (`--json`) and custom database path (`--db`).

[Unreleased]: https://github.com/biyan113/jmp/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/biyan113/jmp/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/biyan113/jmp/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/biyan113/jmp/releases/tag/v0.2.0
[0.1.0]: https://github.com/biyan113/jmp/releases/tag/v0.1.0
