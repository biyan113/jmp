# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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

[Unreleased]: https://github.com/biyan113/jmp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/biyan113/jmp/releases/tag/v0.1.0
