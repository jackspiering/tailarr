# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-08-11

### Added

- Hierarchical interactive menus: Status, Services, Auth keys, Configuration,
  Maintenance, with multi-select batch lifecycle actions.
- Interactive deploy env prompts for empty/placeholder values; shared auth key
  for batch deploys; store/paste flow for `TS_AUTHKEY`.
- Status overview with managed counts and container health classification.
- Compose override labels (`tailarr.managed`, `tailarr.service`, `tailarr.version`).
- Confirmations for replace/remove; `--yes` / `TAILARR_ASSUME_YES` support.
- First-run config create/edit; interactive `config` / `config edit`.
- Auth key rename (CLI + TUI); remove can offer to delete retained backups.
- One-liner install script (`scripts/install.sh`) with OS/arch detection and
  SHA256 verification of release assets.

### Fixed

- Install script warns when another `tailarr` is earlier on `PATH` and prefers
  replacing the first on-PATH binary when that directory is writable.

## [0.1.0] - 2026-08-11

First public Go release of Tailarr (`github.com/jackspiering/tailarr`).

### Added

- Initial Go scaffolding (CLI + Bubble Tea TUI).
- Config load/save (plain `KEY=VALUE`, env and flag overrides, atomic write, mode 600).
- Path and service-name validation, symlink refusal (including ancestry), atomic writes.
- Auth key store (mode 600, `tskey-auth-*` validation, redacted listings, RMW lock).
- ScaleTail catalog discovery (`list` / `deployed`).
- Doctor checks for commands, paths, and Docker reachability (exclusive write probe).
- Deploy / repair / update / stop / restart / remove with locks, backups, managed-only lifecycle.
- `deploy --authkey <name>` resolves empty `TS_AUTHKEY` from the store (never the secret on flags).
- Unit tests for pure logic including force-redeploy secret preservation and fail-closed remove (no Docker required).
- Binary-first install story documented in README.

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

[Unreleased]: https://github.com/jackspiering/tailarr/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/jackspiering/tailarr/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jackspiering/tailarr/releases/tag/v0.1.0
