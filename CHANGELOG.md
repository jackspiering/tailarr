# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial Go rewrite scaffolding (CLI + Bubble Tea TUI).
- Config load/save (plain `KEY=VALUE`, env and flag overrides, atomic write).
- Path and service-name validation, symlink refusal, atomic writes.
- Auth key store (mode 600, `tskey-auth-*` validation, redacted listings).
- ScaleTail catalog discovery (`list` / `deployed`).
- Doctor checks for commands, paths, and Docker reachability.
- Deploy / repair / update / stop / restart / remove skeleton with locks and backups.
- Unit tests for pure logic (no Docker required).

## [0.1.0] - 2026-08-11

### Added

- First public Go tree for Tailarr (`github.com/jackspiering/tailarr`).
- Binary-first install story documented in README.

[Unreleased]: https://github.com/jackspiering/tailarr/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/jackspiering/tailarr/releases/tag/v0.1.0
