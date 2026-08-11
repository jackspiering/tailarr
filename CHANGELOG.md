# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial Go rewrite scaffolding (CLI + Bubble Tea TUI).
- Config load/save (plain `KEY=VALUE`, env and flag overrides, atomic write, mode 600).
- Path and service-name validation, symlink refusal (including ancestry), atomic writes.
- Auth key store (mode 600, `tskey-auth-*` validation, redacted listings, RMW lock).
- ScaleTail catalog discovery (`list` / `deployed`).
- Doctor checks for commands, paths, and Docker reachability (exclusive write probe).
- Deploy / repair / update / stop / restart / remove with locks, backups, managed-only lifecycle.
- `deploy --authkey <name>` resolves empty `TS_AUTHKEY` from the store (never the secret on flags).
- Unit tests for pure logic including force-redeploy secret preservation and fail-closed remove (no Docker required).

### Security

- Reject repository URLs with embedded credentials; redact userinfo in config display.
- Auth key interactive input uses hidden terminal read; non-interactive input is single-line and size-bounded.
- Force redeploy merges backup `.env` secrets; restores previous deployment if copy/up fails.
- Remove fails closed when `compose down` fails (directory left intact).
- Lifecycle ops refuse unmanaged directories (no Tailarr marker).
- Locks are ownership-bound (PID+token); Release does not steal another process's lock.
- Compose project names include deploy-root fingerprint to reduce cross-root collisions.

### Fixed

- Log rotation runs on every event (not once per process).
- Service locks live under `deployPath/.tailarr_locks` (consistent with backups).
- Git commit SHA pins clone/checkout without invalid `--branch` usage; detached HEAD can rejoin default branch for unpinned pull.
- golangci-lint / misspell CI (errcheck, empty branches, US `Canceled` exit code name).

## [0.1.0] - 2026-08-11

### Added

- First public Go tree for Tailarr (`github.com/jackspiering/tailarr`).
- Binary-first install story documented in README.

[Unreleased]: https://github.com/jackspiering/tailarr/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/jackspiering/tailarr/releases/tag/v0.1.0
